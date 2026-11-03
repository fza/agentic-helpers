package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// A todo is a todolist: the steps of one piece of work. A single open task
// is where the seat's work stands, so a todo without items is a state line,
// filed as a note keeping its text and a line on the filing seat's ledger
// carrying its ends and due time.
const todoLineReason = "a todo without items is a state line"

// lineEnds is what ends a state line and when it comes due, as the todo it
// stands for declared them.
type lineEnds struct {
	until   []string
	hashes  map[string]string
	hold    *hold
	session string
	due     time.Time
}

// todoLineText is a todo's line: its title, and its id, so the line ends
// once the note it cites is retired or superseded.
func todoLineText(title string, id string) string {
	return fmt.Sprintf("%s (%s)", title, id)
}

// todoLine is the line a todo without items becomes on the ledger held, its
// id drawn clear of every line the ledger holds and of drawn.
func todoLine(held *ledger, drawn []string, text string, ends lineEnds) (itemPayload, error) {
	id, err := newItemID(func(id string) bool { return id == oldLine || held.line(id) != nil || slices.Contains(drawn, id) })
	if err != nil {
		return itemPayload{}, err
	}

	return itemPayload{
		Op: itemAdd, ID: id, Text: text, Until: ends.until, UntilHashes: ends.hashes, UntilHold: ends.hold,
		Session: ends.session, Due: ends.due,
	}, nil
}

// lineSteps move a format-10 store to format 11: every live todo without
// items becomes a note and a state line (todosToLines).
func (mem *memory) lineSteps(context.Context) ([]formatsteps.Operation, error) {
	return []formatsteps.Operation{{Name: "turning every todo without items into a note and a state line", Apply: mem.todosToLines}}, nil
}

// todosToLines appends, for every live todo without items, a line on the
// ledger of the seat that filed it and a rekind to note moving the todo's
// ends and due time to that line. Nothing is rewritten. A todo whose seat
// already holds its line, left by a run killed between the two rows, gets the
// rekind alone, so a run carrying the step on adds no line twice; a run after
// the rekind landed finds no todo left to turn.
func (mem *memory) todosToLines(ctx context.Context) error {
	// The fold writes no fold cache: a step leaves nothing behind but the
	// rows it appends, wherever a run of it was cut.
	folded, err := mem.foldRead(ctx)
	if err != nil {
		return fmt.Errorf("reading the store to turn its todos without items into state lines: %w", err)
	}

	at := mem.now()
	drawn := map[string][]string{}

	var events []memorylog.Event

	for _, held := range folded.entryList() {
		if held.status != statusLive || held.kind != kindTodo || holdsItems(held) {
			continue
		}

		seatLedger := folded.ledgerFor(held.seat)
		text := todoLineText(held.title, held.id)

		lineID := ""

		for _, line := range seatLedger.Lines {
			if line.Hook == "" && line.Text == text {
				lineID = line.ID

				break
			}
		}

		if lineID == "" {
			line, err := todoLine(seatLedger, drawn[held.seat], text, lineEnds{
				until: held.until, hashes: held.untilHashes, hold: held.untilHold, session: held.session, due: held.due,
			})
			if err != nil {
				return err
			}

			added, err := itemEvents(ledgerID, held.seat, at, []itemPayload{line})
			if err != nil {
				return err
			}

			events = append(events, added...)
			drawn[held.seat] = append(drawn[held.seat], line.ID)
			lineID = line.ID
		}

		raw, err := json.Marshal(rekindPayload{Kind: kindNote, Line: ledgerID + "#" + lineID})
		if err != nil {
			return fmt.Errorf("encoding the rekind of %s: %w", held.id, err)
		}

		events = append(events, memorylog.Event{Kind: memorylog.KindRekind, Entry: held.id, Time: at, Seat: held.seat, Payload: raw})
	}

	if len(events) == 0 {
		return nil
	}

	return mem.appendEvents(ctx, events...)
}

// foldRead folds the log as it stands, writing nothing: no fold cache, no
// log folder where none is yet.
func (mem *memory) foldRead(ctx context.Context) (*state, error) {
	folded := newState()

	_, err := os.Stat(mem.logDir())
	if err == nil {
		log, openErr := mem.openLog()
		if openErr != nil {
			return nil, openErr
		}

		snapshot, snapErr := log.Snapshot(ctx)
		if snapErr != nil {
			return nil, fmt.Errorf("reading the log: %w", snapErr)
		}

		defer func() { _ = snapshot.Close() }()

		folded, err = fold(snapshot, mem.seatName)
		if err != nil {
			return nil, err
		}
	}

	mem.nameEntries(folded)

	return folded, nil
}
