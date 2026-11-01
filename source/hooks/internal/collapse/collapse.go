// Package collapse shortens what sdd's MCP server answers an agent to what
// that agent's context does not already hold: a chain entry, search hit or
// view entry whose line it was shown unchanged, and an instruction lane,
// framing block or report schema it was served byte for byte, become a pointer
// marked `shown_earlier`. An entry a `show` names in its ids, a bodies view's
// entries, and every piece of a `resume_session`, print in full, since the
// call asks for exactly that text.
//
// It reads only the structured fields sdd answers under
// `sessions.token_saving: off` (`items`, `hits`, `view_sections`, `lanes`,
// `framing_blocks`);
// an answer without them, from an older sdd or with the key on, passes
// through untouched, so no text layout is ever parsed.
package collapse

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Note is the line an answer holding a pointer gains, saying how the text
// behind it comes back.
const Note = "pieces marked shown_earlier repeat text this agent was already shown unchanged; " +
	"show an entry by its id, or resume_session, prints it in full"

const (
	noteKey   = "shown_earlier_note"
	markKey   = "shown_earlier"
	itemsKey  = "items"
	hitsKey   = "hits"
	viewKey   = "view_sections"
	lanesKey  = "lanes"
	blocksKey = "framing_blocks"
	schemaKey = "report_schema"
)

// Set is what one agent's context holds of sdd's answers: each entry's line,
// by id, as a digest of its title, kind, status and summary, and each served
// piece, by kind and name, as a digest of its bytes.
type Set struct {
	Lines  map[string]string `json:"lines,omitempty"`
	Pieces map[string]string `json:"pieces,omitempty"`
}

// Empty is whether the set holds nothing.
func (held Set) Empty() bool {
	return len(held.Lines) == 0 && len(held.Pieces) == 0
}

type object = map[string]json.RawMessage

// pass is one answer's walk: what it marks applies to held only once the
// whole answer is read, so nothing in an answer collapses against itself.
type pass struct {
	held      Set
	lines     map[string]string
	pieces    map[string]string
	collapsed bool
}

// Answer collapses the answer an sdd MCP tool returned to the agent holding
// held, and marks in held what the answer printed in full. It answers the
// shortened answer as one JSON object, and false where nothing collapsed or
// the answer carries no structure.
func Answer(tool string, response json.RawMessage, held *Set) (json.RawMessage, bool) {
	answer, found := structured(response)
	if !found {
		return nil, false
	}

	walk := &pass{held: *held, lines: map[string]string{}, pieces: map[string]string{}}

	switch tool {
	case "show":
		walk.items(answer)
	case "search":
		walk.hits(answer)
	case "view":
		walk.view(answer)
	case "resume_session":
		walk.serve(answer, false)
	default:
		walk.serve(answer, true)
	}

	walk.apply(held)

	if !walk.collapsed {
		return nil, false
	}

	answer[noteKey], _ = json.Marshal(Note)

	shortened, err := json.Marshal(answer)
	if err != nil {
		return nil, false
	}

	return shortened, true
}

func (walk *pass) apply(held *Set) {
	if len(walk.lines) > 0 && held.Lines == nil {
		held.Lines = map[string]string{}
	}

	for id, digest := range walk.lines {
		held.Lines[id] = digest
	}

	if len(walk.pieces) > 0 && held.Pieces == nil {
		held.Pieces = map[string]string{}
	}

	for key, digest := range walk.pieces {
		held.Pieces[key] = digest
	}
}

// structured is the answer as one object where it carries a field this
// package reads, in each form Claude Code hands an MCP answer on: the object
// itself, it encoded as a string, under `structuredContent`, or as the text
// of a content block.
func structured(raw json.RawMessage) (object, bool) {
	var text string

	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}

	var direct object

	if json.Unmarshal(raw, &direct) == nil && carries(direct) {
		return direct, true
	}

	type block struct {
		Text string `json:"text"`
	}

	var wrapped struct {
		Structured object  `json:"structuredContent"`
		Content    []block `json:"content"`
	}

	blocks := wrapped.Content

	if json.Unmarshal(raw, &wrapped) == nil {
		if carries(wrapped.Structured) {
			return wrapped.Structured, true
		}

		blocks = wrapped.Content
	} else if json.Unmarshal(raw, &blocks) != nil {
		return nil, false
	}

	for _, block := range blocks {
		var answered object

		if json.Unmarshal([]byte(block.Text), &answered) == nil && carries(answered) {
			return answered, true
		}
	}

	return nil, false
}

// carries is whether the answer holds a structured field, at its top or in a
// serve it carries: `abandon`, and a `next` ending a move, answer the
// session's junction under `base_junction` alone.
func carries(answer object) bool {
	for _, key := range []string{itemsKey, hitsKey, viewKey, lanesKey, blocksKey} {
		_, found := answer[key]
		if found {
			return true
		}
	}

	var junction object

	if json.Unmarshal(answer["base_junction"], &junction) == nil && carries(junction) {
		return true
	}

	var instances []object

	_ = json.Unmarshal(answer["open_instances"], &instances)

	for _, instance := range instances {
		if carries(instance) {
			return true
		}
	}

	return false
}

// items marks each entry a show names as printed and collapses the chains
// below it.
func (walk *pass) items(answer object) {
	var items []object

	if json.Unmarshal(answer[itemsKey], &items) != nil {
		return
	}

	for _, item := range items {
		walk.markLine(item)

		for _, side := range []string{"upstream", "downstream"} {
			chain, changed := walk.chain(item[side])
			if changed {
				item[side] = chain
			}
		}
	}

	walk.store(answer, itemsKey, items)
}

// chain collapses each chain item whose line the agent holds unchanged to its
// edge, id, title and depth, so the preorder list keeps its shape; an item the
// answer already prints elsewhere (`seen_in_response`) is sdd's own pointer.
func (walk *pass) chain(raw json.RawMessage) (json.RawMessage, bool) {
	var chain []object

	if raw == nil || json.Unmarshal(raw, &chain) != nil {
		return raw, false
	}

	changed := false

	for index, item := range chain {
		if flag(item, "seen_in_response") {
			continue
		}

		if !walk.holdsLine(item) {
			walk.markLine(item)

			continue
		}

		chain[index] = keep(item, "relation", "ref", "id", "title", "desc", "depth", "truncated")
		chain[index][markKey] = json.RawMessage("true")
		walk.collapsed, changed = true, true
	}

	if !changed {
		return raw, false
	}

	encoded, err := json.Marshal(chain)
	if err != nil {
		return raw, false
	}

	return encoded, true
}

// hits collapses each search hit whose line the agent holds unchanged to its
// id, title and score.
func (walk *pass) hits(answer object) {
	var hits []object

	if json.Unmarshal(answer[hitsKey], &hits) != nil {
		return
	}

	for index, hit := range hits {
		if !walk.holdsLine(hit) {
			walk.markLine(hit)

			continue
		}

		hits[index] = keep(hit, "id", "title", "score")
		hits[index][markKey] = json.RawMessage("true")
		walk.collapsed = true
	}

	walk.store(answer, hitsKey, hits)
}

// viewEntries names where a view section's entry objects sit, by its shape
// object first: a step names an object's field, `[]` every element of an
// array. A `counts` section holds no entry, so it never changes.
var viewEntries = [][]string{
	{"list", "items", "[]", "entry"},
	{"grouped", "groups", "[]", "entries", "[]"},
	{"focus", "focuses", "[]", "focus"},
	{"focus", "focuses", "[]", "targets", "[]", "target"},
	{"participants", "groups", "[]", "actor"},
	{"wip", "markers", "[]", "entry"},
}

// viewBodies is where a bodies view's entries sit: they print in full, as
// an asked id does, since the view asks for exactly their bodies.
var viewBodies = []string{"bodies", "entries", "[]"}

// view collapses each entry a view section holds whose line the agent holds
// unchanged to its id and title, keeping every field beside the entry, and
// marks each entry it prints in full. Only `view_sections` is read: a
// section's `list.items` is never a show's `items`.
func (walk *pass) view(answer object) {
	var sections []json.RawMessage

	_ = json.Unmarshal(answer[viewKey], &sections)

	for index := range sections {
		for _, path := range viewEntries {
			sections[index], _ = reach(sections[index], path, walk.viewEntry)
		}

		reach(sections[index], viewBodies, walk.bodiesEntry)
	}

	walk.store(answer, viewKey, sections)
}

func (walk *pass) viewEntry(raw json.RawMessage) (json.RawMessage, bool) {
	var entry object

	_ = json.Unmarshal(raw, &entry)

	if !walk.holdsLine(entry) {
		walk.markLine(entry)

		return raw, false
	}

	pointer := keep(entry, "id", "title")
	pointer[markKey] = json.RawMessage("true")

	encoded, err := json.Marshal(pointer)
	if err != nil {
		return raw, false
	}

	walk.collapsed = true

	return encoded, true
}

func (walk *pass) bodiesEntry(raw json.RawMessage) (json.RawMessage, bool) {
	var entry object

	_ = json.Unmarshal(raw, &entry)
	walk.markLine(entry)

	return raw, false
}

// reach runs visit on every value path leads to below raw, and answers raw
// re-encoded where a visit changed anything. A value that does not decode,
// or a field a section lacks, decodes to nothing and so holds nothing to
// visit: no entry without an id is ever marked or collapsed.
func reach(raw json.RawMessage, path []string, visit func(json.RawMessage) (json.RawMessage, bool)) (json.RawMessage, bool) {
	if len(path) == 0 {
		return visit(raw)
	}

	if path[0] == "[]" {
		var elements []json.RawMessage

		_ = json.Unmarshal(raw, &elements)

		changed := false

		for index, element := range elements {
			updated, elementChanged := reach(element, path[1:], visit)
			if elementChanged {
				elements[index], changed = updated, true
			}
		}

		return reencoded(raw, elements, changed)
	}

	var parent object

	_ = json.Unmarshal(raw, &parent)

	updated, changed := reach(parent[path[0]], path[1:], visit)
	if changed {
		parent[path[0]] = updated
	}

	return reencoded(raw, parent, changed)
}

func reencoded(raw json.RawMessage, value any, changed bool) (json.RawMessage, bool) {
	if !changed {
		return raw, false
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return raw, false
	}

	return encoded, true
}

// serve collapses, where collapsing, the lanes, framing blocks and report
// schema the agent was served byte for byte, in the serve and in every serve
// it carries.
func (walk *pass) serve(answer object, collapsing bool) {
	walk.served(answer, lanesKey, "lane:", collapsing)
	walk.served(answer, blocksKey, "framing:", collapsing)
	walk.schema(answer, collapsing)

	var instances []object

	if json.Unmarshal(answer["open_instances"], &instances) == nil && len(instances) > 0 {
		for _, instance := range instances {
			walk.serve(instance, collapsing)
		}

		walk.store(answer, "open_instances", instances)
	}

	var junction object

	if json.Unmarshal(answer["base_junction"], &junction) == nil && junction != nil {
		walk.serve(junction, collapsing)
		walk.store(answer, "base_junction", junction)
	}
}

func (walk *pass) served(answer object, field string, kind string, collapsing bool) {
	var pieces []object

	if json.Unmarshal(answer[field], &pieces) != nil {
		return
	}

	for index, piece := range pieces {
		var name string

		_ = json.Unmarshal(piece["name"], &name)
		key, value := kind+name, digest(piece["text"])

		if !collapsing || walk.held.Pieces[key] != value {
			walk.pieces[key] = value

			continue
		}

		pieces[index] = keep(piece, "name")
		pieces[index][markKey] = json.RawMessage("true")
		walk.collapsed = true
	}

	walk.store(answer, field, pieces)
}

func (walk *pass) schema(answer object, collapsing bool) {
	raw, found := answer[schemaKey]
	if !found || isNull(raw) {
		return
	}

	key, value := "schema:"+digest(raw), digest(raw)

	if !collapsing || walk.held.Pieces[key] != value {
		walk.pieces[key] = value

		return
	}

	answer[schemaKey] = json.RawMessage(`{"` + markKey + `":true}`)
	walk.collapsed = true
}

func (walk *pass) holdsLine(item object) bool {
	id := text(item["id"])

	return id != "" && walk.held.Lines[id] == lineDigest(item)
}

func (walk *pass) markLine(item object) {
	id := text(item["id"])
	if id != "" {
		walk.lines[id] = lineDigest(item)
	}
}

func (walk *pass) store(answer object, field string, value any) {
	encoded, err := json.Marshal(value)
	if err == nil {
		answer[field] = encoded
	}
}

// lineDigest covers what a pointer leaves out: an entry whose title, kind,
// status or summary changed prints in full again.
func lineDigest(item object) string {
	parts := make([]string, 0, 4)
	for _, field := range []string{"title", "kind", "status", "summary"} {
		parts = append(parts, text(item[field]))
	}

	return digest(json.RawMessage(strings.Join(parts, "\x00")))
}

func digest(raw json.RawMessage) string {
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:12])
}

func keep(item object, fields ...string) object {
	kept := object{}

	for _, field := range fields {
		value, found := item[field]
		if found {
			kept[field] = value
		}
	}

	return kept
}

func text(raw json.RawMessage) string {
	var value string

	_ = json.Unmarshal(raw, &value)

	return value
}

func flag(item object, field string) bool {
	var value bool

	_ = json.Unmarshal(item[field], &value)

	return value
}

func isNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null" || len(raw) == 0
}
