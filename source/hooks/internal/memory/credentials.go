package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

// credentialError refuses a write holding a credential; its text is the
// refusal as the command prints it.
type credentialError struct {
	why string
}

func (refusal credentialError) Error() string {
	return refusal.why
}

// credentialIn says why text cannot be kept, naming where it was to go, or
// nothing where the scan finds no credential. A scan that could not finish
// refuses too, naming scan-incomplete: unscanned text is never kept.
func credentialIn(ctx context.Context, where string, text string) string {
	findings := secrets.Scan(ctx, text, "")
	if len(findings) == 0 {
		return ""
	}

	rules, line := secrets.Describe(findings)

	return fmt.Sprintf("%s looks like a credential (%s, line %d); memory never holds one", where, rules, line)
}

// eventText is one text an event carries and where it goes, as a refusal
// names it.
type eventText struct {
	where string
	text  string
}

// fieldNames is where a payload key's text goes, as a refusal names it; a key
// not listed is named "the <key>".
var fieldNames = map[string]string{
	"title": "the title", "body": "the body", "because": "the reason", "topics": "a topic", "into": "a topic",
	"links": "a link", "text_links": "a link", "until": "--until", "when": "--when", "on": "--on",
	"proof": "--proof", "forbids": "--forbids", "anchors": "an anchor", "about": "an anchor", "until_hashes": "--until", "for": "--for",
}

// eventTexts is every text an event carries: its target and every string its
// payload holds, at any depth.
func eventTexts(event memorylog.Event) []eventText {
	var texts []eventText

	if event.Target != "" {
		texts = append(texts, eventText{where: "the link target", text: event.Target})
	}

	var payload map[string]any

	if json.Unmarshal(event.Payload, &payload) != nil {
		return append(texts, eventText{where: "the event", text: string(event.Payload)})
	}

	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	for _, key := range keys {
		where := cmp.Or(fieldNames[key], "the "+key)
		if key == "body" && event.Kind == memorylog.KindState {
			where = "the state"
		}

		for _, text := range stringsIn(payload[key]) {
			texts = append(texts, eventText{where: where, text: text})
		}
	}

	return texts
}

// stringsIn is every string a decoded JSON value holds, map keys included.
func stringsIn(value any) []string {
	switch held := value.(type) {
	case string:
		return []string{held}
	case []any:
		var found []string
		for _, item := range held {
			found = append(found, stringsIn(item)...)
		}

		return found
	case map[string]any:
		var found []string

		for key, item := range held {
			found = append(found, key)
			found = append(found, stringsIn(item)...)
		}

		return found
	}

	return nil
}

// credentialInEvents refuses events carrying a credential anywhere, naming the
// first text holding one.
func credentialInEvents(ctx context.Context, events []memorylog.Event) error {
	for _, event := range events {
		for _, carried := range eventTexts(event) {
			why := credentialIn(ctx, carried.where, carried.text)
			if why != "" {
				return credentialError{why: why}
			}
		}
	}

	return nil
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

// credentialScan is the migration pass reading every event once, with the
// same fields a write is scanned over, by the seat key that filed the entry
// it names; a row naming no entry, a topic merge, is told to the seat that
// made it.
type credentialScan struct {
	ctx   context.Context
	found map[string][]possibleCredential
}

func (scan *credentialScan) pass(events []memorylog.Event) error {
	type held struct {
		id, seat, title string
		rules           []string
		dropped         bool
	}

	entries := map[string]*held{}

	var order []string

	for _, event := range events {
		key := cmp.Or(event.Entry, "- "+event.Seat)

		if event.Kind == memorylog.KindNew {
			var filed newPayload

			err := json.Unmarshal(event.Payload, &filed)
			if err != nil {
				return fmt.Errorf("reading the new event of %s: %w", event.Entry, err)
			}

			entries[key] = &held{id: event.Entry, seat: event.Seat, title: filed.Title, dropped: filed.Dropped}
			order = append(order, key)
		}

		entry := entries[key]
		if entry == nil {
			entry = &held{id: "-", seat: event.Seat, title: "a topic merge"}
			entries[key] = entry
			order = append(order, key)
		}

		if event.Kind == memorylog.KindRetitle {
			var retitled retitlePayload
			if json.Unmarshal(event.Payload, &retitled) == nil {
				entry.title = retitled.Title
			}
		}

		for _, carried := range eventTexts(event) {
			findings := secrets.Scan(scan.ctx, carried.text, "")
			if len(findings) > 0 {
				rules, _ := secrets.Describe(findings)
				entry.rules = append(entry.rules, strings.Split(rules, ", ")...)
			}
		}
	}

	for _, key := range order {
		entry := entries[key]
		if len(entry.rules) == 0 || entry.dropped {
			continue
		}

		slices.Sort(entry.rules)

		if scan.found == nil {
			scan.found = map[string][]possibleCredential{}
		}

		scan.found[entry.seat] = append(scan.found[entry.seat], possibleCredential{
			ID: entry.id, Title: entry.title, Rules: strings.Join(slices.Compact(entry.rules), ", "),
		})
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
