package pieces_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func settings(t *testing.T, options string) []string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "settings.json")
	test.WriteFile(t, path, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+options+`}}}}`)

	return []string{path}
}

func TestEachTieBreaksOnlyWhileItsPieceIsOff(t *testing.T) {
	cases := []struct {
		off  string
		ties []pieces.Tie
	}{
		{`"scratch_enabled":false`, []pieces.Tie{pieces.MktempBan}},
		{`"grounding_gate_enabled":false`, []pieces.Tie{pieces.CaptureGrounded}},
		{`"memory_enabled":false`, []pieces.Tie{pieces.ExemptSeats, pieces.ToolsSeatName}},
		{`"memory_enabled":false,"tools_enabled":false`, []pieces.Tie{pieces.ExemptSeats}},
		{`"capture_enabled":false`, nil},
	}

	for _, tc := range cases {
		t.Run(tc.off, func(t *testing.T) {
			options, err := pluginconfig.Read(settings(t, tc.off))
			if err != nil {
				t.Fatal(err)
			}

			broken := pieces.Broken(options)
			if len(broken) != len(tc.ties) {
				t.Fatalf("exactly the ties resting on the off piece, of pieces still on, should break, got %d", len(broken))
			}

			for index, tie := range tc.ties {
				if broken[index].Feature != tie.Feature || tie.Holds(options) {
					t.Errorf("the tie %q should be broken", tie.Feature)
				}
			}
		})
	}

	if broken := pieces.Broken(pluginconfig.Defaults()); len(broken) != 0 {
		t.Errorf("with every piece on, no tie should break, got %d", len(broken))
	}
}

func TestTheLinesAPieceAndATieSpeak(t *testing.T) {
	if got := pieces.MktempBan.Line(); got != "agentic-helpers: scratch is off, so shell-guard's mktemp ban is off; turn scratch_enabled on to restore it" {
		t.Errorf("a broken tie should name the piece off, the feature and the option, got %q", got)
	}

	if got := pieces.Memory.Off(); got != "agentic-memory: off on this machine (plugin option memory_enabled)" {
		t.Errorf("an off piece's command should name its option, got %q", got)
	}

	if got := pieces.Tools.Instructions(); got != "agentic-tools is off on this machine; plugin option tools_enabled turns it on" {
		t.Errorf("an off piece's MCP server should say which option turns it on, got %q", got)
	}

	if got := pieces.CaptureGrounded.Off(); got != "agentic-capture: off while grounding gate is off (plugin option grounding_gate_enabled)" {
		t.Errorf("a command whose work rests on an off piece should say so, got %q", got)
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		options string
		hook    bool
		want    []string
		absent  []string
	}{
		{"a session start with every tie whole says nothing", "", true, nil, []string{"agentic-helpers"}},
		{"a session start names each broken tie", `"scratch_enabled":false,"memory_enabled":false`, true,
			[]string{pieces.MktempBan.Line(), pieces.ExemptSeats.Line(), pieces.ToolsSeatName.Line()}, []string{"memory  "}},
		{"a command lists every piece and what is off under it", `"scratch_enabled":false`, false,
			[]string{"memory          on\n", "scratch         off  (plugin option scratch_enabled)\n",
				"shell-guard     on\n  off: shell-guard's mktemp ban, as scratch is off; turn scratch_enabled on to restore it\n"}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured test.Captured

			code := pieces.Main(context.Background(), []string{"check"},
				pieces.Env{Settings: settings(t, tc.options), Hook: tc.hook, Getenv: test.Getenv(test.Stubs(t, "timeout"))},
				captured.Streams(""))
			out := captured.Out.String()

			if code != 0 {
				t.Fatalf("check should exit 0, got %d: %s", code, captured.Err.String())
			}

			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("check should print %q, got:\n%s", want, out)
				}
			}

			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("check should not print %q, got:\n%s", absent, out)
				}
			}
		})
	}
}
