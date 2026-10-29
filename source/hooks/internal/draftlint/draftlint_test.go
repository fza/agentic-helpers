package draftlint_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/draftlint"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestRunDecidesOnTheLintsAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer process.Result
		alerts []string
		silent bool
	}{
		{name: "a clean run passes", answer: process.Result{}},
		{
			name:   "each printed line is a finding",
			answer: process.Result{Code: 1, Stdout: "body.md:3:1:Vale.Terms:Use 'sdd'\n\nbody.md:5:2:Vale.Spelling:'grnd'\n"},
			alerts: []string{"body.md:3:1:Vale.Terms:Use 'sdd'", "body.md:5:2:Vale.Spelling:'grnd'"},
		},
		{name: "a failure printing nothing never read the body", answer: process.Result{Code: 2, Stderr: "E100 no config"}, silent: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &test.Runner{Lint: tc.answer}

			alerts, err := draftlint.Run(context.Background(), runner, "/repo", "vale --output=line", "/repo/body.md")
			if tc.silent {
				if !errors.Is(err, draftlint.ErrSilent) {
					t.Fatalf("a silent failure should be an error, got %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("the lint should have run, got %v", err)
			}

			if !slices.Equal(alerts, tc.alerts) {
				t.Errorf("the findings should be the printed lines, got %q", alerts)
			}

			call := runner.Calls[0]
			if call[len(call)-1] != "/repo/body.md" || runner.Dirs[0] != "/repo" {
				t.Errorf("the lint should run from the directory on the file, got %q in %s", call, runner.Dirs[0])
			}
		})
	}
}
