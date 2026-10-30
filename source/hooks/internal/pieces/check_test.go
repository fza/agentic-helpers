package pieces_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheMachineLineAppearsOnlyWhereASubstitutionIsNeeded(t *testing.T) {
	const line = "agentic-helpers: this machine: timeout -> agentic-helpers bound; sed and date are BSD\n"

	cases := []struct {
		name      string
		options   string
		installed []string
		gnu       []string
		hook      bool
		want      string
	}{
		{"a machine needing substitutions", "", []string{"gtimeout", "sed", "date"}, nil, true, line},
		{"a machine with GNU tools", "", []string{"timeout", "sed", "date"}, []string{"sed", "date"}, true, ""},
		{"alternatives off", `"shell_guard_alternatives":false`, []string{"gtimeout", "sed", "date"}, nil, true, ""},
		{"shell-guard off", `"shell_guard_enabled":false`, []string{"gtimeout", "sed", "date"}, nil, true, ""},
		{"a command rather than the hook", "", []string{"gtimeout", "sed", "date"}, nil, false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured test.Captured

			env := pieces.Env{
				Settings: settings(t, tc.options), Hook: tc.hook, CacheRoot: t.TempDir(),
				Getenv: test.Getenv(test.FlavouredPath(t, tc.installed, tc.gnu)),
			}

			code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("check should exit 0, got %d: %s", code, captured.Err.String())
			}

			got := captured.Out.String()
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("the session start should name the substitutions the machine needs, got:\n%s", got)
			}

			if tc.want == "" && strings.Contains(got, "this machine:") {
				t.Errorf("no line should name substitutions here, got:\n%s", got)
			}
		})
	}
}
