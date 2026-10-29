package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
)

// presented is what the calling session's context already holds of each
// entry: its line, printed by a brief, recall, search or view list, or its
// body, printed by show or a view's bodies. A read printing either again
// prints a pointer instead, since the agent still has the text; the set
// empties where the context does, at a compaction, `/clear` and resume.
// Fresh subagents share their parent's session and pass again. Each mark
// carries the entry's state as it was printed, so an entry that changed since
// (a retire, a new link, a drifted anchor) prints in full again.
type presented struct {
	key     string
	set     shownSet
	again   bool
	changed bool
	here    tree
}

// presenting opens the seen set for one read, and returns what closes it. A
// read inside another (the brief a claim prints) shares the outer one's set.
func (mem *memory) presenting(ctx context.Context, again bool) func() {
	if mem.seen != nil {
		return func() {}
	}

	_, claim := mem.ownSeat(ctx)
	if claim.SessionID == "" {
		return func() {}
	}

	seen := &presented{key: mem.shownKey(claim.SessionID), again: again, here: mem.checkout(ctx)}
	mem.readSlot(seen.key, &seen.set)
	mem.seen = seen

	return func() {
		if seen.changed {
			_ = mem.writeSlot(seen.key, &seen.set)
		}

		mem.seen = nil
	}
}

// presentedKey names held as it stands: its id, and a digest of its full
// line, which carries its status, flags and links, and of its event count.
func presentedKey(here tree, held *entry) string {
	digest := sha256.Sum256([]byte(line(here, held) + "\n" + strconv.Itoa(len(held.events))))

	return held.id + "@" + hex.EncodeToString(digest[:6])
}

// hasLine is whether held's line or body, as held stands now, already stands
// in the context.
func (seen *presented) hasLine(held *entry) bool {
	if seen == nil || seen.again {
		return false
	}

	key := presentedKey(seen.here, held)

	return slices.Contains(seen.set.IDs, key) || slices.Contains(seen.set.Bodies, key)
}

// hasBody is whether held's body, as held stands now, already stands in the
// context.
func (seen *presented) hasBody(held *entry) bool {
	return seen != nil && !seen.again && slices.Contains(seen.set.Bodies, presentedKey(seen.here, held))
}

// markLine records held's line as standing in the context, once printed.
func (seen *presented) markLine(held *entry) {
	if seen != nil {
		seen.set.IDs = seen.mark(seen.set.IDs, presentedKey(seen.here, held))
	}
}

// markBody records held's body, and with it its line, as standing.
func (seen *presented) markBody(held *entry) {
	if seen != nil {
		key := presentedKey(seen.here, held)
		seen.set.Bodies = seen.mark(seen.set.Bodies, key)
		seen.set.IDs = seen.mark(seen.set.IDs, key)
	}
}

func (seen *presented) mark(ids []string, id string) []string {
	if slices.Contains(ids, id) {
		return ids
	}

	seen.changed = true

	return append(ids, id)
}

// lineOf is text, held's line, or its pointer where the line already stands.
func (mem *memory) lineOf(held *entry, text string) string {
	if mem.seen.hasLine(held) {
		return pointer(held)
	}

	mem.seen.markLine(held)

	return text
}

// addLine adds held's text, or its pointer, to out, and marks the line only
// once out took it: a line a budget cut never stood in the context.
func (mem *memory) addLine(out *budget, indent string, held *entry, text string) bool {
	if mem.seen.hasLine(held) {
		return out.add(indent + pointer(held))
	}

	if !out.add(indent + text) {
		return false
	}

	mem.seen.markLine(held)

	return true
}

// objectOf is object, or its seen form where what it carries already stands:
// the body where it carries one, else the line.
func (mem *memory) objectOf(held *entry, object jsonEntry) jsonEntry {
	if object.Body != nil {
		object.Seen = mem.seen.hasBody(held)
		mem.seen.markBody(held)
	} else {
		object.Seen = mem.seen.hasLine(held)
		mem.seen.markLine(held)
	}

	return object
}

func pointer(held *entry) string {
	return held.id + " " + held.title + " (seen)"
}

// jsonSeenEntry is an entry the calling session's context already holds: its
// id and title alone, and a search hit's score, as the text answers with its
// pointer.
type jsonSeenEntry struct {
	ID    string `json:"id"`
	Score *int   `json:"score,omitempty"`
	Title string `json:"title,omitempty"`
	Seen  bool   `json:"seen"`
}

func (object jsonEntry) MarshalJSON() ([]byte, error) {
	if object.Seen {
		return json.Marshal(jsonSeenEntry{ID: object.ID, Score: object.Score, Title: object.Title, Seen: true})
	}

	type plain jsonEntry

	return json.Marshal(plain(object))
}

func (jsonEntry) SchemaAlternative() any {
	return jsonSeenEntry{}
}
