package memory

import (
	"encoding/json"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

func payloadOf(t *testing.T, tool string, input map[string]string) hookio.Payload {
	t.Helper()

	raw, err := json.Marshal(map[string]any{"tool_name": tool, "tool_input": input})
	if err != nil {
		t.Fatal(err)
	}

	var payload hookio.Payload

	err = json.Unmarshal(raw, &payload)
	if err != nil {
		t.Fatal(err)
	}

	return payload
}

func TestATrapIsTouchedBy(t *testing.T) {
	root := "/work/app"

	cases := []struct {
		name  string
		trap  indexedTrap
		tool  string
		input map[string]string
		want  string
	}{
		{name: "an anchor named by file_path", trap: indexedTrap{Anchors: []string{"internal/gate/gate.go"}},
			tool: "Edit", input: map[string]string{"file_path": "/work/app/internal/gate/gate.go"}, want: "internal/gate/gate.go"},
		{name: "an anchor named by notebook_path", trap: indexedTrap{Anchors: []string{"notes/run.ipynb"}},
			tool: "NotebookEdit", input: map[string]string{"notebook_path": "/work/app/notes/run.ipynb"}, want: "notes/run.ipynb"},
		{name: "an anchor named by path", trap: indexedTrap{Anchors: []string{"plan/parts/jobs.md"}},
			tool: "Grep", input: map[string]string{"path": "plan/parts/jobs.md"}, want: "plan/parts/jobs.md"},
		{name: "an anchor named in a Bash command", trap: indexedTrap{Anchors: []string{"internal/gate/gate.go"}},
			tool: "Bash", input: map[string]string{"command": "sed -n 1,20p internal/gate/gate.go"}, want: "internal/gate/gate.go"},
		{name: "an anchor not named", trap: indexedTrap{Anchors: []string{"internal/gate/gate.go"}},
			tool: "Read", input: map[string]string{"file_path": "/work/app/README.md"}},
		{name: "a glob crossing directories", trap: indexedTrap{When: []string{"path:.autopilot/plan/parts/jobs/**"}},
			tool: "Write", input: map[string]string{"file_path": "/work/app/.autopilot/plan/parts/jobs/a/b.md"},
			want: ".autopilot/plan/parts/jobs/a/b.md"},
		{name: "a glob a path misses", trap: indexedTrap{When: []string{"path:.autopilot/plan/parts/jobs/**"}},
			tool: "Write", input: map[string]string{"file_path": "/work/app/.autopilot/plan/other.md"}},
		{name: "a glob over a Bash path token", trap: indexedTrap{When: []string{"path:**/*.seg"}},
			tool: "Bash", input: map[string]string{"command": "xxd log/abc.seg | head"}, want: "log/abc.seg"},
		{name: "a path outside the work tree", trap: indexedTrap{When: []string{"path:**"}},
			tool: "Read", input: map[string]string{"file_path": "/etc/hosts"}},
		{name: "a command pattern", trap: indexedTrap{When: []string{"command:pilot board upgrade"}},
			tool: "Bash", input: map[string]string{"command": "cd x && pilot board upgrade --all"}, want: "pilot board upgrade"},
		{name: "a command pattern is case-sensitive", trap: indexedTrap{When: []string{"command:pilot board upgrade"}},
			tool: "Bash", input: map[string]string{"command": "Pilot Board Upgrade"}},
		{name: "a command pattern on another tool's command", trap: indexedTrap{When: []string{"command:upgrade"}},
			tool: "Monitor", input: map[string]string{"command": "pilot board upgrade"}},
		{name: "an anchor in another tool's command", trap: indexedTrap{Anchors: []string{"internal/gate/gate.go"}},
			tool: "Monitor", input: map[string]string{"command": "tail -f internal/gate/gate.go"}},
		{name: "a tool name", trap: indexedTrap{When: []string{"tool:Agent"}},
			tool: "Agent", input: map[string]string{"prompt": "build it"}, want: "Agent"},
		{name: "another tool name", trap: indexedTrap{When: []string{"tool:Agent"}},
			tool: "Task", input: map[string]string{"prompt": "build it"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.trap.touchedBy(callOf(root, payloadOf(t, tc.tool, tc.input)))
			if got != tc.want {
				t.Errorf("a trap should be touched exactly by a call naming what it is about, got %q", got)
			}
		})
	}
}
