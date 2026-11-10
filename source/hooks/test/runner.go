package test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/process"
)

// Runner answers `sdd [<command>] --help` from the help texts under
// fixtures/sdd: the fork's where Stock is false, stock sdd's where it is true.
// Missing stands for no sdd on the PATH, Unreleased takes the `export`
// command out of the help, as no stock release carries it, and NoRepo the
// `repo` command, as builds before connected repos lack it. It records every
// call it answers.
type Runner struct {
	Stock      bool
	Missing    bool
	Unreleased bool
	NoRepo     bool

	mutex sync.Mutex
	Calls [][]string
}

func (fake *Runner) Run(_ context.Context, _ string, argv []string) (process.Result, error) {
	fake.mutex.Lock()
	fake.Calls = append(fake.Calls, slices.Clone(argv))
	fake.mutex.Unlock()

	if !isHelp(argv) {
		return process.Result{Code: 1, Stderr: "no answer for `" + strings.Join(argv, " ") + "`"}, nil
	}

	return fake.help(argv)
}

func isHelp(argv []string) bool {
	return len(argv) >= 2 && len(argv) <= 3 && argv[0] == "sdd" && argv[len(argv)-1] == "--help"
}

func (fake *Runner) help(argv []string) (process.Result, error) {
	if fake.Missing {
		return process.Result{}, errors.New("running sdd: executable file not found in $PATH")
	}

	build := "fork"
	if fake.Stock {
		build = "stock"
	}

	page := "root"
	if len(argv) == 3 {
		page = argv[1]
	}

	text, err := fixtures.ReadFile("fixtures/sdd/" + build + "/" + page + ".txt")
	if err != nil {
		return process.Result{Code: 3, Stderr: "No help topic for '" + page + "'"}, nil
	}

	help := string(text)

	if fake.Unreleased {
		help = withoutCommand(help, "export")
	}

	if fake.NoRepo {
		help = withoutCommand(help, "repo")
	}

	return process.Result{Stdout: help}, nil
}

func withoutCommand(help string, command string) string {
	var kept []string

	for line := range strings.SplitSeq(help, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), command+" ") {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n")
}
