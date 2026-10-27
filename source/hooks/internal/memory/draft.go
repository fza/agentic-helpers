package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

const (
	draftCommits = 20
	draftFiles   = 20
)

// draftState writes a mechanical draft of the seat's state where no state
// the agent wrote is newer than the seat's claim, so the slot is never empty:
// the commits since the claim, the files the session wrote, its last prompt.
// It prints nothing, and the agent's next state write replaces it.
func (mem *memory) draftState(ctx context.Context, seatName string, held seat.Claim, session string) {
	folded, err := mem.load(ctx)
	if err != nil {
		return
	}

	claimed, err := time.Parse(time.RFC3339Nano, held.Claimed)
	if err != nil {
		return
	}

	slot, found := folded.states[seatName]
	if found && !slot.Draft && !slot.at.Before(claimed) {
		return
	}

	here := mem.checkout(ctx)

	body := cutWords(mem.draftBody(ctx, here, held.Commit, mem.loadLadder(session)), mem.options().EntryMaxWords)
	if body == "" || (found && slot.Draft && slot.Body == body) {
		return
	}

	raw, err := json.Marshal(statePayload{Body: body, Session: session, Branch: here.branch, Commit: here.commit, Draft: true})
	if err != nil {
		return
	}

	_ = mem.appendEvents(ctx, memorylog.Event{Kind: memorylog.KindState, Time: mem.now(), Seat: seatName, Payload: raw})
}

func (mem *memory) draftBody(ctx context.Context, here tree, since string, held ladder) string {
	var lines []string

	if since != "" && here.commit != "" && since != here.commit {
		out, err := mem.env.Git.Output(ctx, here.root, "log", "--oneline", "-n", "20", since+"..HEAD")
		if err == nil {
			commits := strings.Split(strings.TrimSpace(out), "\n")
			if commits[0] != "" {
				lines = append(lines, "commits since the claim: "+strings.Join(commits[:min(len(commits), draftCommits)], "; "))
			}
		}
	}

	if len(held.Wrote) > 0 {
		names := make([]string, 0, len(held.Wrote))

		for _, path := range held.Wrote {
			relative, err := filepath.Rel(here.root, path)
			if err != nil || strings.HasPrefix(relative, "..") {
				relative = path
			}

			names = append(names, filepath.ToSlash(relative))
		}

		lines = append(lines, "files written: "+strings.Join(names, ", "))
	}

	if held.LastPrompt != "" {
		lines = append(lines, "last prompt: "+strings.Join(strings.Fields(held.LastPrompt), " "))
	}

	return strings.Join(lines, "\n")
}

// wrotePaths adds path to the files a session wrote, each once, the first
// draftFiles of them.
func wrotePaths(wrote []string, path string) []string {
	if path == "" || slices.Contains(wrote, path) || len(wrote) >= draftFiles {
		return wrote
	}

	return append(wrote, path)
}

// cutWords keeps text within limit words, as words counts them, dropping
// whole words from where it overflows on.
func cutWords(text string, limit int) string {
	if limit <= 0 || words(text) <= limit {
		return text
	}

	var kept []string

	for _, line := range strings.Split(text, "\n") {
		if words(strings.Join(append(slices.Clone(kept), line), "\n")) <= limit {
			kept = append(kept, line)

			continue
		}

		fields := strings.Fields(line)
		for len(fields) > 0 && words(strings.Join(append(slices.Clone(kept), strings.Join(fields, " ")), "\n")) > limit {
			fields = fields[:len(fields)-1]
		}

		if len(fields) > 0 {
			kept = append(kept, strings.Join(fields, " "))
		}

		break
	}

	return strings.Join(kept, "\n")
}
