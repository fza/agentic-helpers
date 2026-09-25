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
	Search int      `json:"search"`
	Query  int      `json:"query"`
	Term   int      `json:"term"`
	Areas  []string `json:"areas"`

	rest map[string]json.RawMessage
}

func freshEvidence() evidence {
	return evidence{Areas: []string{}}
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

	for _, name := range []string{"search", "query", "term", "areas"} {
		delete(fields, name)
	}

	*held = evidence(read)
	held.rest = fields

	if held.Areas == nil {
		held.Areas = []string{}
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
	fields["areas"] = held.Areas

	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("writing the ledger: %w", err)
	}

	return data, nil
}

func (held *evidence) addArea(area string) {
	if !slices.Contains(held.Areas, area) {
		held.Areas = append(held.Areas, area)
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

// verifiedBefore reports whether a verification already read this draft
// against a grounded subject. An edit answering its findings reopens no
// question the reads of a turn would settle.
func verifiedBefore(ctx context.Context, dir string, target string) bool {
	wanted := realPath(target)

	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return false
	}

	for _, name := range names {
		if ctx.Err() != nil {
			return false
		}

		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}

		var held struct {
			Drafts map[string]struct {
				History []json.RawMessage `json:"history"`
			} `json:"drafts"`
		}

		err = json.Unmarshal(data, &held)
		if err != nil {
			continue
		}

		for path, record := range held.Drafts {
			if realPath(path) == wanted && len(record.History) > 0 {
				return true
			}
		}
	}

	return false
}

// realPath resolves what exists of a path, as far as it exists.
func realPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved
	}

	parent, name := filepath.Split(absolute)
	if parent == absolute || parent == "" || filepath.Clean(parent) == absolute {
		return absolute
	}

	return filepath.Join(realPath(filepath.Clean(parent)), name)
}
