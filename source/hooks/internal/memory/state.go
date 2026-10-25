package memory

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

const (
	statusLive       = "live"
	statusRetired    = "retired"
	statusSuperseded = "superseded"
)

// supersedeKeep is how long a superseded entry's body stays indexed: a revive
// is possible until a merge drops it.
const supersedeKeep = 14 * 24 * time.Hour

type anchor struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type newPayload struct {
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Topics    []string `json:"topics"`
	Retention string   `json:"retention"`
	From      string   `json:"from"`
	Session   string   `json:"session"`
	Commit    string   `json:"commit"`
	Branch    string   `json:"branch"`
	Anchors   []anchor `json:"anchors"`
	Dropped   bool     `json:"dropped,omitempty"`
}

type supersedePayload struct {
	By      string `json:"by,omitempty"`
	Because string `json:"because,omitempty"`
}

type mergePayload struct {
	From string `json:"from"`
	Into string `json:"into"`
}

type confirmPayload struct {
	Anchors []anchor `json:"anchors"`
	Commit  string   `json:"commit"`
	Branch  string   `json:"branch"`
}

type retainPayload struct {
	Level string `json:"level"`
}

type retitlePayload struct {
	Title string `json:"title"`
}

type tagPayload struct {
	Topics []string `json:"topics"`
}

// transition is one status change, kept so a revive can step back over it.
type transition struct {
	kind    memorylog.Kind
	at      time.Time
	seat    string
	by      string
	because string
}

type change struct {
	at   time.Time
	seat string
	text string
}

type entry struct {
	id        string
	seat      string
	session   string
	filed     time.Time
	title     string
	body      string
	topics    []string
	retention string
	from      string
	commit    string
	branch    string
	anchors   []anchor
	verified  time.Time
	dropped   bool

	status      string
	transitions []transition
	history     []string
	changes     []change
}

func (held *entry) supersededAt() time.Time {
	for _, step := range slices.Backward(held.transitions) {
		if step.kind == memorylog.KindSupersede {
			return step.at
		}
	}

	return time.Time{}
}

func (held *entry) latest() (transition, bool) {
	if len(held.transitions) == 0 {
		return transition{}, false
	}

	return held.transitions[len(held.transitions)-1], true
}

type topicMerge struct {
	from string
	into string
	seat string
	at   time.Time
}

// state is the log folded: every entry with its current status, and every
// topic merge, oldest first.
type state struct {
	entries map[string]*entry
	order   []string
	merges  []topicMerge
}

func normalTopic(topic string) string {
	return strings.ToLower(strings.TrimSpace(topic))
}

// resolve follows merges to the topic a name reads as now.
func (folded *state) resolve(topic string) string {
	topic = normalTopic(topic)

	for range len(folded.merges) + 1 {
		moved := false

		for _, merge := range folded.merges {
			if merge.from == topic && merge.into != topic {
				topic = merge.into
				moved = true
			}
		}

		if !moved {
			break
		}
	}

	return topic
}

func (folded *state) topicsOf(held *entry) []string {
	var topics []string

	for _, topic := range held.topics {
		resolved := folded.resolve(topic)
		if resolved != "" && !slices.Contains(topics, resolved) {
			topics = append(topics, resolved)
		}
	}

	return topics
}

func (folded *state) entryList() []*entry {
	list := make([]*entry, 0, len(folded.order))
	for _, id := range folded.order {
		list = append(list, folded.entries[id])
	}

	return list
}

func fold(snapshot *memorylog.Snapshot) (*state, error) {
	folded := &state{entries: map[string]*entry{}}

	for index, row := range snapshot.Rows() {
		payload, err := snapshot.Payload(index)
		if err != nil {
			return nil, fmt.Errorf("reading the payload of %s %s: %w", row.Kind, row.Entry, err)
		}

		err = folded.apply(row, payload)
		if err != nil {
			return nil, fmt.Errorf("folding %s %s: %w", row.Kind, row.Entry, err)
		}
	}

	return folded, nil
}

func (folded *state) apply(row memorylog.Row, payload []byte) error {
	if row.Kind == memorylog.KindMerge {
		var merge mergePayload

		err := json.Unmarshal(payload, &merge)
		if err != nil {
			return err
		}

		folded.merges = append(folded.merges, topicMerge{
			from: normalTopic(merge.From), into: normalTopic(merge.Into), seat: row.Seat, at: row.Time,
		})

		return nil
	}

	if row.Kind == memorylog.KindNew {
		var filed newPayload

		err := json.Unmarshal(payload, &filed)
		if err != nil {
			return err
		}

		folded.entries[row.Entry] = &entry{
			id: row.Entry, seat: row.Seat, session: filed.Session, filed: row.Time, title: filed.Title,
			body: filed.Body, topics: filed.Topics, retention: filed.Retention, from: filed.From,
			commit: filed.Commit, branch: filed.Branch, anchors: filed.Anchors, dropped: filed.Dropped,
			status: statusLive,
		}
		folded.order = append(folded.order, row.Entry)

		return nil
	}

	held, found := folded.entries[row.Entry]
	if !found {
		return nil
	}

	return held.apply(row, payload)
}

func (held *entry) apply(row memorylog.Row, payload []byte) error {
	other := row.Seat != held.seat
	note := func(text string) {
		held.history = append(held.history, fmt.Sprintf("%s %s by %s", stamp(row.Time), text, row.Seat))
		if other {
			held.changes = append(held.changes, change{at: row.Time, seat: row.Seat, text: text})
		}
	}

	switch row.Kind {
	case memorylog.KindRetire:
		held.status = statusRetired
		held.transitions = append(held.transitions, transition{kind: row.Kind, at: row.Time, seat: row.Seat})
		note("retired")
	case memorylog.KindSupersede:
		var superseded supersedePayload

		err := json.Unmarshal(payload, &superseded)
		if err != nil {
			return err
		}

		held.status = statusSuperseded
		held.transitions = append(held.transitions, transition{
			kind: row.Kind, at: row.Time, seat: row.Seat, by: superseded.By, because: superseded.Because,
		})

		if superseded.By != "" {
			note("superseded by " + superseded.By)
		} else {
			note("superseded: " + superseded.Because)
		}
	case memorylog.KindRevive:
		if len(held.transitions) > 0 {
			held.transitions = held.transitions[:len(held.transitions)-1]
		}

		held.status = statusLive

		last, found := held.latest()
		if found {
			held.status = map[memorylog.Kind]string{
				memorylog.KindRetire: statusRetired, memorylog.KindSupersede: statusSuperseded,
			}[last.kind]
		}

		note("revived")
	case memorylog.KindConfirm:
		var confirmed confirmPayload

		err := json.Unmarshal(payload, &confirmed)
		if err != nil {
			return err
		}

		held.anchors = confirmed.Anchors
		held.commit = confirmed.Commit
		held.branch = confirmed.Branch
		held.verified = row.Time
		note("confirmed")
	case memorylog.KindRetain:
		var retained retainPayload

		err := json.Unmarshal(payload, &retained)
		if err != nil {
			return err
		}

		held.retention = retained.Level
		note("retention set to " + retained.Level)
	case memorylog.KindRetitle:
		var retitled retitlePayload

		err := json.Unmarshal(payload, &retitled)
		if err != nil {
			return err
		}

		held.title = retitled.Title
		note("retitled")
	case memorylog.KindTag:
		var tagged tagPayload

		err := json.Unmarshal(payload, &tagged)
		if err != nil {
			return err
		}

		for _, topic := range tagged.Topics {
			if !slices.Contains(held.topics, topic) {
				held.topics = append(held.topics, topic)
			}
		}

		note("tagged " + strings.Join(tagged.Topics, ", "))
	}

	return nil
}

// shadowed reports whether an entry never shows in a search again: a successor
// stands in for it.
func (held *entry) shadowed() bool {
	if held.status != statusSuperseded {
		return false
	}

	last, _ := held.latest()

	return last.by != ""
}

// mergePolicy keeps every event, stripping the body of each entry superseded
// longer ago than supersedeKeep: the one place a body is ever dropped.
func mergePolicy(now time.Time) memorylog.Policy {
	return func(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
		folded := &state{entries: map[string]*entry{}}
		payloads := make([][]byte, len(rows))

		for index, row := range rows {
			raw, err := payload(index)
			if err != nil {
				return nil, err
			}

			payloads[index] = raw

			err = folded.apply(row, raw)
			if err != nil {
				return nil, err
			}
		}

		kept := make([]memorylog.Event, 0, len(rows))

		for index, row := range rows {
			raw := payloads[index]

			held := folded.entries[row.Entry]
			if row.Kind == memorylog.KindNew && held != nil && held.status == statusSuperseded &&
				now.Sub(held.supersededAt()) > supersedeKeep {
				var filed newPayload

				err := json.Unmarshal(raw, &filed)
				if err != nil {
					return nil, err
				}

				filed.Body = ""
				filed.Dropped = true

				raw, err = json.Marshal(filed)
				if err != nil {
					return nil, err
				}
			}

			kept = append(kept, memorylog.Event{
				Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: raw,
			})
		}

		return kept, nil
	}
}
