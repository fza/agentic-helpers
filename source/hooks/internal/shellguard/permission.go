package shellguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

const (
	modeDontAsk = "dontAsk"
	modeAuto    = "auto"
	modeBypass  = "bypassPermissions"

	// After this many countdowns ran out unanswered since the session's last
	// prompt, nobody is watching: an ask denies at once.
	unansweredLimit = 3
)

var ErrUsage = errors.New("usage: agentic-shell-guard [permission|output]")

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Clock times an ask's countdown. Wait returns early, with an error, when the
// context ends: a hook stopped by a signal answers nothing.
type Clock interface {
	Now() time.Time
	Wait(ctx context.Context, duration time.Duration) error
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}

func (SystemClock) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for an answer: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// Run answers the hook event its arguments name: none for PreToolUse,
// `permission` for PermissionRequest, `output` for PostToolUse.
func Run(ctx context.Context, args []string, env Env, streams Streams) int {
	switch {
	case len(args) == 0:
		return Main(ctx, env, streams)
	case len(args) == 1 && args[0] == "permission":
		return Permission(ctx, env, streams)
	case len(args) == 1 && args[0] == "output":
		return Output(ctx, env, streams)
	}

	_, _ = fmt.Fprintln(streams.Err, self+": "+ErrUsage.Error())

	return 2
}

type permissionAnswer struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// Permission answers one PermissionRequest on Bash. In auto and
// bypassPermissions mode, for a call shell-guard asks about, it waits out the
// countdown and denies; whichever of the person and the hook answers first
// decides. Every other call, and anything it cannot read, it leaves to the
// person at the prompt.
func Permission(ctx context.Context, env Env, streams Streams) (code int) {
	defer func() {
		failure := recover()
		if failure != nil {
			code = 0
		}
	}()

	read, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	var call payload

	err = json.NewDecoder(streams.In).Decode(&call)
	if err != nil || call.ToolInput.Command == nil || !pieces.ShellGuard.On(read) {
		return 0
	}

	seconds := read.ShellGuardAskTimeout
	if call.PermissionMode != modeAuto && call.PermissionMode != modeBypass || seconds == 0 {
		return 0
	}

	verdict := judgeCall(ctx, env, read, call)
	if verdict.Decision != Ask {
		return 0
	}

	held := stateOf(ctx, env, call.SessionID)
	if held.unanswered(lastPrompt(ctx, call.TranscriptPath)) >= unansweredLimit {
		return deny(streams, fmt.Sprintf("%s: %d countdowns ran out unanswered since the last prompt; %s",
			self, unansweredLimit, verdict.Reason))
	}

	err = env.Clock.Wait(ctx, time.Duration(seconds)*time.Second)
	if err != nil {
		return 0
	}

	held.ranOut(env.Clock.Now())

	return deny(streams, fmt.Sprintf("%s: nobody answered within %ds; %s", self, seconds, verdict.Reason))
}

func deny(streams Streams, message string) int {
	var reply permissionAnswer

	reply.HookSpecificOutput.HookEventName = "PermissionRequest"
	reply.HookSpecificOutput.Decision.Behavior = "deny"
	reply.HookSpecificOutput.Decision.Message = message

	_ = json.NewEncoder(streams.Out).Encode(reply)

	return 0
}

// sessionState is what shell-guard keeps for a session: its countdowns that
// ran out, and how often each rewritable ask was refused. It lives in the
// session's slot of `<cache>/shell-guard/sessions.slots`, keyed by the session
// id. A session without a usable slot reads as fresh and keeps nothing.
type sessionState struct {
	ctx      context.Context
	file     *slots.File
	key      string
	RanOut   []time.Time    `json:"ran_out"`
	Attempts map[string]int `json:"attempts,omitempty"`
}

const (
	stateDir  = "shell-guard"
	slotsName = "sessions.slots"
	slotWidth = 4096
	// stateFormat is the shell-guard state's layout: one slot per session; 1
	// was a JSON file per session.
	stateFormat = 2
	staleState  = 7 * 24 * time.Hour
)

// StateSlots is shell-guard's slot file below the cache root.
func StateSlots(cache string) *slots.File {
	return slots.Open(filepath.Join(cache, stateDir, slotsName), slotWidth)
}

func stateOf(ctx context.Context, env Env, session string) *sessionState {
	held := &sessionState{ctx: ctx}

	if env.StateDir == "" || !sessionName.MatchString(session) {
		return held
	}

	file := StateSlots(env.StateDir)

	err := file.Adopt(ctx, filepath.Join(env.StateDir, stateDir), stateFormat, func() []slots.Legacy {
		return legacyStates(filepath.Join(env.StateDir, stateDir))
	})
	if err != nil {
		return held
	}

	held.file, held.key = file, session
	file.GetJSON(ctx, session, held)

	return held
}

// legacyStates is every per-session JSON file an earlier release kept.
func legacyStates(dir string) []slots.Legacy {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))

	var legacy []slots.Legacy

	for _, path := range paths {
		session := strings.TrimSuffix(filepath.Base(path), ".json")
		if sessionName.MatchString(session) {
			legacy = append(legacy, slots.Legacy{Path: path, Key: session})
		}
	}

	return legacy
}

func lastPrompt(ctx context.Context, transcriptPath string) time.Time {
	turns, err := transcript.Since(ctx, transcriptPath, time.Time{})
	if err != nil || len(turns) == 0 {
		return time.Time{}
	}

	return turns[len(turns)-1].At
}

func (held *sessionState) unanswered(since time.Time) int {
	count := 0

	for _, at := range held.RanOut {
		if at.After(since) {
			count++
		}
	}

	return count
}

// ranOut notes a countdown that ran out. A note it cannot write costs only the
// deny-at-once.
func (held *sessionState) ranOut(at time.Time) {
	held.RanOut = append(held.RanOut, at)
	held.save()
}

// save writes the state back, dropping the oldest countdowns where they would
// not fit the slot: only those since the last prompt count. It first clears
// every other session's slot no write touched for staleState, so the file
// holds the sessions of the last week and never grows past them.
func (held *sessionState) save() {
	if held.file == nil {
		return
	}

	cutoff := time.Now().Add(-staleState)
	_ = held.file.Reclaim(held.ctx, func(slot slots.Slot) bool {
		return slot.Key != held.key && slot.Written.Before(cutoff)
	})

	for {
		err := held.file.PutJSON(held.ctx, held.key, held)
		if !errors.Is(err, slots.ErrTooLarge) || len(held.RanOut) == 0 {
			return
		}

		held.RanOut = held.RanOut[len(held.RanOut)/2+len(held.RanOut)%2:]
	}
}
