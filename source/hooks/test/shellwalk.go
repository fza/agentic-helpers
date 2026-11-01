package test

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

// WalkRecorder notes every event a shellwalk walk hands it, each as one line:
// `call <words>`, `launched <words>` (with ` (certain)` where the walk is
// sure), `code <runner>: <words>`, `inline <name>: <words>`, `piped <name>`,
// `unseen <variable>`, `script <name>: <text>` (`script <name> by <interpreter>: ...` for another interpreter's), `unread <name>: <why>`. A
// call, code or script the walk places in a directory ends ` in <dir>`.
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

	event += inDir(call.Dir)

	recorder.Events = append(recorder.Events, event)
}

func (recorder *WalkRecorder) Code(runner string, words []*syntax.Word, dir string) {
	recorder.Events = append(recorder.Events, "code "+runner+": "+spelled(words)+inDir(dir))
}

func (recorder *WalkRecorder) Inline(name string, words []*syntax.Word) {
	recorder.Events = append(recorder.Events, "inline "+name+": "+spelled(words))
}

func (recorder *WalkRecorder) Piped(name string) {
	recorder.Events = append(recorder.Events, "piped "+name)
}

func (recorder *WalkRecorder) Unseen(variable string) {
	recorder.Events = append(recorder.Events, "unseen "+variable)
}

func (recorder *WalkRecorder) Script(name string, interpreter string, text string, within shellwalk.Scripts) {
	if interpreter != "" {
		name += " by " + interpreter
	}

	recorder.Events = append(recorder.Events, "script "+name+": "+strings.TrimSpace(text)+inDir(within.Dir))
}

func (recorder *WalkRecorder) Unread(name string, why string) {
	recorder.Events = append(recorder.Events, "unread "+name+": "+why)
}

// inDir names the directory a call or code runs in, where the walk knows it.
func inDir(dir string) string {
	if dir == "" {
		return ""
	}

	return " in " + dir
}

// RemovalPlaces walks every shell script the walk hands it as the walk would
// read it, and notes the directory each rm call there runs in, "" where the
// walk cannot tell.
type RemovalPlaces struct {
	WalkRecorder

	Dirs []string
}

func (places *RemovalPlaces) Command(call shellwalk.Call) {
	if shellwalk.CommandName(call.Args[0]) == "rm" {
		places.Dirs = append(places.Dirs, call.Dir)
	}
}

func (places *RemovalPlaces) Script(_ string, interpreter string, text string, within shellwalk.Scripts) {
	file, err := shellwalk.Parse(text)
	if interpreter != "" || err != nil {
		return
	}

	shellwalk.WalkWith(file, places, within)
}

func spelled(words []*syntax.Word) string {
	values := make([]string, 0, len(words))
	for _, word := range words {
		values = append(values, shellwalk.Spelled(word))
	}

	return strings.Join(values, " ")
}
