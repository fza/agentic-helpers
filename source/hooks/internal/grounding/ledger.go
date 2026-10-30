package grounding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

var errNoLedger = errors.New("no ledger for this session")

// evidence is what one session read out of the graph while the graph stood as
// Graph says, plus the drafts saved while it held every read, and the missing
// reads its last let-through note named. The file may carry fields another
// tool wrote, and they survive a record.
type evidence struct {
	Search   int             `json:"search"`
	Query    int             `json:"query"`
	Term     int             `json:"term"`
	Listings []string        `json:"listings"`
	Grounded []groundedDraft `json:"grounded"`
	Graph    string          `json:"graph"`
	Noted    string          `json:"noted"`
	// Captures holds each running capture through sdd's engine by its
	// instance handle. Neither a graph change nor a compaction clears it: it
	// is the body and topics the engine holds, not a read.
	Captures map[string]engineDraft `json:"captures"`
	// Staged holds the text of each file staged through sdd's engine by its
	// handle, where the project lints captures: the file a `bodyHandle`
	// names. Like Captures it mirrors the engine's state, not a read.
	Staged map[string]stagedFile `json:"staged"`

	rest map[string]json.RawMessage
}

// groundedDraft is a draft's content hash and the graph's state when a fully
// read turn saved it. Either moving voids it.
type groundedDraft struct {
	Draft string `json:"draft"`
	Graph string `json:"graph"`
}

func freshEvidence() evidence {
	return evidence{Listings: []string{}, Grounded: []groundedDraft{}, Captures: map[string]engineDraft{}, Staged: map[string]stagedFile{}}
}

// against holds the reads to the graph as it stands now: reads recorded while
// it stood otherwise no longer count. Grounded drafts carry their own graph
// state and stay.
func (held *evidence) against(graph string) {
	if held.Graph == graph {
		return
	}

	held.Search, held.Query, held.Term, held.Listings, held.Graph, held.Noted = 0, 0, 0, []string{}, graph, ""
}

// forget drops the reads and keeps the grounded drafts, as a compaction drops
// the reads' output from the context. The note went with that context too.
func (held *evidence) forget() {
	held.Search, held.Query, held.Term, held.Listings, held.Noted = 0, 0, 0, []string{}, ""
}

// missing names the reads still owed, the cause a let-through note answers.
func (held evidence) missing() string {
	var owed []string

	if held.Query == 0 {
		owed = append(owed, "query")
	}

	if held.Term == 0 {
		owed = append(owed, "term")
	}

	if len(held.Listings) == 0 {
		owed = append(owed, "listing")
	}

	return strings.Join(owed, ",")
}

func (held evidence) complete() bool {
	return held.Query > 0 && held.Term > 0 && len(held.Listings) > 0
}

func (held evidence) grounds(draft groundedDraft) bool {
	return slices.Contains(held.Grounded, draft)
}

func (held *evidence) addGrounded(draft groundedDraft) {
	if !held.grounds(draft) {
		held.Grounded = append(held.Grounded, draft)
	}
}

func (held *evidence) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage

	err := json.Unmarshal(data, &fields)
	if err != nil {
		return fmt.Errorf("reading the ledger: %w", err)
	}

	type known evidence

	var read known

	err = json.Unmarshal(data, &read)
	if err != nil {
		return fmt.Errorf("reading the ledger: %w", err)
	}

	for _, name := range []string{"search", "query", "term", "listings", "grounded", "graph", "noted", "captures", "staged"} {
		delete(fields, name)
	}

	*held = evidence(read)
	held.rest = fields

	if held.Listings == nil {
		held.Listings = []string{}
	}

	if held.Grounded == nil {
		held.Grounded = []groundedDraft{}
	}

	if held.Captures == nil {
		held.Captures = map[string]engineDraft{}
	}

	if held.Staged == nil {
		held.Staged = map[string]stagedFile{}
	}

	return nil
}

func (held evidence) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	for name, value := range held.rest {
		fields[name] = value
	}

	fields["search"] = held.Search
	fields["query"] = held.Query
	fields["term"] = held.Term
	fields["listings"] = held.Listings
	fields["grounded"] = held.Grounded
	fields["graph"] = held.Graph
	fields["noted"] = held.Noted
	fields["captures"] = held.Captures
	fields["staged"] = held.Staged

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("writing the ledger: %w", err)
	}

	return data, nil
}

func (held *evidence) addListing(listing string) {
	if !slices.Contains(held.Listings, listing) {
		held.Listings = append(held.Listings, listing)
	}
}

// ledgerPath is where this session's evidence lives. A spawned agent shares
// its parent's session, so its evidence is a file of its own: no prompt ever
// starts a turn for it, and the parent's reads are not its.
func ledgerPath(root string, dir string, payload hookio.Payload) (string, error) {
	session := payload.SessionID
	if session == "" {
		session = "unknown"
	}

	session = strings.ReplaceAll(session, "/", "-")

	if payload.AgentID != "" {
		session += "." + strings.ReplaceAll(payload.AgentID, "/", "-")
	}

	err := safefs.MkdirAll(root, dir, 0o755)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNoLedger, err)
	}

	return filepath.Join(dir, session+".json"), nil
}

// loadEvidence reads a ledger. An unreadable one holds no evidence, so the
// gate refuses rather than passing on grounding nobody can show.
func loadEvidence(ctx context.Context, path string) evidence {
	if ctx.Err() != nil {
		return freshEvidence()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return freshEvidence()
	}

	var held evidence

	err = json.Unmarshal(data, &held)
	if err != nil {
		return freshEvidence()
	}

	return held
}

func saveEvidence(ctx context.Context, root string, path string, held evidence) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	err = atomicfile.Write(root, path, data, 0o644)
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	return nil
}

// end removes the session's ledger and those of the agents it spawned. Only
// the session's own name, or that name followed by an agent's, qualifies.
func end(ctx context.Context, dir string, payload hookio.Payload) {
	session := strings.ReplaceAll(payload.SessionID, "/", "-")
	if session == "" {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}

		name := entry.Name()
		if entry.IsDir() || !ledgerName.MatchString(name) {
			continue
		}

		if name == session+".json" || strings.HasPrefix(name, session+".") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// reap removes the ledgers a session left when it ended without `SessionEnd`
// firing, once they are a week untouched.
func reap(ctx context.Context, dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}

		if entry.IsDir() || !ledgerName.MatchString(entry.Name()) {
			continue
		}

		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= orphanAge {
			continue
		}

		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}
