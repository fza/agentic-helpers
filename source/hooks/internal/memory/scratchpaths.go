package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Scratch moved from `.tmp/claude/<name>/` to `.tmp/seats/<seat or session>/`
// and `.tmp/keep/<name>/`. Every store rewrites the paths its entries quote
// once, in place, through a migration pass: ids, links, heat and history stay,
// and the format stays 7, so an older release still reads the store. A new
// event kind for the edits was rejected, since an older binary reads an
// unknown kind as a corrupt log.
const (
	scratchPathsMarker = "scratch-paths.json"
	oldScratchPrefix   = ".tmp/claude/"
	seatsScratchPrefix = ".tmp/seats/"
	keepScratchPrefix  = ".tmp/keep/"
)

// oldScratchPath is an old scratch path and the name after it: a run of
// characters a folder name holds. A name opening with a placeholder or a
// glob, `<seat>`, `$SESSION` or `*`, is no name, and the path stays.
var (
	oldScratchPath = regexp.MustCompile(`\.tmp/claude/[^/\s` + "`" + `'"()\[\]{}<>,;:|*?$\\]*`)
	sessionFolder  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// scratchTextFields are the payload fields of each event kind whose text may
// quote a path: an entry's title and body, a retitle, a todo item, a seat's
// state.
var scratchTextFields = map[memorylog.Kind][]string{
	memorylog.KindNew:     {"title", "body"},
	memorylog.KindRetitle: {"title"},
	memorylog.KindItem:    {"text"},
	memorylog.KindState:   {"body"},
}

type scratchMapper struct {
	seats map[string]bool
}

func newScratchMapper(registry seat.Registry) scratchMapper {
	seats := map[string]bool{}

	for _, held := range registry.Seats {
		seats[held.Name] = true
	}

	return scratchMapper{seats: seats}
}

// rewrite maps every old scratch path of text by rule: a seat of the store or
// a session id moves under `.tmp/seats/`, any other name under `.tmp/keep/`.
// `.tmp/claude/` with no name after it names the old layout itself, and
// stays.
func (mapper scratchMapper) rewrite(text string) string {
	return mapper.mapPaths(text, false)
}

// rewriteGlob is rewrite over a `path:` glob, mapping a path only where its
// name is literal: a glob within the name, `.tmp/claude/ma*`, could match a
// seat folder and a keep alike, and stays.
func (mapper scratchMapper) rewriteGlob(glob string) string {
	return mapper.mapPaths(glob, true)
}

func (mapper scratchMapper) mapPaths(text string, glob bool) string {
	var mapped strings.Builder

	last := 0

	for _, at := range oldScratchPath.FindAllStringIndex(text, -1) {
		found := text[at[0]:at[1]]
		mapped.WriteString(text[last:at[0]])
		last = at[1]

		if glob && at[1] < len(text) && text[at[1]] != '/' {
			mapped.WriteString(found)

			continue
		}

		mapped.WriteString(mapper.mapOne(found))
	}

	mapped.WriteString(text[last:])

	return mapped.String()
}

func (mapper scratchMapper) mapOne(found string) string {
	name := strings.TrimPrefix(found, oldScratchPrefix)

	switch {
	case name == "":
		return found
	case mapper.seats[strings.TrimRight(name, ".")] || sessionFolder.MatchString(name):
		return seatsScratchPrefix + name
	}

	return keepScratchPrefix + name
}

// storedPath maps a path a filing stored relative to the work tree: an
// anchor, a declared --about path, the path of an end. Filings that trimmed a
// leading dot as punctuation stored `.tmp/claude/<name>` as
// `tmp/claude/<name>`, so that form maps to the dotted new path; any other
// dotless `tmp/` path stays as stored, since nothing says it once had a dot.
func (mapper scratchMapper) storedPath(path string) string {
	if !strings.HasPrefix(path, "tmp/claude/") {
		return mapper.rewrite(path)
	}

	dotted := "." + path

	mapped := mapper.rewrite(dotted)
	if mapped == dotted {
		return path
	}

	return mapped
}

// end maps the path of a `changed:` or `gone:` end; any other end names no
// path.
func (mapper scratchMapper) end(condition string) string {
	form, value, named := strings.Cut(condition, ":")
	if !named || (form != "changed" && form != "gone") {
		return condition
	}

	return form + ":" + mapper.storedPath(value)
}

// trigger maps the glob of a `path:` trigger; a command or tool trigger
// names no path.
func (mapper scratchMapper) trigger(text string) string {
	form, value, named := strings.Cut(text, ":")
	if !named || form != "path" {
		return text
	}

	return form + ":" + mapper.rewriteGlob(value)
}

// pass rewrites the payload of every event that may quote a path, the
// anchors of a new or confirm event among them: a moved file keeps its
// content, so an anchor keeps its hash under its new path.
func (mapper scratchMapper) pass(events []memorylog.Event) error {
	for index, event := range events {
		rewritten, err := mapper.payload(event.Kind, event.Payload)
		if err != nil {
			return fmt.Errorf("rewriting the %s event of %s: %w", event.Kind, event.Entry, err)
		}

		events[index].Payload = rewritten
	}

	return nil
}

// payload rewrites the fields of one event's payload that may name a path. A
// `new` event alone carries the paths --about declares, its ends, its
// triggers and proofs, and the hash of each `changed:` path, keyed by it.
func (mapper scratchMapper) payload(kind memorylog.Kind, payload []byte) ([]byte, error) {
	texts := scratchTextFields[kind]
	anchored := kind == memorylog.KindNew || kind == memorylog.KindConfirm

	if len(texts) == 0 && !anchored {
		return payload, nil
	}

	var fields map[string]json.RawMessage

	err := json.Unmarshal(payload, &fields)
	if err != nil {
		return nil, fmt.Errorf("reading the payload: %w", err)
	}

	rewrites := []func() (bool, error){}

	for _, name := range texts {
		rewrites = append(rewrites, func() (bool, error) { return rewriteField(fields, name, mapper.rewrite) })
	}

	rewrites = append(rewrites, func() (bool, error) { return rewriteField(fields, "anchors", mapper.anchors) })

	if kind == memorylog.KindNew {
		rewrites = append(rewrites,
			func() (bool, error) { return rewriteField(fields, "about", each(mapper.storedPath)) },
			func() (bool, error) { return rewriteField(fields, "until", each(mapper.end)) },
			func() (bool, error) { return rewriteField(fields, "until_hashes", mapper.hashKeys) },
			func() (bool, error) { return rewriteField(fields, "when", each(mapper.trigger)) },
			func() (bool, error) { return rewriteField(fields, "proof", each(mapper.trigger)) },
		)
	}

	changed := false

	for _, rewrite := range rewrites {
		moved, err := rewrite()
		if err != nil {
			return nil, err
		}

		changed = changed || moved
	}

	// A payload no path changed stays byte for byte.
	if !changed {
		return payload, nil
	}

	return json.Marshal(fields)
}

// rewriteField decodes the field name of fields, where present, maps it, and
// encodes it back only where the mapping changed it, answering whether it
// did.
func rewriteField[T any](fields map[string]json.RawMessage, name string, mapping func(T) T) (bool, error) {
	raw, found := fields[name]
	if !found {
		return false, nil
	}

	var held T

	err := json.Unmarshal(raw, &held)
	if err != nil {
		return false, fmt.Errorf("reading its %s: %w", name, err)
	}

	before, err := json.Marshal(held)
	if err != nil {
		return false, fmt.Errorf("encoding its %s: %w", name, err)
	}

	after, err := json.Marshal(mapping(held))
	if err != nil {
		return false, fmt.Errorf("encoding its %s: %w", name, err)
	}

	if string(after) == string(before) {
		return false, nil
	}

	fields[name] = after

	return true, nil
}

func each(mapping func(string) string) func([]string) []string {
	return func(texts []string) []string {
		mapped := make([]string, 0, len(texts))
		for _, text := range texts {
			mapped = append(mapped, mapping(text))
		}

		return mapped
	}
}

func (mapper scratchMapper) anchors(anchors []anchor) []anchor {
	mapped := make([]anchor, 0, len(anchors))
	for _, held := range anchors {
		mapped = append(mapped, anchor{Path: mapper.storedPath(held.Path), Hash: held.Hash})
	}

	return mapped
}

func (mapper scratchMapper) hashKeys(hashes map[string]string) map[string]string {
	mapped := make(map[string]string, len(hashes))
	for path, hash := range hashes {
		mapped[mapper.storedPath(path)] = hash
	}

	return mapped
}

func (mem *memory) scratchPathsMarker() string {
	return filepath.Join(mem.store(), scratchPathsMarker)
}

func (mem *memory) scratchPathsRewritten() bool {
	info, err := os.Lstat(mem.scratchPathsMarker())

	return err == nil && info.Mode().IsRegular()
}

// rewriteScratchPaths runs the pass once per store, under the store lock: a
// call that waited on another's pass runs it again and finds nothing to
// change. Holding the log lock, the log is copied to
// backup/scratch-paths-<UTC stamp>/, then rewritten through the log's own
// merge, which lists the rebuilt segment in place of the old ones in one
// manifest write: a run stopped at any point leaves a log
// either release reads, and the next call runs the pass again, which finds
// nothing left to change in a log already rewritten. The marker is written
// last. A log no pass changes is left as it is, with no backup.
func (mem *memory) rewriteScratchPaths(ctx context.Context) error {
	if mem.scratchPathsRewritten() {
		return nil
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return err
	}

	defer unlock()

	err = mem.rewriteScratchLog(ctx)
	if err == nil {
		err = mem.markScratchPaths()
	}

	if err != nil {
		return fmt.Errorf("rewriting the scratch paths the store's entries name failed: %w; the next call tries again", err)
	}

	mem.recordStore(ctx)

	return nil
}

func (mem *memory) rewriteScratchLog(ctx context.Context) error {
	registry, _, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return err
	}

	mapper := newScratchMapper(registry)

	events, err := mem.allEvents(ctx)
	if err != nil {
		return err
	}

	original := make([][]byte, len(events))
	for index, event := range events {
		original[index] = event.Payload
	}

	err = mapper.pass(events)
	if err != nil {
		return err
	}

	if !payloadsChanged(events, original) {
		return nil
	}

	backup := filepath.Join(mem.store(), "backup", "scratch-paths-"+mem.now().Format(home.BackupStamp))

	// The copy is taken under the log lock the rewrite holds, so no row
	// appended between the two is missing from it.
	backUp := func() error {
		err := copyTree(mem.env.Roots.Data, mem.logDir(), filepath.Join(backup, "log"))
		if err != nil {
			return fmt.Errorf("backing up the store's log: %w", err)
		}

		return nil
	}

	log, err := mem.openLog()
	if err != nil {
		return err
	}

	err = log.RewriteAfter(ctx, backUp, func(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
		rewritten := make([]memorylog.Event, 0, len(rows))

		for index, row := range rows {
			held, err := payload(index)
			if err != nil {
				return nil, fmt.Errorf("reading the payload of %s %s: %w", row.Kind, row.Entry, err)
			}

			rewritten = append(rewritten, memorylog.Event{
				Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: held,
			})
		}

		return rewritten, mapper.pass(rewritten)
	})
	if err != nil {
		return fmt.Errorf("rewriting the log: %w", err)
	}

	return nil
}

func payloadsChanged(events []memorylog.Event, original [][]byte) bool {
	for index, event := range events {
		if string(event.Payload) != string(original[index]) {
			return true
		}
	}

	return false
}

type scratchPathsFile struct {
	Rewritten string `json:"rewritten"`
}

func (mem *memory) markScratchPaths() error {
	data, err := json.Marshal(scratchPathsFile{Rewritten: mem.now().Format(home.BackupStamp)})
	if err != nil {
		return fmt.Errorf("encoding the scratch paths marker: %w", err)
	}

	err = atomicfile.Write(mem.env.Roots.Data, mem.scratchPathsMarker(), append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("placing the scratch paths marker: %w", err)
	}

	return nil
}

// expireBackups removes the store's migration backups older than
// home.BackupLifetime, under the store lock, so a migration writing one
// never meets a removal. A removal failing is left to the next session; the
// size ledger is refreshed by the session start's later writes.
func (mem *memory) expireBackups(ctx context.Context) {
	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return
	}

	defer unlock()

	_ = home.ExpireBackups(ctx, mem.env.Roots.Data, filepath.Join(mem.store(), "backup"), mem.now())
}
