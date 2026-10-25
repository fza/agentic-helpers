package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

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

	if ownerOnly && held.seat != name {
		return "", nil, refused("%s belongs to %s", id, held.seat), false
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

func (mem *memory) retire(ctx context.Context, req request) result {
	var lines []string

	for _, id := range req.IDs {
		_, _, failure, ok := mem.change(ctx, id, true, func(_ *state, held *entry) error {
			if held.status != statusLive {
				return fmt.Errorf("%s is already %s", id, held.status)
			}

			return nil
		}, memorylog.KindRetire, "", nil)
		if !ok {
			return failure
		}

		lines = append(lines, "retired "+id)
	}

	return done(strings.Join(lines, "\n"))
}

func (mem *memory) supersede(ctx context.Context, req request) result {
	if (req.By == "") == (req.Because == "") {
		return refused("supersede takes exactly one of --by <id> or --because '<why>'")
	}

	payload := supersedePayload{By: req.By, Because: req.Because}

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(folded *state, held *entry) error {
		if held.status == statusSuperseded {
			return fmt.Errorf("%s is already superseded", held.id)
		}

		if req.By == "" {
			return nil
		}

		if req.By == held.id {
			return fmt.Errorf("%s cannot supersede itself", held.id)
		}

		_, found := folded.entries[req.By]
		if !found {
			return fmt.Errorf("--by %s: %w", req.By, ErrNoEntry)
		}

		return nil
	}, memorylog.KindSupersede, req.By, payload)
	if !ok {
		return failure
	}

	if req.By != "" {
		return done(fmt.Sprintf("superseded %s by %s", req.ID, req.By))
	}

	return done(fmt.Sprintf("superseded %s: %s", req.ID, req.Because))
}

func (mem *memory) revive(ctx context.Context, req request) result {
	name, _ := mem.seated(ctx)

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(_ *state, held *entry) error {
		last, found := held.latest()
		if !found {
			return fmt.Errorf("%s was never retired or superseded", held.id)
		}

		if last.kind == memorylog.KindRetire && held.seat != name {
			return fmt.Errorf("%s belongs to %s", held.id, held.seat)
		}

		if held.dropped || (last.kind == memorylog.KindSupersede && mem.now().Sub(last.at) > supersedeKeep) {
			return fmt.Errorf("%s body dropped; file it again", held.id)
		}

		return nil
	}, memorylog.KindRevive, "", nil)
	if !ok {
		return failure
	}

	return done("revived " + req.ID)
}

func (mem *memory) retain(ctx context.Context, req request) result {
	if !validRetention(req.Level) {
		return refused("retain takes %s, not %q", strings.Join(retentionLevels, ", "), req.Level)
	}

	_, _, failure, ok := mem.change(ctx, req.ID, true, nil, memorylog.KindRetain, "", retainPayload{Level: req.Level})
	if !ok {
		return failure
	}

	return done(fmt.Sprintf("%s retained %s", req.ID, req.Level))
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

	return done(text)
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

	return done(fmt.Sprintf("%s tagged %s", req.ID, strings.Join(topics, ", ")))
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

	return done(fmt.Sprintf("topic %s merged into %s", from, into))
}
