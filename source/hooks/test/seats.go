package test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// OwnerStart is when the fake owner process started, and OwnerStarted how a
// claim records it: a claim naming it with OwnerPID reads as held by the live
// session of NewProcesses.
var OwnerStart = time.Date(2026, time.September, 25, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))

const OwnerStarted = "2026-09-25T12:00:00Z"

// ClaimSeat writes a claim on seat in the memory store at store, held by the
// fake owner process: live under NewProcesses.
func ClaimSeat(t *testing.T, store string, seat string, session string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": OwnerPID, "started": OwnerStarted})
}

// ClaimSeatStarted writes a claim held by the fake owner process with started
// as an earlier release may have written it.
func ClaimSeatStarted(t *testing.T, store string, seat string, session string, started string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": OwnerPID, "started": started})
}

// OtherOwnerStart is when the other fake session's claude process started.
var OtherOwnerStart = time.Date(2026, time.September, 26, 9, 0, 0, 0, time.FixedZone("CEST", 2*60*60))

const OtherOwnerStarted = "2026-09-26T07:00:00Z"

// ClaimOtherSeat writes a claim held by the other live session of Processes.Other.
func ClaimOtherSeat(t *testing.T, store string, seat string, session string) {
	t.Helper()

	writeClaim(t, store, seat, map[string]any{"session_id": session, "pid": OtherOwnerPID, "started": OtherOwnerStarted})
}

// MemoryStore is the agentic-memory store of the project a session works on in
// base, under home.
func MemoryStore(base string, home string, lookup project.CommonDirLookup) string {
	key, err := projectkey.Key(context.Background(), base, lookup)
	if err != nil {
		key = projectkey.DirectoryKey(base)
	}

	return projectkey.Dir(seat.StoreRoot(home), key)
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
