package memory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// ErrExportFormat is a memory folder at a format other than the one this
// release reads. An export migrates a scratch copy of an older one first
// (MigrateCopy), never the store itself.
var ErrExportFormat = errors.New("memory at a format this release does not export")

// StoreFormat is the memory format this release reads and writes.
const StoreFormat = storeFormat

// StoreExport is one store's memory as the `agent-memory-export` archive
// holds it (docs/export/format.md): every record in the order its file
// lists them, and every attachment the entries name, once.
type StoreExport struct {
	Format      int
	Seats       []ExportSeat
	Shelves     []ExportShelf
	Entries     []ExportEntry
	Names       []ExportName
	TopicMerges []ExportTopicMerge
	State       []any
	Slices      []ExportSlice
	Attachments []ExportAttachment
}

type ExportSeat struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Shelf     string           `json:"shelf,omitempty"`
	Expired   bool             `json:"expired,omitempty"`
	Private   bool             `json:"private,omitempty"`
	LastClaim *ExportLastClaim `json:"last_claim,omitempty"`
}

type ExportLastClaim struct {
	SessionID string `json:"session_id,omitempty"`
	Claimed   string `json:"claimed,omitempty"`
	Closed    string `json:"closed,omitempty"`
	Commit    string `json:"commit,omitempty"`
}

type ExportShelf struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Follows  []string `json:"follows,omitempty"`
	Lifetime string   `json:"lifetime,omitempty"`
}

type ExportEntry struct {
	ID                string              `json:"id"`
	Name              string              `json:"name,omitempty"`
	Title             string              `json:"title"`
	Body              string              `json:"body"`
	Dropped           bool                `json:"dropped,omitempty"`
	Kind              string              `json:"kind"`
	From              string              `json:"from,omitempty"`
	Retention         string              `json:"retention"`
	RetentionOverride string              `json:"retention_override,omitempty"`
	Seat              string              `json:"seat"`
	Shelf             string              `json:"shelf,omitempty"`
	Session           string              `json:"session,omitempty"`
	Commit            string              `json:"commit,omitempty"`
	Branch            string              `json:"branch,omitempty"`
	Anchors           []anchor            `json:"anchors,omitempty"`
	Filed             string              `json:"filed"`
	Changed           string              `json:"changed"`
	Confirmed         string              `json:"confirmed,omitempty"`
	Status            string              `json:"status"`
	StatusAt          string              `json:"status_at,omitempty"`
	StatusSeat        string              `json:"status_seat,omitempty"`
	StatusReason      string              `json:"status_reason,omitempty"`
	SupersededBy      string              `json:"superseded_by,omitempty"`
	EndedBy           string              `json:"ended_by,omitempty"`
	Revived           string              `json:"revived,omitempty"`
	Topics            []string            `json:"topics"`
	For               any                 `json:"for,omitempty"`
	Links             []ExportLink        `json:"links,omitempty"`
	Until             []string            `json:"until,omitempty"`
	UntilHashes       map[string]string   `json:"until_hashes,omitempty"`
	UntilHold         *hold               `json:"until_hold,omitempty"`
	Due               string              `json:"due,omitempty"`
	When              []string            `json:"when,omitempty"`
	On                []string            `json:"on,omitempty"`
	Proof             []string            `json:"proof,omitempty"`
	About             []string            `json:"about,omitempty"`
	Forbids           []string            `json:"forbids,omitempty"`
	Attachments       []ExportAttachedRef `json:"attachments,omitempty"`
	Items             []ExportItem        `json:"items,omitempty"`
	ItemsChanged      string              `json:"items_changed,omitempty"`
	Events            []ExportEvent       `json:"events"`
}

type ExportLink struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Via    string `json:"via"`
	Seat   string `json:"seat,omitempty"`
	At     string `json:"at"`
}

type ExportAttachedRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Stored bool   `json:"stored"`
	Seat   string `json:"seat"`
	At     string `json:"at"`
}

type ExportItem struct {
	ID      string `json:"id"`
	Text    string `json:"text,omitempty"`
	Link    string `json:"link,omitempty"`
	State   string `json:"state"`
	Set     bool   `json:"set"`
	Reason  string `json:"reason,omitempty"`
	From    string `json:"from,omitempty"`
	Added   string `json:"added"`
	StateAt string `json:"state_at,omitempty"`
}

type ExportEvent struct {
	Kind   string          `json:"kind"`
	At     string          `json:"at"`
	Seat   string          `json:"seat"`
	Target string          `json:"target,omitempty"`
	Data   json.RawMessage `json:"data"`
}

type ExportName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ExportTopicMerge struct {
	From string `json:"from"`
	Into string `json:"into"`
	Seat string `json:"seat"`
	At   string `json:"at"`
}

type ExportLine struct {
	Record      string            `json:"record"`
	Seat        string            `json:"seat"`
	ID          string            `json:"id"`
	Text        string            `json:"text"`
	Link        string            `json:"link,omitempty"`
	Hook        string            `json:"hook,omitempty"`
	Commit      string            `json:"commit,omitempty"`
	Paths       []string          `json:"paths,omitempty"`
	Session     string            `json:"session,omitempty"`
	Until       []string          `json:"until,omitempty"`
	UntilHashes map[string]string `json:"until_hashes,omitempty"`
	UntilHold   *hold             `json:"until_hold,omitempty"`
	Due         string            `json:"due,omitempty"`
	Added       string            `json:"added"`
	State       string            `json:"state"`
	Set         bool              `json:"set"`
	Events      []ExportEvent     `json:"events"`
}

type ExportOldState struct {
	Record  string        `json:"record"`
	Seat    string        `json:"seat"`
	At      string        `json:"at"`
	Body    string        `json:"body"`
	Session string        `json:"session,omitempty"`
	Branch  string        `json:"branch,omitempty"`
	Commit  string        `json:"commit,omitempty"`
	Draft   bool          `json:"draft,omitempty"`
	Settled string        `json:"settled,omitempty"`
	Events  []ExportEvent `json:"events,omitempty"`
}

// ExportSlice is a seat's slice, the bytes the store holds.
type ExportSlice struct {
	Seat string
	Data []byte
}

// ExportAttachment is one file the store's entries name: Stored where the
// machine holds bytes matching it, Problem saying why not otherwise. Form is
// how the machine keeps it, which ReadAttachment needs.
type ExportAttachment struct {
	SHA256  string
	Size    int64
	Form    string
	Name    string
	Stored  bool
	Problem string
}

func exportStamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format(time.RFC3339Nano)
}

// rawRow is one log row with its payload, as the log holds it.
type rawRow struct {
	row     memorylog.Row
	payload []byte
}

// Export reads the memory folder dir, below roots' data root, for the
// archive. It reads a snapshot of the log as every reader does, folds it
// with seats kept by their ids, and writes nothing: no fold cache, no
// migration, no name index.
func Export(ctx context.Context, roots home.Roots, dir string) (StoreExport, error) {
	format, err := formatsteps.Read(dir)
	if err != nil {
		return StoreExport{}, fmt.Errorf("reading the memory format of %s: %w", dir, err)
	}

	if format != storeFormat {
		return StoreExport{}, fmt.Errorf("%s is at memory format %d, and this release exports format %d alone: %w",
			dir, format, storeFormat, ErrExportFormat)
	}

	mem := &memory{env: Env{Roots: roots}, dir: dir}

	registry, _, err := seat.LoadRegistry(dir)
	if err != nil {
		return StoreExport{}, err
	}

	shelves, _, err := seat.LoadShelves(dir)
	if err != nil {
		return StoreExport{}, err
	}

	names, err := mem.loadNames()
	if err != nil {
		return StoreExport{}, err
	}

	folded, rows, err := mem.exportFold(ctx)
	if err != nil {
		return StoreExport{}, err
	}

	exported := StoreExport{Format: format, Seats: exportSeats(dir, registry), Shelves: exportShelves(shelves)}

	byEntry := map[string][]rawRow{}

	for _, raw := range rows {
		byEntry[raw.row.Entry] = append(byEntry[raw.row.Entry], raw)
	}

	attachments := map[string]*ExportAttachment{}
	var attachmentOrder []string

	for _, id := range folded.order {
		entry, err := mem.exportEntry(folded, folded.entries[id], byEntry[id], registry, names, attachments, &attachmentOrder)
		if err != nil {
			return StoreExport{}, err
		}

		exported.Entries = append(exported.Entries, entry)
	}

	for _, sha := range attachmentOrder {
		exported.Attachments = append(exported.Attachments, *attachments[sha])
	}

	for _, id := range slices.Sorted(maps.Keys(names.names)) {
		_, held := folded.entries[id]
		if !held {
			exported.Names = append(exported.Names, ExportName{ID: id, Name: names.names[id]})
		}
	}

	for _, merge := range folded.merges {
		exported.TopicMerges = append(exported.TopicMerges, ExportTopicMerge{
			From: merge.from, Into: merge.into, Seat: merge.seat, At: exportStamp(merge.at),
		})
	}

	exported.State, err = exportState(folded, byEntry[ledgerID])
	if err != nil {
		return StoreExport{}, err
	}

	exported.Slices, err = exportSlices(dir)
	if err != nil {
		return StoreExport{}, err
	}

	return exported, nil
}

// MigrateCopy brings the memory folder of the store id below roots to
// StoreFormat through the store's own step table. roots is a scratch home an
// export copied the folder into: every step writes below it, the session
// slots of its cache included, and never below the machine's home.
func MigrateCopy(ctx context.Context, roots home.Roots, id string) error {
	mem := &memory{env: Env{Roots: roots, Random: rand.Reader, Now: time.Now}, key: id}

	err := formatsteps.Run(ctx, mem.storeComponent())
	if err != nil {
		return fmt.Errorf("migrating a copy of the memory of %s: %w", id, err)
	}

	return nil
}

// ReadAttachment is the attachment's original bytes, checked against its
// hash and size as a read of it is.
func ReadAttachment(roots home.Roots, held ExportAttachment) ([]byte, error) {
	mem := &memory{env: Env{Roots: roots}}

	return mem.readStored(attachPayload{Name: held.Name, SHA256: held.SHA256, Size: held.Size, Form: held.Form})
}

// exportFold folds a snapshot of the log, every seat kept by the id the log
// names it by, and answers every row with its payload. A store with no log
// folds empty, and none is created.
func (mem *memory) exportFold(ctx context.Context) (*state, []rawRow, error) {
	_, err := os.Stat(mem.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return newState(), nil, nil
	}

	// The folder may be a scratch copy outside the data root, so the log is
	// held to the folder itself.
	log, err := memorylog.Open(mem.store(), mem.logDir())
	if err != nil {
		return nil, nil, fmt.Errorf("opening the log: %w", err)
	}

	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the log: %w", err)
	}

	defer func() { _ = snapshot.Close() }()

	byID := func(key string) string { return key }

	folded, err := fold(snapshot, byID)
	if err != nil {
		return nil, nil, err
	}

	rows := make([]rawRow, 0, len(snapshot.Rows()))

	for index, row := range snapshot.Rows() {
		payload, err := snapshot.Payload(index)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the payload of %s %s: %w", row.Kind, row.Entry, err)
		}

		rows = append(rows, rawRow{row: row, payload: slices.Clone(payload)})
	}

	return folded, rows, nil
}

func exportSeats(dir string, registry seat.Registry) []ExportSeat {
	seats := make([]ExportSeat, 0, len(registry.Seats))
	claims := seat.Dir(dir)

	for _, id := range registry.IDs() {
		held := registry.Seats[id]
		one := ExportSeat{ID: id, Name: held.Name, Shelf: held.Shelf, Expired: held.Expired, Private: held.Private}

		claim, found := seat.Read(claims, id)
		if !found || claim.SessionID == "" {
			claim, found = seat.ReadLast(claims, id)
		}

		if found {
			one.LastClaim = &ExportLastClaim{
				SessionID: claim.SessionID, Claimed: claim.Claimed, Closed: claim.Closed, Commit: claim.Commit,
			}
		}

		seats = append(seats, one)
	}

	return seats
}

func exportShelves(shelves seat.Shelves) []ExportShelf {
	exported := make([]ExportShelf, 0, len(shelves.Shelves))

	for _, id := range shelves.IDs() {
		held := shelves.Shelves[id]
		exported = append(exported, ExportShelf{ID: id, Name: held.Name, Follows: held.Follows, Lifetime: held.Lifetime})
	}

	return exported
}

func exportSlices(dir string) ([]ExportSlice, error) {
	paths, err := filepath.Glob(filepath.Join(seat.Dir(dir), "*.slice"))
	if err != nil {
		return nil, err
	}

	slices.Sort(paths)

	var exported []ExportSlice

	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".slice")
		if !seat.IsID(id) {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading the slice of %s: %w", id, err)
		}

		exported = append(exported, ExportSlice{Seat: id, Data: data})
	}

	return exported, nil
}

// exportData is an event's payload as the archive carries it: an object,
// without a field the store left null, without a `new` row's body, which the folded record carries, or an attach
// row's storage form, which the archive does not keep.
func exportData(kind memorylog.Kind, payload []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return json.RawMessage("{}"), nil
	}

	var fields map[string]json.RawMessage

	err := json.Unmarshal(payload, &fields)
	if err != nil {
		return nil, fmt.Errorf("reading a %s payload: %w", kind, err)
	}

	if fields == nil {
		return json.RawMessage("{}"), nil
	}

	for key, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(fields, key)
		}
	}

	switch kind {
	case memorylog.KindNew:
		delete(fields, "body")
	case memorylog.KindAttach:
		delete(fields, "form")
	}

	return marshalPlain(fields)
}

// marshalPlain encodes value as JSON escaping nothing JSON does not need:
// text is exported as filed.
func marshalPlain(value any) (json.RawMessage, error) {
	var encoded bytes.Buffer

	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(value)
	if err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

func exportEvent(raw rawRow) (ExportEvent, error) {
	data, err := exportData(raw.row.Kind, raw.payload)
	if err != nil {
		return ExportEvent{}, err
	}

	return ExportEvent{
		Kind: string(raw.row.Kind), At: exportStamp(raw.row.Time), Seat: raw.row.Seat, Target: raw.row.Target, Data: data,
	}, nil
}

func linkKind(target string) string {
	switch {
	case isToolLink(target):
		return "tool"
	case strings.HasPrefix(target, "m-"):
		return "memory"
	}

	return "graph"
}

func (mem *memory) exportEntry(folded *state, held *entry, rows []rawRow, registry seat.Registry, names nameIndex,
	attachments map[string]*ExportAttachment, order *[]string,
) (ExportEntry, error) {
	exported := ExportEntry{
		ID: held.id, Name: names.names[held.id], Title: held.title, Body: held.body, Dropped: held.dropped,
		Kind: held.kind, From: held.from, Retention: held.retention, RetentionOverride: held.retainedOverride(),
		Seat: held.seat, Shelf: registry.Seats[held.seat].Shelf, Session: held.session, Commit: held.commit,
		Branch: held.branch, Anchors: held.anchors, Filed: exportStamp(held.filed), Confirmed: exportStamp(held.verified),
		Status: held.status, Revived: exportStamp(held.revived), Topics: folded.topicsOf(held), Until: held.until,
		UntilHashes: held.untilHashes, UntilHold: held.untilHold, Due: exportStamp(held.due), When: held.when,
		On: held.on, Proof: held.proof, About: held.about, Forbids: held.forbids,
		ItemsChanged: exportStamp(held.itemsChanged), Events: []ExportEvent{},
	}

	if exported.Topics == nil {
		exported.Topics = []string{}
	}

	switch {
	case held.forAll:
		exported.For = "all"
	case len(held.forKeys) > 0:
		exported.For = held.forKeys
	}

	last, ended := held.latest()
	if ended && held.status != statusLive {
		exported.StatusAt, exported.StatusSeat, exported.StatusReason = exportStamp(last.at), last.seat, last.because
		exported.SupersededBy = last.by

		condition, found := strings.CutPrefix(last.because, endReached)
		if found {
			exported.EndedBy = condition
		}
	}

	changed := held.filed
	linked := map[string]time.Time{}
	stateAt := map[string]time.Time{}

	for _, raw := range rows {
		event, err := exportEvent(raw)
		if err != nil {
			return ExportEntry{}, fmt.Errorf("exporting %s: %w", held.id, err)
		}

		exported.Events = append(exported.Events, event)

		if raw.row.Time.After(changed) {
			changed = raw.row.Time
		}

		switch raw.row.Kind {
		case memorylog.KindLink:
			linked[raw.row.Target] = raw.row.Time
		case memorylog.KindItem:
			var change itemPayload

			if json.Unmarshal(raw.payload, &change) == nil && change.Op != itemOrder && (change.Op != itemAdd || change.State != "") {
				stateAt[change.ID] = raw.row.Time
			}
		case memorylog.KindAttach:
			ref, err := mem.exportAttached(raw, attachments, order)
			if err != nil {
				return ExportEntry{}, fmt.Errorf("exporting %s: %w", held.id, err)
			}

			exported.Attachments = append(exported.Attachments, ref)
		}
	}

	exported.Changed = exportStamp(changed)

	for _, target := range held.textLinks {
		exported.Links = append(exported.Links, ExportLink{
			Target: target, Kind: linkKind(target), Via: "text", At: exportStamp(held.filed),
		})
	}

	for _, link := range held.explicit {
		at, found := linked[link.target]
		if !found {
			at = held.filed
		}

		exported.Links = append(exported.Links, ExportLink{
			Target: link.target, Kind: linkKind(link.target), Via: "explicit", Seat: link.seat, At: exportStamp(at),
		})
	}

	for _, item := range held.items {
		exported.Items = append(exported.Items, ExportItem{
			ID: item.ID, Text: item.Text, Link: item.Link, State: item.State, Set: item.Set, Reason: item.Reason,
			From: item.From, Added: exportStamp(item.added), StateAt: exportStamp(stateAt[item.ID]),
		})
	}

	return exported, nil
}

// exportAttached is the record of one attach row, the file it names read
// once per store to tell whether the machine still holds its bytes.
func (mem *memory) exportAttached(raw rawRow, attachments map[string]*ExportAttachment, order *[]string) (ExportAttachedRef, error) {
	var attached attachPayload

	err := json.Unmarshal(raw.payload, &attached)
	if err != nil {
		return ExportAttachedRef{}, fmt.Errorf("reading an attach payload: %w", err)
	}

	known, found := attachments[attached.SHA256]
	if !found {
		known = &ExportAttachment{SHA256: attached.SHA256, Size: attached.Size, Form: attached.Form, Name: attached.Name}

		_, err = mem.readStored(attached)
		if err != nil {
			known.Problem = err.Error()
		} else {
			known.Stored = true
		}

		attachments[attached.SHA256] = known
		*order = append(*order, attached.SHA256)
	}

	return ExportAttachedRef{
		Name: attached.Name, SHA256: attached.SHA256, Size: attached.Size, Stored: known.Stored, Seat: raw.row.Seat,
		At: exportStamp(raw.row.Time),
	}, nil
}

// exportState is every seat's ledger lines, then its old free-text state,
// seats in ascending id order.
func exportState(folded *state, rows []rawRow) ([]any, error) {
	seats := map[string]bool{}
	for id := range folded.ledgers {
		seats[id] = true
	}

	for id := range folded.states {
		seats[id] = true
	}

	lineEvents := map[string][]ExportEvent{}

	for _, raw := range rows {
		if raw.row.Kind != memorylog.KindItem {
			continue
		}

		var change itemPayload

		err := json.Unmarshal(raw.payload, &change)
		if err != nil {
			return nil, fmt.Errorf("reading a state line payload: %w", err)
		}

		event, err := exportEvent(raw)
		if err != nil {
			return nil, err
		}

		key := raw.row.Seat + "#" + change.ID
		lineEvents[key] = append(lineEvents[key], event)
	}

	var records []any

	for _, id := range slices.Sorted(maps.Keys(seats)) {
		held := folded.ledgerFor(id)

		for _, line := range held.Lines {
			events := lineEvents[id+"#"+line.ID]
			if events == nil {
				events = []ExportEvent{}
			}

			records = append(records, ExportLine{
				Record: "line", Seat: id, ID: line.ID, Text: line.Text, Link: line.Link, Hook: line.Hook,
				Commit: line.Commit, Paths: line.Paths, Session: line.Session, Until: line.Until,
				UntilHashes: line.UntilHashes, UntilHold: line.UntilHold, Due: exportStamp(line.Due),
				Added: exportStamp(line.Added), State: line.State, Set: line.Set, Events: events,
			})
		}

		slot, found := folded.states[id]
		if !found {
			continue
		}

		old := ExportOldState{
			Record: "old", Seat: id, At: exportStamp(slot.at), Body: slot.Body, Session: slot.Session,
			Branch: slot.Branch, Commit: slot.Commit, Draft: slot.Draft, Events: lineEvents[id+"#"+oldLine],
		}

		if held.OldSettled.After(slot.at) {
			old.Settled = exportStamp(held.OldSettled)
		}

		records = append(records, old)
	}

	return records, nil
}
