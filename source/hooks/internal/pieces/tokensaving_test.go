package pieces_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSessionStartNamesSddsOwnRepeatFilter(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		options string
		graph   bool
		says    bool
		asks    bool
	}{
		{name: "filter on in a graph project", value: "on", graph: true, says: true, asks: true},
		{name: "filter off", value: "off", graph: true, asks: true},
		{name: "an sdd without the key", value: "", graph: true, asks: true},
		{name: "no graph", value: "on"},
		{name: "gate off", value: "on", graph: true, options: `"grounding_gate_enabled":false`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := layeredEnv(t, "", tc.options)
			env.Hook = true
			env.Dir = t.TempDir()

			if tc.graph {
				err := os.Mkdir(filepath.Join(env.Dir, ".sdd"), 0o755)
				if err != nil {
					t.Fatalf("creating the graph: %v", err)
				}
			}

			sdd := &test.SddValues{Values: map[string]string{}}
			if tc.value != "" {
				sdd.Values["sessions.token_saving"] = tc.value
			}

			env.SddValues = sdd

			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"check"}, env, captured.Streams(""))
			if code != 0 {
				t.Fatalf("check should exit 0, got %d", code)
			}

			said := strings.Contains(captured.Out.String(), pieces.TokenSavingLine)
			if said != tc.says {
				t.Errorf("the line should appear only while sdd's own filter is on in a gated graph project, got %q",
					captured.Out.String())
			}

			if (sdd.Asked() > 0) != tc.asks {
				t.Errorf("sdd should be asked only in a graph project the gate works on, asked %d times", sdd.Asked())
			}
		})
	}
}
