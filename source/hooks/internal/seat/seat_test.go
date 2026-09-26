package seat_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestOfSession(t *testing.T) {
	root := t.TempDir()
	test.ClaimSeat(t, root, "reviewer", test.Session)
	test.ClaimSeat(t, root, "drive", test.OtherSession)
	test.WriteFile(t, filepath.Join(root, ".memory", "roles", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(root, ".memory", "roles", "released.json"), `{"session_id": ""}`)

	dir := seat.Dir(root)

	cases := []struct {
		name    string
		session string
		want    string
	}{
		{name: "holder", session: test.Session, want: "reviewer"},
		{name: "another holder", session: test.OtherSession, want: "drive"},
		{name: "no claim", session: "c0ffee00-0000-4000-8000-000000000000"},
		{name: "no session"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if seat.OfSession(dir, tc.session) != tc.want {
				t.Error("the session should read as the seat its claim names, and as none without one")
			}
		})
	}
}

func TestHeld(t *testing.T) {
	root := t.TempDir()
	test.ClaimSeat(t, root, "reviewer", test.Session)
	test.WriteFile(t, filepath.Join(root, ".memory", "roles", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(root, ".memory", "roles", "released.json"), `{"session_id": ""}`)

	held := seat.Held(seat.Dir(root))

	if len(held) != 1 || held["reviewer"].SessionID != test.Session {
		t.Errorf("only a claim naming a session should count as held, got: %v", held)
	}

	if len(seat.Held(filepath.Join(root, "missing"))) != 0 {
		t.Error("a checkout without claims should hold no seat")
	}
}
