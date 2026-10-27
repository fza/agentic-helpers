package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// mayEdit is whether seatName may retire, retain, retitle or tag the entry:
// every seat on the entry's shelf may, and, once a purge left the entry with an
// expired filer, every seat on a shelf it was filed for.
func (mem *memory) mayEdit(_ *state, held *entry, seatName string) bool {
	if mem.onShelf(held, seatName) {
		return true
	}

	if !mem.filerExpired(held) {
		return false
	}

	mine := mem.shelfOf(seatName)

	return held.forAll || slices.ContainsFunc(held.forKeys, func(key string) bool { return mem.recipientShelf(key) == mine })
}

func (mem *memory) filerExpired(held *entry) bool {
	registry, _ := mem.registry()

	return registry.Seats[held.seatKey].Expired
}

func (mem *memory) notOnShelf(held *entry) string {
	if mem.filerExpired(held) {
		return fmt.Sprintf("%s was filed by %s for other shelves", held.id, held.seat)
	}

	return fmt.Sprintf("%s is on shelf %s", held.id, mem.shelfName(mem.entryShelf(held)))
}

// change loads the log, checks the entry and the acting seat, and appends one
// event. ownerOnly holds an event to the seat that filed the entry.
func (mem *memory) change(ctx context.Context, id string, ownerOnly bool,
	check func(folded *state, held *entry) error, kind memorylog.Kind, target string, payload any,
) (string, *entry, result, bool) {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return "", nil, refused("%s", ErrNoSeat), false
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return "", nil, refused("%v", err), false
	}

	held, found := folded.entries[id]
	if !found {
		return "", nil, refused("%s: %s", id, ErrNoEntry), false
	}

	if ownerOnly && !mem.mayEdit(folded, held, name) {
		return "", nil, refused("%s", mem.notOnShelf(held)), false
	}

	if check != nil {
		err = check(folded, held)
		if err != nil {
			return "", nil, refused("%v", err), false
		}
	}

	raw := []byte("{}")
	if payload != nil {
		raw, err = json.Marshal(payload)
		if err != nil {
			return "", nil, refused("encoding the %s: %v", kind, err), false
		}
	}

	err = mem.appendEvents(ctx, memorylog.Event{
		Kind: kind, Entry: id, Time: mem.now(), Seat: name, Target: target, Payload: raw,
	})
	if err != nil {
		return "", nil, refused("%v", err), false
	}

	return name, held, result{}, true
}

// batch checks every id against one load of the log and lands one event per
// id in a single append, or refuses the whole call naming every id that fails:
// nothing ever lands half.
func (mem *memory) batch(ctx context.Context, verb string, ids []string, ownerOnly bool,
	check func(folded *state, held *entry) error,
	event func(folded *state, held *entry, seat string) (memorylog.Event, error),
) (*state, []string, result, bool) {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return nil, nil, refused("%s", ErrNoSeat), false
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return nil, nil, refused("%v", err), false
	}

	var unique, failures []string

	for _, id := range ids {
		if slices.Contains(unique, id) {
			continue
		}

		unique = append(unique, id)

		held, found := folded.entries[id]

		switch {
		case !found:
			failures = append(failures, fmt.Sprintf("%s: %s", id, ErrNoEntry))
		case ownerOnly && !mem.mayEdit(folded, held, name):
			failures = append(failures, mem.notOnShelf(held))
		case check != nil:
			err = check(folded, held)
			if err != nil {
				failures = append(failures, err.Error())
			}
		}
	}

	if len(failures) > 0 {
		return nil, nil, refused("nothing %s:\n  %s", verb, strings.Join(failures, "\n  ")), false
	}

	events := make([]memorylog.Event, 0, len(unique))

	for _, id := range unique {
		next, err := event(folded, folded.entries[id], name)
		if err != nil {
			return nil, nil, refused("%v", err), false
		}

		events = append(events, next)
	}

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return nil, nil, refused("%v", err), false
	}

	return folded, unique, result{}, true
}

// simpleEvent is an event of kind with payload, on the entry, by the seat.
func (mem *memory) simpleEvent(kind memorylog.Kind, target string, payload any) func(*state, *entry, string) (memorylog.Event, error) {
	return func(_ *state, held *entry, seat string) (memorylog.Event, error) {
		raw := []byte("{}")

		if payload != nil {
			var err error

			raw, err = json.Marshal(payload)
			if err != nil {
				return memorylog.Event{}, fmt.Errorf("encoding the %s: %w", kind, err)
			}
		}

		return memorylog.Event{Kind: kind, Entry: held.id, Time: mem.now(), Seat: seat, Target: target, Payload: raw}, nil
	}
}

func (mem *memory) retire(ctx context.Context, req request) result {
	_, ids, failure, ok := mem.batch(ctx, "retired", req.IDs, true, func(_ *state, held *entry) error {
		if held.status != statusLive {
			return fmt.Errorf("%s is already %s", held.id, held.status)
		}

		return nil
	}, mem.simpleEvent(memorylog.KindRetire, "", retirement(req.Because)))
	if !ok {
		return failure
	}

	return done("retired " + strings.Join(ids, ", ")).with(idsFields{IDs: ids})
}

// retirement is a retire's payload: its reason where one is given, and no
// payload otherwise, as earlier releases wrote every retire.
func retirement(because string) any {
	if strings.TrimSpace(because) == "" {
		return nil
	}

	return retirePayload{Because: strings.TrimSpace(because)}
}

func (mem *memory) supersede(ctx context.Context, req request) result {
	if req.By == "" && req.Because == "" {
		return refused("supersede needs --by <id>, --because '<why>' or both")
	}

	_, ids, failure, ok := mem.batch(ctx, "superseded", req.IDs, false, mem.supersedable(req.By),
		mem.simpleEvent(memorylog.KindSupersede, req.By, supersedePayload{By: req.By, Because: req.Because}))
	if !ok {
		return failure
	}

	return done("superseded " + strings.Join(ids, ", ") + supersession(req.By, req.Because)).
		with(supersededFields{IDs: ids, By: req.By, Because: req.Because})
}

// supersession is what follows "superseded <ids>" on a line: the successor,
// the reason, or both.
func supersession(by, because string) string {
	tail := ""
	if by != "" {
		tail = " by " + by
	}

	if because != "" {
		tail += ": " + because
	}

	return tail
}

// supersedable says why an entry cannot be superseded by successor, if it
// cannot; an empty successor is a supersede --because.
func (mem *memory) supersedable(successor string) func(folded *state, held *entry) error {
	return func(folded *state, held *entry) error {
		if held.status == statusSuperseded {
			return fmt.Errorf("%s is already superseded", held.id)
		}

		if successor == "" {
			return nil
		}

		if successor == held.id {
			return fmt.Errorf("%s cannot supersede itself", held.id)
		}

		_, found := folded.entries[successor]
		if !found {
			return fmt.Errorf("--by %s: %w", successor, ErrNoEntry)
		}

		return nil
	}
}

func (mem *memory) revive(ctx context.Context, req request) result {
	name, _ := mem.seated(ctx)

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(folded *state, held *entry) error {
		last, found := held.latest()
		if !found {
			return fmt.Errorf("%s was never retired or superseded", held.id)
		}

		if last.kind == memorylog.KindRetire && !mem.mayEdit(folded, held, name) {
			return errors.New(mem.notOnShelf(held))
		}

		if held.dropped || (last.kind == memorylog.KindSupersede && mem.now().Sub(last.at) > supersedeKeep) {
			return fmt.Errorf("%s body dropped; file it again", held.id)
		}

		return nil
	}, memorylog.KindRevive, "", nil)
	if !ok {
		return failure
	}

	return done("revived " + req.ID).with(idFields{ID: req.ID})
}

func (mem *memory) retain(ctx context.Context, req request) result {
	if !validRetention(req.Level) {
		return refused("retain takes %s, not %q", strings.Join(retentionLevels, ", "), req.Level)
	}

	_, ids, failure, ok := mem.batch(ctx, "retained", req.IDs, true, nil,
		mem.simpleEvent(memorylog.KindRetain, "", retainPayload{Level: req.Level}))
	if !ok {
		return failure
	}

	return done(fmt.Sprintf("%s retained %s", strings.Join(ids, ", "), req.Level)).
		with(retainedFields{IDs: ids, Retention: req.Level})
}

func (mem *memory) retitle(ctx context.Context, req request) result {
	title := strings.TrimSpace(req.Title)
	_, _, failure, ok := mem.change(ctx, req.ID, true, nil, memorylog.KindRetitle, "", retitlePayload{Title: title})
	if !ok {
		return failure
	}

	text := fmt.Sprintf("%s retitled %q", req.ID, title)

	count := words(title)
	if count < 3 || count > 10 {
		text += fmt.Sprintf("\nnote: title has %s, a title takes 3-10", plural(count, "word"))
	}

	return done(text).with(retitledFields{ID: req.ID, Title: title})
}

func (mem *memory) tag(ctx context.Context, req request) result {
	var topics []string

	for _, topic := range req.Topics {
		topic = normalTopic(topic)
		if topic != "" {
			topics = append(topics, topic)
		}
	}

	if len(topics) == 0 {
		return refused("tag needs at least one topic")
	}

	_, _, failure, ok := mem.change(ctx, req.ID, true, nil, memorylog.KindTag, "", tagPayload{Topics: topics})
	if !ok {
		return failure
	}

	return done(fmt.Sprintf("%s tagged %s", req.ID, strings.Join(topics, ", "))).
		with(topicsFields{ID: req.ID, Topics: topics})
}

// untag removes topics as the entry reads them now, through topic merges: the
// event carries every name the entry was filed or tagged with that reads as
// one of them, so folding it needs no merge.
func (mem *memory) untag(ctx context.Context, req request) result {
	var named []string

	for _, topic := range req.Untagged {
		topic = normalTopic(topic)
		if topic != "" && !slices.Contains(named, topic) {
			named = append(named, topic)
		}
	}

	if len(named) == 0 {
		return refused("untag needs at least one topic")
	}

	removed := &tagPayload{}

	_, _, failure, ok := mem.change(ctx, req.ID, true, func(folded *state, held *entry) error {
		current := folded.topicsOf(held)

		var wanted []string

		for _, topic := range named {
			resolved := folded.resolve(topic)
			if !slices.Contains(current, resolved) {
				return fmt.Errorf("%s carries no topic %s", held.id, topic)
			}

			wanted = append(wanted, resolved)
		}

		if !slices.ContainsFunc(current, func(topic string) bool { return !slices.Contains(wanted, topic) }) {
			return fmt.Errorf("%s keeps at least one topic; tag the right one first: %s tag %s <topic>", held.id, self, held.id)
		}

		for _, topic := range held.topics {
			if slices.Contains(wanted, folded.resolve(topic)) {
				removed.Topics = append(removed.Topics, topic)
			}
		}

		return nil
	}, memorylog.KindUntag, "", removed)
	if !ok {
		return failure
	}

	return done(fmt.Sprintf("%s untagged %s", req.ID, strings.Join(named, ", "))).
		with(topicsFields{ID: req.ID, Topics: named})
}

func (mem *memory) merge(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	from, into := normalTopic(req.FromTopic), normalTopic(req.Into)
	if from == "" || into == "" || from == into {
		return refused("merge takes two different topics: %s merge <from> <into>", self)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	if folded.resolve(into) == from {
		return refused("%s already reads as %s; merging back would loop", into, from)
	}

	if _, known := mem.topicCounts(folded)[folded.resolve(from)]; !known {
		return refused("no live entry carries topic %s", from)
	}

	raw, err := json.Marshal(mergePayload{From: from, Into: into})
	if err != nil {
		return refused("encoding the merge: %v", err)
	}

	err = mem.appendEvents(ctx, memorylog.Event{Kind: memorylog.KindMerge, Time: mem.now(), Seat: name, Payload: raw})
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("topic %s merged into %s", from, into)).with(mergedFields{From: from, Into: into})
}
