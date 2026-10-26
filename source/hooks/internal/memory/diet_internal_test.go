package memory

import (
	"testing"
	"time"
)

func TestShortLine(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)
	here := tree{root: t.TempDir(), branch: "main"}

	cases := []struct {
		name  string
		held  entry
		reads string
		want  string
	}{
		{name: "the reader's own entry", reads: "main",
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "main", kind: kindNote, filed: now.Add(-20 * time.Hour)},
			want: "m-h8pdp8 fdbox guard inventory  [20h]"},
		{name: "another seat's entry", reads: "main",
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "drive", kind: kindNote, filed: now.Add(-3 * time.Minute)},
			want: "m-h8pdp8 fdbox guard inventory  [3m, drive]"},
		{name: "an override that differs", reads: "main",
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "main", kind: kindNote, override: "long",
				filed: now.Add(-50 * time.Hour)},
			want: "m-h8pdp8 fdbox guard inventory  [2d, retain long]"},
		{name: "an override the kind sets anyway", reads: "main",
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "main", kind: kindNote, override: "normal",
				filed: now.Add(-15 * 24 * time.Hour)},
			want: "m-h8pdp8 fdbox guard inventory  [2w]"},
		{name: "every part with flags", reads: "main",
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "drive", kind: kindTodo, override: "scratch",
				filed: now.Add(-30 * time.Second), branch: "feature", linkFlags: []string{"→ m-aaaaaa retired"},
				until: []string{"claim"}, inbound: []*entry{{status: statusLive}, {status: statusSuperseded}}},
			want: "m-h8pdp8 fdbox guard inventory  [0m, drive, retain scratch]  other branch: feature; → m-aaaaaa retired; ← 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortLine(here, &tc.held, tc.reads, now); got != tc.want {
				t.Errorf("the short line should carry age, a foreign seat, a differing override and the flags, got %q", got)
			}
		})
	}
}
