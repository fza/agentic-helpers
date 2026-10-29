package memory

import (
	"cmp"
	"context"
	"slices"
)

const shippedText = "Work shipped since your last state write. Write where work stands: " + self +
	" state < body. Then end the turn without commentary."

const shippedPromptText = "Work shipped since your last state write. Write where work stands first: " + self +
	" state < body."

var fileWriters = []string{"Edit", "Write", "NotebookEdit"}

// countShipped counts a tool call's file write and the graph entries it
// added toward the shipped-work hold, and starts the window's measure at its
// first call: HEAD then, and no state write seen yet.
func (mem *memory) countShipped(ctx context.Context, session string, toolName string, path string, captured int) {
	held := mem.loadLadder(session)
	writes := slices.Contains(fileWriters, toolName)

	if !held.ShipSince.IsZero() && !writes && captured == 0 {
		return
	}

	mem.startShipping(ctx, &held)

	if writes {
		held.Writes++
		held.Wrote = wrotePaths(held.Wrote, path)
	}

	held.Captured += captured
	_ = writeJSON(mem.ladderPath(session), held)
}

func (mem *memory) startShipping(ctx context.Context, held *ladder) {
	if !held.ShipSince.IsZero() {
		return
	}

	now := mem.now()
	held.ShipSince, held.StateSeen, held.ShipBase = now, now, mem.checkout(ctx).commit
}

// shippedDue is whether the seat owes a state write: HEAD moved, the graph
// gained an entry, or file writes reached state_stale_after_writes since the
// seat's last state write, and no request went out since. The caller that
// delivers the request marks it held, since hidden messages to the agent stay
// as scarce as possible. Every Stop and prompt calls it, so a state write
// resets the counts before later work adds to them.
func (mem *memory) shippedDue(ctx context.Context, seatName string, held *ladder) bool {
	folded, err := mem.load(ctx)
	if err != nil {
		return false
	}

	mem.startShipping(ctx, held)

	commit := mem.checkout(ctx).commit

	slot, written := folded.states[seatName]
	if written && !slot.Draft && slot.at.After(held.StateSeen) {
		held.ShipBase, held.StateSeen = cmp.Or(slot.Commit, commit), slot.at
		held.Writes, held.Captured, held.Held = 0, 0, false
	}

	if held.Held {
		return false
	}

	moved := held.ShipBase != "" && commit != "" && commit != held.ShipBase

	return moved || held.Captured > 0 || held.Writes >= mem.options().StateStaleAfterWrites
}
