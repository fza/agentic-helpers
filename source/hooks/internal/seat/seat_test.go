package seat_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestOfSession(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "reviewer", test.Session)
	test.ClaimSeat(t, store, "drive", test.OtherSession)
	test.WriteFile(t, filepath.Join(store, "seats", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(store, "seats", "released.json"), `{"session_id": ""}`)

	dir := seat.Dir(store)

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
			got, _ := seat.OfSession(dir, tc.session)
			if got != tc.want {
				t.Error("the session should read as the seat its claim names, and as none without one")
			}
		})
	}
}

func TestHeld(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "reviewer", test.Session)
	test.WriteFile(t, filepath.Join(store, "seats", "broken.json"), "{not json")
	test.WriteFile(t, filepath.Join(store, "seats", "released.json"), `{"session_id": ""}`)
	test.WriteFile(t, filepath.Join(store, "seats", "Bad Name.json"), `{"session_id": "x"}`)

	held := seat.Held(seat.Dir(store))

	if len(held) != 1 || held["reviewer"].SessionID != test.Session {
		t.Errorf("only a well-named claim naming a session should count as held, got: %v", held)
	}

	if len(seat.Held(filepath.Join(store, "missing"))) != 0 {
		t.Error("a store without claims should hold no seat")
	}
}

func TestOf(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name  string
		claim func(t *testing.T, store string)
		want  string
	}{
		{
			name:  "the owning process's live claim is its seat",
			claim: func(t *testing.T, store string) { test.ClaimSeat(t, store, "main", test.Session) },
			want:  "main",
		},
		{
			name:  "a claim whose holder is gone is no seat",
			claim: func(t *testing.T, store string) { test.ClaimDeadSeat(t, store, "main", test.Session) },
		},
		{
			name: "a claim naming the owner pid with another start time is a reused pid",
			claim: func(t *testing.T, store string) {
				test.WriteFile(t, filepath.Join(store, "seats", "main.json"),
					`{"session_id":"x","pid":200,"started":"Mon Jan  1 00:00:00 2024"}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := t.TempDir()
			tc.claim(t, store)

			got, _ := seat.Of(ctx, seat.Dir(store), test.NewProcesses(), "claude")
			if got != tc.want {
				t.Error("a process should hold exactly the seat its owner's live claim names")
			}
		})
	}
}

func TestOwnerWalksPastTheShell(t *testing.T) {
	if seat.Owner(context.Background(), test.NewProcesses(), "claude") != test.OwnerPID {
		t.Error("the owner should be the nearest claude ancestor, past the shell")
	}

	if seat.Owner(context.Background(), test.NewProcesses(), "nothing") != test.ShellPID {
		t.Error("without an owner ancestor the immediate parent should stand in")
	}
}

func TestRemoveKeepsTheLastClaim(t *testing.T) {
	store := t.TempDir()
	test.ClaimSeat(t, store, "main", test.Session)
	dir := seat.Dir(store)

	err := seat.Remove(dir, "main")
	if err != nil {
		t.Fatalf("removing a claim should succeed: %v", err)
	}

	_, held := seat.Read(dir, "main")
	if held {
		t.Error("a removed claim should leave the seat free")
	}

	last, found := seat.ReadLast(dir, "main")
	if !found || last.SessionID != test.Session {
		t.Error("a removed claim should survive as the seat's last claim")
	}

	if len(seat.Known(dir)) != 1 {
		t.Error("a seat with only a last claim should still be known")
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"main": true, "fdbox-main": true, "a": true, "Main": false, "1st": false, "": false,
		"abcdefghijklmnopqrstuvwxy": false, "abcdefghijklmnopqrstuvwx": true,
	} {
		if seat.ValidName(name) != want {
			t.Errorf("seat names should match ^[a-z][a-z0-9-]{0,23}$, judged %q wrong", name)
		}
	}
}

func TestANameOutsideTheSeatsDirectoryIsRefused(t *testing.T) {
	store := t.TempDir()
	dir := seat.Dir(store)
	test.WriteFile(t, filepath.Join(store, "outside.json"), `{"session_id":"x"}`)

	_, found := seat.Read(dir, "../outside")
	if found {
		t.Error("a name escaping the seats directory should read as no claim")
	}

	if seat.Write(dir, "../outside", seat.Claim{SessionID: "y"}) == nil {
		t.Error("a name escaping the seats directory should not be written")
	}

	if seat.Remove(dir, "../outside") == nil || !test.Exists(t, filepath.Join(store, "outside.json")) {
		t.Error("a name escaping the seats directory should not remove anything")
	}
}
