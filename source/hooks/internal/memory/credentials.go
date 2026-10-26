package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

// credentialIn says why text cannot be kept, naming where it was to go, or
// nothing where the scan finds no credential. A scan that could not finish
// refuses too: unscanned text is never kept. It runs before anything is
// written or sent to the embedding endpoint.
func credentialIn(ctx context.Context, where string, text string) string {
	findings := secrets.Scan(ctx, text, "")
	if len(findings) == 0 {
		return ""
	}

	rules, line := secrets.Describe(findings)

	return fmt.Sprintf("%s looks like a credential (%s, line %d); memory never holds one", where, rules, line)
}

// firstCredential is the refusal for the first of the named texts holding a
// credential, or nothing.
func firstCredential(ctx context.Context, texts ...[2]string) string {
	for _, named := range texts {
		if strings.TrimSpace(named[1]) == "" {
			continue
		}

		why := credentialIn(ctx, named[0], named[1])
		if why != "" {
			return why
		}
	}

	return ""
}

// possibleCredential is an entry the migration scan found a credential in:
// told once to the seat that filed it, in its next brief.
type possibleCredential struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rules string `json:"rules"`
}

const credentialsFound = "credentials-found.json"

func (mem *memory) credentialsPath() string {
	return filepath.Join(mem.store(), credentialsFound)
}

// credentialScan is the migration pass reading every entry still holding its
// body once, by the seat key that filed it.
type credentialScan struct {
	ctx   context.Context
	found map[string][]possibleCredential
}

func (scan *credentialScan) pass(events []memorylog.Event) error {
	type held struct {
		seat, title, text string
	}

	entries := map[string]*held{}

	var order []string

	for _, event := range events {
		switch event.Kind {
		case memorylog.KindNew:
			var filed newPayload

			err := json.Unmarshal(event.Payload, &filed)
			if err != nil {
				return fmt.Errorf("reading the new event of %s: %w", event.Entry, err)
			}

			if filed.Dropped || filed.Body == "" {
				continue
			}

			entries[event.Entry] = &held{seat: event.Seat, title: filed.Title, text: filed.Title + "\n" + filed.Body}
			order = append(order, event.Entry)
		case memorylog.KindRetitle:
			var retitled retitlePayload

			err := json.Unmarshal(event.Payload, &retitled)
			if err == nil && entries[event.Entry] != nil {
				entries[event.Entry].title = retitled.Title
				entries[event.Entry].text += "\n" + retitled.Title
			}
		}
	}

	for _, id := range order {
		entry := entries[id]

		findings := secrets.Scan(scan.ctx, entry.text, "")
		if len(findings) == 0 {
			continue
		}

		rules, _ := secrets.Describe(findings)

		if scan.found == nil {
			scan.found = map[string][]possibleCredential{}
		}

		scan.found[entry.seat] = append(scan.found[entry.seat], possibleCredential{ID: id, Title: entry.title, Rules: rules})
	}

	return nil
}

// save keeps what the scan found in the store, beside the log, until each
// owning seat's next brief tells it.
func (scan *credentialScan) save(store string) error {
	if len(scan.found) == 0 {
		return nil
	}

	return writeJSON(filepath.Join(store, credentialsFound), scan.found)
}

// credentialLines tells the seat, once, which of its entries the migration
// scan found a credential in.
func (mem *memory) credentialLines(ctx context.Context, seatName string) []string {
	var found map[string][]possibleCredential
	if !readJSON(mem.credentialsPath(), &found) {
		return nil
	}

	key := mem.seatKey(seatName)
	if len(found[key]) == 0 {
		return nil
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return nil
	}
	defer unlock()

	found = nil
	if !readJSON(mem.credentialsPath(), &found) || len(found[key]) == 0 {
		return nil
	}

	lines := make([]string, 0, len(found[key]))
	for _, held := range found[key] {
		lines = append(lines, fmt.Sprintf("possible credential: %s %s (%s); supersede it --because 'credential'",
			held.ID, held.Title, held.Rules))
	}

	delete(found, key)

	if len(found) == 0 {
		removeIfThere(mem.credentialsPath())
	} else {
		_ = writeJSON(mem.credentialsPath(), found)
	}

	return lines
}
