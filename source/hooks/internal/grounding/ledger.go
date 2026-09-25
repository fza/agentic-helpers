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

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

var errNoLedger = errors.New("no ledger for this session")

// evidence is what one turn of one session read out of the graph. The file
// may carry fields another tool wrote, and they survive a record.
type evidence struct {
	Search   int      `json:"search"`
	Query    int      `json:"query"`
	Term     int      `json:"term"`
	Listings []string `json:"listings"`

	rest map[string]json.RawMessage
}

func freshEvidence() evidence {
	return evidence{Listings: []string{}}
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

	for _, name := range []string{"search", "query", "term", "listings"} {
		delete(fields, name)
	}

	*held = evidence(read)
	held.rest = fields

	if held.Listings == nil {
		held.Listings = []string{}
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
func ledgerPath(dir string, payload hookio.Payload) (string, error) {
	session := payload.SessionID
	if session == "" {
		session = "unknown"
	}

	session = strings.ReplaceAll(session, "/", "-")

	if payload.AgentID != "" {
		session += "." + strings.ReplaceAll(payload.AgentID, "/", "-")
	}

	err := os.MkdirAll(dir, 0o755)
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

func saveEvidence(ctx context.Context, path string, held evidence) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	data, err := json.Marshal(held)
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	err = os.WriteFile(path, data, 0o644)
	if err != nil {
		return fmt.Errorf("saving the ledger: %w", err)
	}

	return nil
}
