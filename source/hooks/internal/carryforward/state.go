package carryforward

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	contextPercentThreshold  = 65
	transcriptDeltaThreshold = 400 * 1024
	sensorMaxAge             = 5 * time.Minute
	nudgesBeforeBackstop     = 2
	rearmPercentMargin       = 10
)

// state is one session's nudge ladder and its place in the harness transcript.
type state struct {
	TranscriptOffsetAtRefresh int64 `json:"transcript_offset_at_refresh"`
	LogOffset                 int64 `json:"log_offset"`
	Nudges                    int   `json:"nudges"`
	PercentRung               *int  `json:"percent_rung"`
	Turns                     int   `json:"turns"`
}

type sensor struct {
	UsedPercentage *float64 `json:"used_percentage"`
	At             float64  `json:"at"`
	Size           float64  `json:"size"`
}

func (hook *hook) statePath(session string) string {
	return filepath.Join(hook.transcriptsDir(), session+".state")
}

func (hook *hook) loadState(session string) state {
	var held state

	data, err := os.ReadFile(hook.statePath(session))
	if err != nil {
		return state{}
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		return state{}
	}

	return held
}

func (hook *hook) saveState(session string, held state) error {
	err := os.MkdirAll(hook.transcriptsDir(), 0o755)
	if err != nil {
		return fmt.Errorf("creating the transcripts directory: %w", err)
	}

	return writeJSON(hook.statePath(session), held)
}

// contextPercent is how full the session's context is, as a share of the
// window it compacts at, or false where no fresh reading exists. The sensor
// reports a share of the model's whole window, while a session may compact at
// a smaller one: every rung is a share of the window it actually compacts at.
func (hook *hook) contextPercent(session string) (float64, bool) {
	data, err := os.ReadFile(filepath.Join(hook.transcriptsDir(), session+".ctx"))
	if err != nil {
		return 0, false
	}

	var reading sensor

	err = json.Unmarshal(data, &reading)
	if err != nil || reading.At == 0 || reading.UsedPercentage == nil {
		return 0, false
	}

	recorded := time.Unix(0, int64(reading.At*float64(time.Second)))
	if hook.env.Now().Sub(recorded) > sensorMaxAge {
		return 0, false
	}

	percent := *reading.UsedPercentage

	window := float64(hook.env.CompactWindow)
	if reading.Size > 0 && window > 0 && window < reading.Size {
		return percent * reading.Size / window, true
	}

	return percent, true
}

func rungOf(held state) int {
	if held.PercentRung == nil || *held.PercentRung == 0 {
		return contextPercentThreshold
	}

	return *held.PercentRung
}

// thresholdCrossed decides whether the session is near its compaction: by the
// context sensor where a fresh reading exists, by transcript growth otherwise.
// A refresh does not shrink the context, so the reading stays past the
// threshold and the trigger re-arms one fixed rung at a time.
func (hook *hook) thresholdCrossed(session string, transcript string, held state) bool {
	percent, known := hook.contextPercent(session)
	if known {
		return percent >= float64(rungOf(held))
	}

	info, err := os.Stat(transcript)
	if err != nil {
		return false
	}

	return info.Size()-held.TranscriptOffsetAtRefresh >= transcriptDeltaThreshold
}
