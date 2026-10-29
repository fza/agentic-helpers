package memory

import (
	"slices"
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
		extra []string
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
			want: "m-h8pdp8 fdbox guard inventory  [0m, drive, retain scratch]  other branch: feature; → 1 ended; ← 1"},
		{name: "an extra flag after the rest", reads: "main", extra: []string{"retire? every link ended"},
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "main", kind: kindNote, filed: now.Add(-20 * time.Hour),
				linkFlags: []string{"→ m-aaaaaa retired"}},
			want: "m-h8pdp8 fdbox guard inventory  [20h]  → 1 ended; retire? every link ended"},
		{name: "an extra flag alone", reads: "main", extra: []string{"retire? every anchor gone"},
			held: entry{id: "m-h8pdp8", title: "fdbox guard inventory", seat: "main", kind: kindNote, filed: now.Add(-20 * time.Hour)},
			want: "m-h8pdp8 fdbox guard inventory  [20h]  retire? every anchor gone"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortLine(here, &tc.held, tc.reads, now, tc.extra...); got != tc.want {
				t.Errorf("the short line should carry age, a foreign seat, a differing override and the flags, got %q", got)
			}
		})
	}
}

func TestFoldedLinks(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  []string
	}{
		{name: "no ended link", want: nil},
		{name: "a settled target stays listed", flags: []string{"→ 20261024-110000-d-tac-set settled"},
			want: []string{"→ 20261024-110000-d-tac-set settled"}},
		{name: "closed, retired and gone fold into one count",
			flags: []string{"→ 20261025-162357-d-tac-427 closed by 20261026-101500-s-tac-a1b", "→ m-aaaaaa retired",
				"→ 20261020-100000-d-tac-gon gone"},
			want: []string{"→ 3 ended"}},
		{name: "a superseded target keeps its successor",
			flags: []string{"→ m-aaaaaa superseded by m-bbbbbb", "→ m-cccccc superseded by m-bbbbbb",
				"→ 20261024-120000-d-tac-sup superseded by 20261027-090000-d-tac-new"},
			want: []string{"→ 2 superseded by m-bbbbbb, 1 superseded by 20261027-090000-d-tac-new"}},
		{name: "a supersede naming no successor counts as ended", flags: []string{"→ m-aaaaaa superseded"},
			want: []string{"→ 1 ended"}},
		{name: "settled listed, the rest folded after it",
			flags: []string{"→ m-aaaaaa retired", "→ 20261024-110000-d-tac-set settled", "→ m-cccccc superseded by m-bbbbbb",
				"→ fdbox:20261024-120000-d-tac-k2m closed by 20261025-090000-s-tac-c3d"},
			want: []string{"→ 20261024-110000-d-tac-set settled", "→ 2 ended, 1 superseded by m-bbbbbb"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldedLinks(tc.flags); !slices.Equal(got, tc.want) {
				t.Errorf("settled links should stay listed and ended ones fold by how they ended, got %q", got)
			}
		})
	}
}
