package memory

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// A todolist's items are records in the log beside the todo, never lines of
// its body: ticking one changes a few bytes where a body would be superseded
// whole, and an item keeps its id however the list is re-sorted.
const (
	itemAdd     = "add"
	itemCheck   = "check"
	itemUncheck = "uncheck"
	itemDrop    = "drop"
	itemRemove  = "remove"
	itemOrder   = "order"

	itemOpen    = "open"
	itemDone    = "done"
	itemDropped = "dropped"
	itemRemoved = "removed"

	itemsCarry = "carry"
	itemsLeave = "leave"
	// formItems is the end a finished todolist reaches.
	formItems = "items"

	itemIDLength   = 3
	itemTextCut    = 60
	briefOpenItems = 3
	// chainHops bounds how far an item follows a supersede chain, against a
	// cycle a corrupt log could hold.
	chainHops = 16
)

var (
	ErrItemKind     = errors.New("--item files a todolist, which only a todo holds")
	ErrItemsUnnamed = errors.New("holds a todolist; pass --items carry or --items leave")
	itemAddress     = regexp.MustCompile(`^(m-[a-z0-9]{4,16})#([a-z0-9]+)$`)
	memoryID        = regexp.MustCompile(`^m-[a-z0-9]{4,16}$`)
	// listLine is a body line written as a list item: `- `, `* `, `1. `, `- [ ] `.
	listLine = regexp.MustCompile(`(?m)^\s*(?:[-*]|\d+[.)])\s+\S`)
)

// itemPayload is one change to a todolist. A carried item's add brings its
// state, reason and the address it came from.
type itemPayload struct {
	Op     string   `json:"op"`
	ID     string   `json:"id,omitempty"`
	Text   string   `json:"text,omitempty"`
	Link   string   `json:"link,omitempty"`
	State  string   `json:"state,omitempty"`
	Reason string   `json:"reason,omitempty"`
	From   string   `json:"from,omitempty"`
	Order  []string `json:"order,omitempty"`
}

// todoItem is one item as the log left it. Set is whether an ack gave it its
// state, which wins over what a linked item's target says.
type todoItem struct {
	ID, Text, Link, State, Reason, From string
	Set                                 bool
}

func (held *entry) item(id string) *todoItem {
	for index := range held.items {
		if held.items[index].ID == id {
			return &held.items[index]
		}
	}

	return nil
}

func (held *entry) applyItem(change itemPayload) string {
	if change.Op == itemOrder {
		held.items = reordered(held.items, change.Order)

		return "items reordered"
	}

	if change.Op == itemAdd {
		if held.item(change.ID) != nil {
			return ""
		}

		state := cmp.Or(change.State, itemOpen)
		held.items = append(held.items, todoItem{
			ID: change.ID, Text: change.Text, Link: change.Link, State: state, Reason: change.Reason, From: change.From,
			Set: state != itemOpen,
		})

		return "item #" + change.ID + " added"
	}

	target := held.item(change.ID)
	if target == nil {
		return ""
	}

	switch change.Op {
	case itemCheck:
		target.State, target.Reason, target.Set = itemDone, "", true

		return "item #" + change.ID + " done"
	case itemUncheck:
		target.State, target.Reason, target.Set = itemOpen, "", false

		return "item #" + change.ID + " undone"
	case itemDrop:
		target.State, target.Reason, target.Set = itemDropped, change.Reason, true

		return "item #" + change.ID + " dropped: " + change.Reason
	case itemRemove:
		target.State, target.Set = itemRemoved, true

		return "item #" + change.ID + " removed"
	}

	return ""
}

// reordered puts the named items first, in the order named; every other item
// keeps its place relative to the rest behind them.
func reordered(items []todoItem, order []string) []todoItem {
	first := make([]todoItem, 0, len(items))
	rest := make([]todoItem, 0, len(items))

	for _, id := range order {
		for _, one := range items {
			if one.ID == id && !slices.ContainsFunc(first, func(taken todoItem) bool { return taken.ID == id }) {
				first = append(first, one)
			}
		}
	}

	for _, one := range items {
		if !slices.Contains(order, one.ID) {
			rest = append(rest, one)
		}
	}

	return append(first, rest...)
}

// newItemID is three lowercase base36 characters no item of taken holds,
// removed ones included, never all digits so it is never read as a position.
func newItemID(taken func(string) bool) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	for range 1000 {
		raw := make([]byte, itemIDLength)

		_, err := rand.Read(raw)
		if err != nil {
			return "", fmt.Errorf("drawing an item id: %w", err)
		}

		id := make([]byte, itemIDLength)
		for index, value := range raw {
			id[index] = alphabet[int(value)%len(alphabet)]
		}

		candidate := string(id)
		if strings.Trim(candidate, "0123456789") != "" && !taken(candidate) {
			return candidate, nil
		}
	}

	return "", errors.New("no free item id left in this todolist")
}

// itemOf is an item argument as a payload: a memory or graph id links, any
// other text is the item.
func itemOf(text string) itemPayload {
	text = strings.TrimSpace(text)

	_, _, qualified := splitQualified(text)
	if memoryID.MatchString(text) || isGraphID(text) || qualified {
		return itemPayload{Op: itemAdd, Link: text}
	}

	return itemPayload{Op: itemAdd, Text: text}
}

// addItems gives each of texts a fresh id beside held's items, in order.
func addItems(held *entry, texts []string) ([]itemPayload, error) {
	var added []itemPayload

	taken := func(id string) bool {
		return (held != nil && held.item(id) != nil) ||
			slices.ContainsFunc(added, func(one itemPayload) bool { return one.ID == id })
	}

	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}

		id, err := newItemID(taken)
		if err != nil {
			return nil, err
		}

		one := itemOf(text)
		one.ID = id
		added = append(added, one)
	}

	return added, nil
}

// carried is from's items, removed ones aside, as adds onto a successor: each
// keeps its id where taken does not hold it, else gets a new one, and records
// the address it came from, through which `<old>#<id>` still resolves.
func carried(from *entry, taken func(string) bool) ([]itemPayload, []string, error) {
	var (
		moved   []itemPayload
		renamed []string
	)

	holds := func(id string) bool {
		return taken(id) || slices.ContainsFunc(moved, func(one itemPayload) bool { return one.ID == id })
	}

	for _, one := range from.items {
		if one.State == itemRemoved {
			continue
		}

		id := one.ID
		if holds(id) {
			fresh, err := newItemID(holds)
			if err != nil {
				return nil, nil, err
			}

			renamed = append(renamed, fmt.Sprintf("%s#%s is now #%s", from.id, id, fresh))
			id = fresh
		}

		state := one.State
		if !one.Set {
			state = itemOpen
		}

		moved = append(moved, itemPayload{
			Op: itemAdd, ID: id, Text: one.Text, Link: one.Link, State: state, Reason: one.Reason, From: from.id + "#" + one.ID,
		})
	}

	return moved, renamed, nil
}

func itemsOf(changes []itemPayload) []todoItem {
	items := make([]todoItem, 0, len(changes))
	for _, change := range changes {
		items = append(items, todoItem{ID: change.ID})
	}

	return items
}

// holdsItems is whether held keeps a todolist a supersede has to carry or
// leave.
func holdsItems(held *entry) bool {
	return slices.ContainsFunc(held.items, func(one todoItem) bool { return one.State != itemRemoved })
}

func itemsModeRefusal(mode string) string {
	if mode != "" && mode != itemsCarry && mode != itemsLeave {
		return fmt.Sprintf("--items takes carry or leave, not %q", mode)
	}

	return ""
}

func itemEvents(id string, seat string, at time.Time, changes []itemPayload) ([]memorylog.Event, error) {
	events := make([]memorylog.Event, 0, len(changes))

	for _, change := range changes {
		raw, err := json.Marshal(change)
		if err != nil {
			return nil, fmt.Errorf("encoding an item: %w", err)
		}

		events = append(events, memorylog.Event{Kind: memorylog.KindItem, Entry: id, Time: at, Seat: seat, Payload: raw})
	}

	return events, nil
}

// itemView is one item as a read shows it: its state worked out, its text,
// and a linked item's flag.
type itemView struct {
	position int
	item     todoItem
	state    string
	text     string
	flag     string
}

// itemState is an item's state now: an ack's tick, drop or remove wins; else
// a linked item is done once what it names ended.
func (folded *state) itemState(one todoItem) (string, string) {
	if one.Link == "" {
		return one.State, ""
	}

	ended, flag := false, "→ "+one.Link
	if folded != nil && folded.itemLinks != nil {
		ended, flag = folded.itemLinks(one.Link)
	}

	if one.Set {
		return one.State, flag
	}

	if ended {
		return itemDone, flag
	}

	return itemOpen, flag
}

// itemViews is held's items in order, removed ones left out unless asked for,
// with the count of done or dropped items and of all kept.
func (folded *state) itemViews(held *entry, withRemoved bool) ([]itemView, int, int) {
	var views []itemView

	settled, total := 0, 0

	for _, one := range held.items {
		state, flag := folded.itemState(one)
		if state == itemRemoved && !withRemoved {
			continue
		}

		text := one.Text
		if one.Link != "" {
			text = one.Link

			if folded != nil {
				linked, found := folded.entries[one.Link]
				if found {
					text = linked.title
				}
			}
		}

		if state != itemRemoved {
			total++

			if state == itemDone || state == itemDropped {
				settled++
			}
		}

		views = append(views, itemView{position: len(views) + 1, item: one, state: state, text: text, flag: flag})
	}

	return views, settled, total
}

func cut(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}

	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// briefItems is a todo's count, for its short line, and its next open items
// as indented lines; nothing for a todo without a todolist.
func (folded *state) briefItems(held *entry, indent string) (string, []string) {
	views, settled, total := folded.itemViews(held, false)
	if total == 0 {
		return "", nil
	}

	var lines []string

	for _, view := range views {
		if view.state != itemOpen || len(lines) == briefOpenItems {
			continue
		}

		lines = append(lines, fmt.Sprintf("%s%d. #%s: %s", indent, view.position, view.item.ID, cut(view.text, itemTextCut)))
	}

	return fmt.Sprintf("%d/%d", settled, total), lines
}

// showItems is every item of held's todolist, removed ones only where asked.
func (folded *state) showItems(held *entry, withRemoved bool) []string {
	views, settled, total := folded.itemViews(held, withRemoved)
	if len(views) == 0 {
		return nil
	}

	lines := []string{fmt.Sprintf("items %d/%d:", settled, total)}

	for _, view := range views {
		box := map[string]string{itemOpen: "[ ]", itemDone: "[x]", itemDropped: "[-]", itemRemoved: "[r]"}[view.state]

		line := fmt.Sprintf("  %d. %s #%s: %s", view.position, box, view.item.ID, view.text)
		if view.flag != "" {
			line += "  " + view.flag
		}

		lines = append(lines, line)

		if view.state == itemDropped && view.item.Reason != "" {
			lines = append(lines, "    dropped: "+view.item.Reason)
		}
	}

	return lines
}

// itemsFinished is whether every item of a todolist is done or dropped, with
// the count, for the end that retires it. A revive after the last item change
// keeps the todo: the list finished before it, and only a change to an item
// ends it again.
func (folded *state) itemsFinished(held *entry) (bool, int) {
	if len(held.items) == 0 {
		return false, 0
	}

	if !held.revived.IsZero() && !held.revived.Before(held.itemsChanged) {
		return false, 0
	}

	_, settled, total := folded.itemViews(held, false)

	return total > 0 && settled == total, total
}

// listLines counts a body's lines written as list items: steps that belong in
// --item, which the filing reply names.
func listLines(body string) int {
	return len(listLine.FindAllString(body, -1))
}

// ackItems changes a todo's items for good: ticks, unticks, drops or removes
// the items addressed `<todo>#<id>`, or adds or re-sorts the items of one todo.
// Every address is checked before anything lands.
func (mem *memory) ackItems(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	because := strings.TrimSpace(req.Decline)

	changes := 0
	for _, given := range []bool{req.Done, because != "", req.Undo, req.Remove, len(req.Add) > 0, req.Order != ""} {
		if given {
			changes++
		}
	}

	if changes != 1 {
		return refused("ack on items takes exactly one of --done, --undo, --decline '<why>', --remove, --add or --order")
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	if len(req.Add) > 0 || req.Order != "" {
		return mem.listChange(ctx, folded, name, req)
	}

	op := map[bool]string{true: itemCheck}[req.Done]
	switch {
	case req.Undo:
		op = itemUncheck
	case because != "":
		op = itemDrop
	case req.Remove:
		op = itemRemove
	}

	var (
		failures []string
		todos    []string
	)

	byTodo := map[string][]itemPayload{}

	for _, address := range slices.Compact(slices.Clone(req.IDs)) {
		held, id, err := mem.resolveItem(folded, name, address)
		if err != nil {
			failures = append(failures, err.Error())

			continue
		}

		if !slices.Contains(todos, held.id) {
			todos = append(todos, held.id)
		}

		byTodo[held.id] = append(byTodo[held.id], itemPayload{Op: op, ID: id, Reason: because})
	}

	if len(failures) > 0 {
		return refused("nothing acked:\n  %s", strings.Join(failures, "\n  "))
	}

	var events []memorylog.Event

	for _, todo := range todos {
		more, err := itemEvents(todo, name, mem.now(), byTodo[todo])
		if err != nil {
			return refused("%v", err)
		}

		events = append(events, more...)
	}

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return refused("%v", err)
	}

	verb := map[string]string{itemCheck: "done", itemUncheck: "undone", itemDrop: "dropped", itemRemove: "removed"}[op]
	lines := append([]string{verb}, mem.itemEnds(ctx, todos, name)...)

	return done(strings.Join(lines, "\n"))
}

// listChange adds items to one todo's end, or puts the named items first.
func (mem *memory) listChange(ctx context.Context, folded *state, name string, req request) result {
	if len(req.IDs) != 1 || strings.Contains(req.IDs[0], "#") {
		return refused("--add and --order take one todo id, no item address")
	}

	held, err := mem.itemTodo(folded, name, req.IDs[0])
	if err != nil {
		return refused("nothing acked:\n  %v", err)
	}

	var changes []itemPayload

	if len(req.Add) > 0 {
		changes, err = addItems(held, req.Add)
		if err != nil {
			return refused("%v", err)
		}
	} else {
		order := strings.Split(req.Order, ",")

		var unknown []string

		for index, id := range order {
			order[index] = strings.TrimPrefix(strings.TrimSpace(id), "#")

			found := held.item(order[index])
			if found == nil || found.State == itemRemoved {
				unknown = append(unknown, fmt.Sprintf("%s#%s: no such item", held.id, order[index]))
			}
		}

		if len(unknown) > 0 {
			return refused("nothing acked:\n  %s", strings.Join(unknown, "\n  "))
		}

		changes = []itemPayload{{Op: itemOrder, Order: order}}
	}

	events, err := itemEvents(held.id, name, mem.now(), changes)
	if err != nil {
		return refused("%v", err)
	}

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return refused("%v", err)
	}

	if len(req.Add) > 0 {
		var ids []string
		for _, change := range changes {
			ids = append(ids, held.id+"#"+change.ID)
		}

		return done("added " + strings.Join(ids, ", "))
	}

	return done("reordered")
}

// itemTodo is the todo id names, where the seat may change its items.
func (mem *memory) itemTodo(folded *state, name string, id string) (*entry, error) {
	held, found := folded.entries[id]

	switch {
	case !found:
		return nil, fmt.Errorf("%s: %w", id, ErrNoEntry)
	case held.kind != kindTodo:
		return nil, fmt.Errorf("%s is a %s; items belong to a todo", id, held.kind)
	case !mem.mayEdit(folded, held, name):
		return nil, errors.New(mem.notOnShelf(held))
	}

	return held, nil
}

// resolveItem is the todo and item id an address names, following the
// supersede chain where the list was carried on.
func (mem *memory) resolveItem(folded *state, name string, address string) (*entry, string, error) {
	parts := itemAddress.FindStringSubmatch(address)
	if parts == nil {
		return nil, "", fmt.Errorf("%s is no item address; give <todo>#<item>", address)
	}

	if strings.Trim(parts[2], "0123456789") == "" {
		return nil, "", fmt.Errorf("%s: #%s is a position; address an item by its id, which show <todo> lists", address, parts[2])
	}

	held, err := mem.itemTodo(folded, name, parts[1])
	if err != nil {
		return nil, "", err
	}

	id := parts[2]

	for range chainHops {
		last, _ := held.latest()
		if held.status != statusSuperseded || last.by == "" {
			break
		}

		successor, found := folded.entries[last.by]
		if !found {
			break
		}

		from := held.id + "#" + id
		index := slices.IndexFunc(successor.items, func(one todoItem) bool { return one.From == from })

		if index < 0 {
			break
		}

		held, id = successor, successor.items[index].ID
	}

	// The list may have moved onto a todo of another shelf, which this seat
	// may not change.
	if !mem.mayEdit(folded, held, name) {
		return nil, "", errors.New(mem.notOnShelf(held))
	}

	one := held.item(id)
	if one == nil || one.State == itemRemoved {
		return nil, "", fmt.Errorf("%s: no such item", address)
	}

	return held, id, nil
}

// itemEnds retires each of todos whose every item is now done or dropped.
func (mem *memory) itemEnds(ctx context.Context, todos []string, name string) []string {
	folded, err := mem.load(ctx)
	if err != nil {
		return nil
	}

	var lines []string

	for _, todo := range todos {
		lines = append(lines, mem.reachEnds(ctx, folded, name, todo, formItems)...)
	}

	return lines
}
