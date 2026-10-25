package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// forAllSeats is the word `--for` takes for every seat, present and future,
// and so the one word no seat may be named.
const forAllSeats = "all"

// recipients is who an entry was filed for besides its filer: every seat, or
// the seats it names by key. It is written as "all" or as the list of keys.
type recipients struct {
	All   bool
	Seats []string
}

func (held recipients) MarshalJSON() ([]byte, error) {
	if held.All {
		return json.Marshal(forAllSeats)
	}

	return json.Marshal(held.Seats)
}

func (held *recipients) UnmarshalJSON(raw []byte) error {
	var word string

	err := json.Unmarshal(raw, &word)
	if err == nil {
		held.All = word == forAllSeats

		return nil
	}

	err = json.Unmarshal(raw, &held.Seats)
	if err != nil {
		return fmt.Errorf("reading who an entry is for: %w", err)
	}

	return nil
}

// recipientsFor turns the seat names a `--for` gave into the keys an entry
// stores, refusing a name no seat has.
func (mem *memory) recipientsFor(names []string, filer string) (*recipients, error) {
	if len(names) == 0 {
		return nil, nil
	}

	if slices.Contains(names, forAllSeats) {
		return &recipients{All: true}, nil
	}

	held := &recipients{}

	for _, name := range names {
		key := mem.seatKey(name)
		if key == "" {
			return nil, fmt.Errorf("--for names no seat called %s; %s sweep lists the seats", name, self)
		}

		if name != filer && !slices.Contains(held.Seats, key) {
			held.Seats = append(held.Seats, key)
		}
	}

	return held, nil
}

// follows is every topic the seat named seatName follows, each read through
// the topic merges.
func (mem *memory) follows(folded *state, seatName string) []string {
	registry, found := mem.registry()
	if !found {
		return nil
	}

	id, known := registry.IDOf(seatName)
	if !known {
		return nil
	}

	var topics []string
	for _, topic := range registry.Seats[id].Follows {
		topics = append(topics, folded.resolve(topic))
	}

	return topics
}

// receives is whether the entry reaches seatName without being its own: filed
// for every seat, for seatName by key, or under a topic seatName follows.
func (mem *memory) receives(folded *state, held *entry, seatName string) bool {
	if seatName == "" || held.seat == seatName {
		return false
	}

	if held.forAll || slices.Contains(held.forKeys, mem.seatKey(seatName)) {
		return true
	}

	followed := mem.follows(folded, seatName)

	return slices.ContainsFunc(folded.topicsOf(held), func(topic string) bool { return slices.Contains(followed, topic) })
}

// ownOrReceived is whether the entry stands with seatName's own.
func (mem *memory) ownOrReceived(folded *state, held *entry, seatName string) bool {
	return held.seat == seatName || mem.receives(folded, held, seatName)
}

// annotate names each entry's recipients the way its line shows them: every
// seat, the seats it was filed for, or the followers of its topics where only a
// follow brings it to anyone.
func (mem *memory) annotate(folded *state) {
	registry, found := mem.registry()

	followed := map[string]bool{}

	if found {
		for _, id := range registry.IDs() {
			for _, topic := range registry.Seats[id].Follows {
				followed[folded.resolve(topic)] = true
			}
		}
	}

	for _, held := range folded.entries {
		switch {
		case held.forAll:
			held.recipients = forAllSeats
		case len(held.forKeys) > 0:
			names := make([]string, 0, len(held.forKeys))
			for _, key := range held.forKeys {
				names = append(names, mem.seatName(key))
			}

			held.recipients = strings.Join(names, ", ")
		default:
			var topics []string

			for _, topic := range folded.topicsOf(held) {
				if followed[topic] {
					topics = append(topics, topic)
				}
			}

			held.recipients = ""
			if len(topics) > 0 {
				held.recipients = strings.Join(topics, ", ") + " followers"
			}
		}
	}
}

// setFollows changes what the seat the calling session holds follows.
func (mem *memory) setFollows(ctx context.Context, req request, add bool) result {
	own, _ := mem.ownSeat(ctx)
	if own == "" {
		return refused("%s", ErrNoSeat)
	}

	var topics []string

	for _, topic := range req.Topics {
		topic = normalTopic(topic)
		if topic != "" {
			topics = append(topics, topic)
		}
	}

	if len(topics) == 0 {
		return refused("name at least one topic")
	}

	unlock, err := mem.lockStore(ctx)
	if err != nil {
		return refused("%v", err)
	}
	defer unlock()

	registry, found, err := seat.LoadRegistry(mem.store())
	if err != nil {
		return refused("%v", err)
	}

	id, known := registry.IDOf(own)
	if !found || !known {
		return refused("this store keeps no seat ids yet, so %s cannot follow topics", own)
	}

	held := registry.Seats[id]

	for _, topic := range topics {
		switch {
		case add && !slices.Contains(held.Follows, topic):
			held.Follows = append(held.Follows, topic)
		case !add:
			held.Follows = slices.DeleteFunc(held.Follows, func(followed string) bool { return followed == topic })
		}
	}

	slices.Sort(held.Follows)
	registry.Seats[id] = held

	err = registry.Save()
	if err != nil {
		return refused("%v", err)
	}

	mem.forgetRegistry()

	if len(held.Follows) == 0 {
		return done(own + " follows no topic")
	}

	return done(fmt.Sprintf("%s follows %s", own, strings.Join(held.Follows, ", ")))
}

func (mem *memory) follow(ctx context.Context, req request) result {
	return mem.setFollows(ctx, req, true)
}

func (mem *memory) unfollow(ctx context.Context, req request) result {
	return mem.setFollows(ctx, req, false)
}

// seenMark is when the newest entry a seat was told had arrived was filed, and
// how the log stood when the seat last looked.
type seenMark struct {
	At  time.Time `json:"at"`
	Log string    `json:"log,omitempty"`
}

func (mem *memory) seenPath(seatName string) string {
	return filepath.Join(mem.cacheDir(), "briefs", mem.seatKey(seatName)+".seen.json")
}

// arrivals is every entry seatName receives that was filed after its seen mark,
// oldest first. A seat without a mark has seen everything filed so far.
func (mem *memory) arrivals(folded *state, seatName string) []*entry {
	var mark seenMark
	if !readJSON(mem.seenPath(seatName), &mark) {
		mem.markSeen(seatName, mem.now())

		return nil
	}

	var arrived []*entry

	for _, held := range folded.entryList() {
		if held.status == statusLive && held.filed.After(mark.At) && mem.receives(folded, held, seatName) {
			arrived = append(arrived, held)
		}
	}

	slices.SortStableFunc(arrived, func(a, b *entry) int { return a.filed.Compare(b.filed) })

	return arrived
}

func (mem *memory) markSeen(seatName string, at time.Time) {
	var mark seenMark
	readJSON(mem.seenPath(seatName), &mark)
	mark.At = at
	_ = writeJSON(mem.seenPath(seatName), mark)
}

func (mem *memory) noteLog(seatName string, signature string) {
	var mark seenMark
	readJSON(mem.seenPath(seatName), &mark)
	mark.Log = signature
	_ = writeJSON(mem.seenPath(seatName), mark)
}

// announce is the lines telling seatName what arrived, and moves its mark past
// them; nothing where nothing did.
func (mem *memory) announce(ctx context.Context, folded *state, seatName string) string {
	arrived := mem.arrivals(folded, seatName)
	if len(arrived) == 0 {
		return ""
	}

	here := mem.checkout(ctx)
	lines := []string{fmt.Sprintf("%s: new for %s (%d):", self, seatName, len(arrived))}

	for _, held := range arrived {
		lines = append(lines, "  "+line(here, held))
	}

	mem.markSeen(seatName, arrived[len(arrived)-1].filed)

	return strings.Join(lines, "\n")
}
