package test

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// WalkRecorder notes every event a shellwalk walk hands it, each as one line:
// `call <words>`, `launched <words>` (with ` (certain)` where the walk is
// sure), `code <runner>: <words>`, `inline <name>: <words>`, `piped <name>`.
type WalkRecorder struct {
	Events []string
}

func (recorder *WalkRecorder) Command(call shellwalk.Call) {
	kind := "call"
	if call.Launched {
		kind = "launched"
	}

	event := kind + " " + spelled(call.Args)
	if call.Launched && call.Certain {
		event += " (certain)"
	}

	recorder.Events = append(recorder.Events, event)
}

func (recorder *WalkRecorder) Code(runner string, words []*syntax.Word) {
	recorder.Events = append(recorder.Events, "code "+runner+": "+spelled(words))
}

func (recorder *WalkRecorder) Inline(name string, words []*syntax.Word) {
	recorder.Events = append(recorder.Events, "inline "+name+": "+spelled(words))
}

func (recorder *WalkRecorder) Piped(name string) {
	recorder.Events = append(recorder.Events, "piped "+name)
}

func spelled(words []*syntax.Word) string {
	values := make([]string, 0, len(words))
	for _, word := range words {
		values = append(values, shellwalk.Spelled(word))
	}

	return strings.Join(values, " ")
}
