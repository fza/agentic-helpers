package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// OwnerStarted is when the fake owner process started; a claim naming it with
// OwnerPID reads as held by the live session of NewProcesses.
const OwnerStarted = "Thu Sep 25 14:00:00 2026"

// ClaimSeat writes a claim on seat in the memory store at store, held by the
// fake owner process: live under NewProcesses.
func ClaimSeat(t *testing.T, store string, seat string, session string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": OwnerPID, "started": OwnerStarted})
}

// OtherOwnerStarted is when the other fake session's claude process started.
const OtherOwnerStarted = "Fri Sep 26 09:00:00 2026"

// ClaimOtherSeat writes a claim held by the other live session of Processes.Other.
func ClaimOtherSeat(t *testing.T, store string, seat string, session string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": OtherOwnerPID, "started": OtherOwnerStarted})
}

// MemoryStore is the agentic-memory store of the project a session works on in
// base, under home.
func MemoryStore(base string, home string, lookup project.CommonDirLookup) string {
	return projectkey.Dir(seat.StoreRoot(home), projectkey.Key(context.Background(), base, lookup))
}

// ClaimDeadSeat writes a claim whose holder process is gone.
func ClaimDeadSeat(t *testing.T, store string, seat string, session string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": DeadPID, "started": "irrelevant"})
}

func writeClaim(t *testing.T, store string, seat string, claim map[string]any) {
	t.Helper()

	data, err := json.Marshal(claim)
	if err != nil {
		t.Fatalf("encoding the claim: %v", err)
	}

	WriteFile(t, filepath.Join(store, "seats", seat+".json"), string(data))
}
