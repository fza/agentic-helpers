package test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
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

// MemoryStore is the memory folder of the store the checkout holding base
// resolves to in roots, the store created where none matches, as a session's
// first write there would.
func MemoryStore(t *testing.T, roots home.Roots, base string, lookup project.CommonDirLookup) string {
	t.Helper()

	return roots.Store(StoreOf(t, roots, base, lookup, nil), home.Memory)
}

// StoreOf is the id of the store the checkout holding base resolves to in
// roots, created where none matches; git answers its remotes, nil none.
func StoreOf(t *testing.T, roots home.Roots, base string, lookup project.CommonDirLookup, git home.Git) string {
	t.Helper()

	checkout, err := home.Inspect(context.Background(), base, lookup, git)
	if err != nil {
		checkout = home.InspectDirectory(base)
	}

	resolution, err := home.Resolver{Roots: roots}.Ensure(context.Background(), checkout)
	if err != nil || resolution.Store == "" {
		t.Fatalf("resolving the store of %s: %v %+v", base, err, resolution.Question)
	}

	return resolution.Store
}

// Roots is a test's agent-neutral home below dir.
func Roots(dir string) home.Roots {
	return home.Roots{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache", "agentic-helpers")}
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

// ClaimSeatByID writes a store of the current format: a registry naming the
// seat id, and the claim filed under that id, held by the fake owner process.
func ClaimSeatByID(t *testing.T, store string, id string, name string, session string) {
	t.Helper()

	WriteFile(t, filepath.Join(store, "seats.json"), fmt.Sprintf(`{%q: {"name": %q}}`, id, name))
	WriteFile(t, filepath.Join(store, "format.json"), `{"format": 2}`)
	writeClaim(t, store, id, map[string]any{"session_id": session, "pid": OwnerPID, "started": OwnerStarted})
}

// OtherStore is the id of another project's store on the machine: a checkout
// of its own declaring repoID, bound to a store of its own.
func OtherStore(t *testing.T, roots home.Roots, repoID string) string {
	t.Helper()

	checkout := filepath.Join(t.TempDir(), "checkout")
	WriteFile(t, filepath.Join(checkout, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	WriteFile(t, filepath.Join(checkout, ".git", "HEAD"), "ref: refs/heads/main\n")

	return StoreOf(t, roots, checkout, GitDirOf{}, nil)
}
