package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// The folded log is cached in the cache directory, keyed by everything the
// fold read: the stamp of every sealed segment, the tail rows it took in and
// their digest, the seat registry that named the seats, and the zone its
// stamps were printed in. A call whose log only grew in its tail folds the
// rows appended since onto the cached fold; any other difference folds the
// whole log again. Any sandboxed job can write the cache as it can the store,
// so a key that does not match costs a fold, never a wrong answer.

// foldLayout changes whenever the cached shape does, so a cache of another
// release's shape is folded again rather than misread.
const foldLayout = 4

type foldKey struct {
	Layout     int
	Version    string
	Segments   []memorylog.SegmentStamp
	TailRows   int
	TailDigest string
	Seats      string
	Zone       string
}

type cachedFold struct {
	Key     foldKey
	Entries []cachedEntry
	Merges  []cachedMerge
	States  map[string]cachedSlot
}

type cachedChange struct {
	At   time.Time
	Seat string
	Text string
	Kind memorylog.Kind
}

type cachedTransition struct {
	Kind    memorylog.Kind
	At      time.Time
	Seat    string
	By      string
	Because string
}

type cachedLink struct {
	Target string
	Seat   string
}

type cachedMerge struct {
	From string
	Into string
	Seat string
	At   time.Time
}

type cachedSlot struct {
	Payload statePayload
	At      time.Time
}

type cachedEntry struct {
	ID, Seat, SeatKey, Session             string
	Filed, Verified                        time.Time
	Title, Body, Kind, Retention, Override string
	From, Commit, Branch, Status           string
	Topics, History, ForKeys, TextLinks    []string
	Until, When, On, Proof, Forbids, About []string
	Anchors                                []anchor
	Dropped, ForAll                        bool
	Transitions                            []cachedTransition
	Changes, Events                        []cachedChange
	Explicit                               []cachedLink
	UntilHashes                            map[string]string
	UntilHold                              *hold
	Attachments                            []attachPayload
	Items                                  []todoItem
	ItemsChanged, Revived                  time.Time
}

func (mem *memory) foldCachePath() string {
	return filepath.Join(mem.cacheDir(), "fold.gob")
}

// foldKeyFor is the key of a fold of the given segments and tail rows, as this
// release, this seat registry and this zone fold them.
func (mem *memory) foldKeyFor(segments []memorylog.SegmentStamp, tail []memorylog.Event) foldKey {
	seats, _ := os.ReadFile(filepath.Join(mem.store(), "seats.json"))
	seatsSum := sha256.Sum256(seats)
	return foldKey{
		Layout: foldLayout, Version: mem.env.Version, Segments: segments, TailRows: len(tail),
		TailDigest: tailDigest(tail), Seats: hex.EncodeToString(seatsSum[:]), Zone: localZone(),
	}
}

// localZone names the zone a fold prints its stamps in, by its winter and its
// summer abbreviation and offset, so a machine moved to another zone folds
// again.
func localZone() string {
	winter, winterOffset := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.Local).Zone()
	summer, summerOffset := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.Local).Zone()

	return fmt.Sprintf("%s%+d/%s%+d", winter, winterOffset, summer, summerOffset)
}

// tailDigest is a digest of every field of every row, each length-prefixed so
// no two rows spell the same bytes.
func tailDigest(tail []memorylog.Event) string {
	digest := sha256.New()

	write := func(field []byte) {
		_ = binary.Write(digest, binary.BigEndian, int64(len(field)))
		_, _ = digest.Write(field)
	}

	for _, event := range tail {
		write([]byte(event.Kind))
		write([]byte(event.Entry))
		write([]byte(event.Time.UTC().Format(time.RFC3339Nano)))
		write([]byte(event.Seat))
		write([]byte(event.Target))
		write(event.Payload)
	}

	return hex.EncodeToString(digest.Sum(nil))
}

// sameStatic is whether the parts of two keys the log's own growth leaves as
// they were match.
func (key foldKey) sameStatic(other foldKey) bool {
	return key.Layout == other.Layout && key.Version == other.Version && key.Seats == other.Seats &&
		key.Zone == other.Zone && slices.Equal(key.Segments, other.Segments)
}

// foldLog folds the log, from the cached fold where the log only grew in its
// tail since, and keeps the fold it ends with for the next call.
func (mem *memory) foldLog(ctx context.Context, log *memorylog.Log) (*state, error) {
	cached, readable := mem.readFold()
	if readable {
		segments, tail, err := log.Unsealed(ctx)
		if err == nil {
			now := mem.foldKeyFor(segments, tail[:min(cached.Key.TailRows, len(tail))])
			if now.sameStatic(cached.Key) && now.TailRows == cached.Key.TailRows && now.TailDigest == cached.Key.TailDigest {
				folded := cached.state()

				for _, event := range tail[cached.Key.TailRows:] {
					row := memorylog.Row{Kind: event.Kind, Entry: event.Entry, Time: event.Time, Seat: event.Seat, Target: event.Target}

					err = folded.take(row, event.Payload, mem.seatName)
					if err != nil {
						return nil, fmt.Errorf("folding %s %s: %w", row.Kind, row.Entry, err)
					}
				}

				if len(tail) > cached.Key.TailRows {
					mem.writeFold(mem.foldKeyFor(segments, tail), folded)
				}

				return folded, nil
			}
		}
	}

	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the log: %w", err)
	}

	defer func() { _ = snapshot.Close() }()

	folded, err := fold(snapshot, mem.seatName)
	if err != nil {
		return nil, err
	}

	tail := make([]memorylog.Event, 0, len(snapshot.Rows())-snapshot.Sealed())

	for index, row := range snapshot.Rows()[snapshot.Sealed():] {
		payload, err := snapshot.Payload(snapshot.Sealed() + index)
		if err != nil {
			return folded, nil
		}

		tail = append(tail, memorylog.Event{Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat,
			Target: row.Target, Payload: payload})
	}

	mem.writeFold(mem.foldKeyFor(snapshot.Segments(), tail), folded)

	return folded, nil
}

func (mem *memory) readFold() (cachedFold, bool) {
	data, err := os.ReadFile(mem.foldCachePath())
	if err != nil {
		return cachedFold{}, false
	}

	var cached cachedFold

	err = gob.NewDecoder(bytes.NewReader(data)).Decode(&cached)
	if err != nil || cached.Key.Layout != foldLayout {
		return cachedFold{}, false
	}

	return cached, true
}

// writeFold keeps the fold for the next call. The cache may be lost at any
// time, so a failure to write it costs a fold, never memory.
func (mem *memory) writeFold(key foldKey, folded *state) {
	var data bytes.Buffer

	err := gob.NewEncoder(&data).Encode(cacheOf(key, folded))
	if err != nil {
		return
	}

	err = os.MkdirAll(mem.cacheDir(), 0o700)
	if err != nil {
		return
	}

	_ = atomicfile.Write(mem.foldCachePath(), data.Bytes(), 0o600)
}

func cacheOf(key foldKey, folded *state) cachedFold {
	cached := cachedFold{Key: key, Entries: make([]cachedEntry, 0, len(folded.order)), States: map[string]cachedSlot{}}

	for _, merge := range folded.merges {
		cached.Merges = append(cached.Merges, cachedMerge{From: merge.from, Into: merge.into, Seat: merge.seat, At: merge.at})
	}

	for name, slot := range folded.states {
		cached.States[name] = cachedSlot{Payload: slot.statePayload, At: slot.at}
	}

	for _, id := range folded.order {
		held := folded.entries[id]
		one := cachedEntry{
			ID: held.id, Seat: held.seat, SeatKey: held.seatKey, Session: held.session, Filed: held.filed,
			Verified: held.verified, Title: held.title, Body: held.body, Kind: held.kind, Retention: held.retention,
			Override: held.override, From: held.from, Commit: held.commit, Branch: held.branch, Status: held.status,
			Topics: held.topics, History: held.history, ForKeys: held.forKeys, TextLinks: held.textLinks,
			Until: held.until, When: held.when, On: held.on, Proof: held.proof, Forbids: held.forbids, About: held.about,
			Anchors: held.anchors, Dropped: held.dropped, ForAll: held.forAll, UntilHashes: held.untilHashes,
			UntilHold: held.untilHold, Attachments: held.attachments, Items: held.items, ItemsChanged: held.itemsChanged, Revived: held.revived,
		}

		for _, step := range held.transitions {
			one.Transitions = append(one.Transitions, cachedTransition{Kind: step.kind, At: step.at, Seat: step.seat,
				By: step.by, Because: step.because})
		}

		for _, made := range held.changes {
			one.Changes = append(one.Changes, cachedChange{At: made.at, Seat: made.seat, Text: made.text, Kind: made.kind})
		}

		for _, made := range held.events {
			one.Events = append(one.Events, cachedChange{At: made.at, Seat: made.seat, Text: made.text, Kind: made.kind})
		}

		for _, link := range held.explicit {
			one.Explicit = append(one.Explicit, cachedLink{Target: link.target, Seat: link.seat})
		}

		cached.Entries = append(cached.Entries, one)
	}

	return cached
}

func (cached cachedFold) state() *state {
	folded := newState()

	for _, merge := range cached.Merges {
		folded.merges = append(folded.merges, topicMerge{from: merge.From, into: merge.Into, seat: merge.Seat, at: merge.At})
	}

	for name, slot := range cached.States {
		folded.states[name] = stateSlot{statePayload: slot.Payload, at: slot.At}
	}

	for _, one := range cached.Entries {
		held := &entry{
			id: one.ID, seat: one.Seat, seatKey: one.SeatKey, session: one.Session, filed: one.Filed,
			verified: one.Verified, title: one.Title, body: one.Body, kind: one.Kind, retention: one.Retention,
			override: one.Override, from: one.From, commit: one.Commit, branch: one.Branch, status: one.Status,
			topics: one.Topics, history: one.History, forKeys: one.ForKeys, textLinks: one.TextLinks,
			until: one.Until, when: one.When, on: one.On, proof: one.Proof, forbids: one.Forbids, about: one.About,
			anchors: one.Anchors, dropped: one.Dropped, forAll: one.ForAll, untilHashes: one.UntilHashes,
			untilHold: one.UntilHold, attachments: one.Attachments, items: one.Items, itemsChanged: one.ItemsChanged, revived: one.Revived,
		}

		for _, step := range one.Transitions {
			held.transitions = append(held.transitions, transition{kind: step.Kind, at: step.At, seat: step.Seat,
				by: step.By, because: step.Because})
		}

		for _, made := range one.Changes {
			held.changes = append(held.changes, change{at: made.At, seat: made.Seat, text: made.Text, kind: made.Kind})
		}

		for _, made := range one.Events {
			held.events = append(held.events, change{at: made.At, seat: made.Seat, text: made.Text, kind: made.Kind})
		}

		for _, link := range one.Explicit {
			held.explicit = append(held.explicit, explicitLink{target: link.Target, seat: link.Seat})
		}

		folded.entries[held.id] = held
		folded.order = append(folded.order, held.id)
	}

	return folded
}
