package test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// ClaimSeat writes the claim the carry-forward keeps for a seat held by the
// session, in the checkout at root.
func ClaimSeat(t *testing.T, root string, seat string, session string) {
	t.Helper()

	data, err := json.Marshal(map[string]any{"session_id": session, "pid": DeadPID, "started": "irrelevant"})
	if err != nil {
		t.Fatalf("encoding the claim: %v", err)
	}

	WriteFile(t, filepath.Join(root, ".memory", "roles", seat+".json"), string(data))
}
