package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

// A checkout works on the store the home maps it to (internal/home). One
// matching none gets a store of its own, silently, at its first call that
// writes; reads and hooks create nothing. Where the mapping is the user's
// call, every call but those answering anywhere is refused with the question
// until `bind` answers it.

// readOnly are the calls that answer anywhere, a question standing or not;
// reads change no store either, and create none.
var (
	readOnly = []string{"help", "--help", "-h", "who", "holder", "bind"}
	reads    = []string{"search", "show", "brief", "topics", "view", "dump", "mcp"}
)

var ErrUnbound = errors.New("this checkout is bound to no store")

// Terminal says whether the call's standard input is a terminal. An agent's
// Bash tool and MCP calls hand over none, but an agent can open one (`script`,
// a pty), so a terminal counts only outside Claude Code.
type Terminal interface {
	Interactive() bool
}

// An agent's `bind <store>` and `merge <a> <b>` reach only the stores the
// open question for the calling checkout offers, so a committed id or a
// prompt cannot attach a checkout to another project's data; the user at a
// terminal runs any. `bind --new` reaches no data and always runs.

// interactive says the user runs the call at a shell: a terminal on standard
// input, and no Claude Code process (the owner the seats match) among the
// caller's ancestors, so a pty an agent opens, and a `!` command, count as
// the agent's. It is a guardrail against a bind made by accident or by an
// injected instruction, not a security boundary: an agent running as the
// user can still detach from its process tree, or write the store pointer
// itself.
func (mem *memory) interactive(ctx context.Context) bool {
	return mem.env.Terminal != nil && mem.env.Terminal.Interactive() &&
		!seat.UnderOwner(ctx, mem.env.Processes, mem.env.OwnerName)
}

// bindRefusal is why an agent may not bind the checkout to target, empty
// where the open question offers it.
func (mem *memory) bindRefusal(target string) string {
	if mem.question.Offers(target) {
		return ""
	}

	return notOffered(target, "bind")
}

// mergeRefusal is why an agent may not join from and into, empty where one
// is the checkout's own store and the open question offers the other. It
// names the side that is neither.
func (mem *memory) mergeRefusal(from string, into string) string {
	if from == mem.key && mem.question.Offers(into) || into == mem.key && mem.question.Offers(from) {
		return ""
	}

	switch {
	case from == mem.key:
		return notOffered(into, "merge")
	case into == mem.key:
		return notOffered(from, "merge")
	case !mem.question.Offers(from):
		return notOffered(from, "merge")
	case !mem.question.Offers(into):
		return notOffered(into, "merge")
	}

	return fmt.Sprintf("neither %s nor %s is this checkout's own store; run the merge in a terminal", from, into)
}

func notOffered(store string, verb string) string {
	return fmt.Sprintf("%s is not offered for this checkout; run the %s in a terminal", store, verb)
}

// admission creates the checkout's store where none matched it yet and call
// is one that writes.
func (mem *memory) admission(ctx context.Context, call string) {
	if !mem.unbound || slices.Contains(readOnly, call) || slices.Contains(reads, call) {
		return
	}

	mem.resolve(ctx, true)
}

// settlesMapping says whether a call answers the mapping itself: `bind`, and
// `merge` given two store ids, run whatever the mapping says now.
func settlesMapping(name string, args []string) bool {
	if name == "bind" {
		return true
	}

	positional := slices.DeleteFunc(slices.Clone(args), func(arg string) bool { return strings.HasPrefix(arg, "--") })

	return name == "merge" && len(positional) == 2 && home.ValidID(positional[0]) && home.ValidID(positional[1])
}

func (mem *memory) bind(ctx context.Context, req request) result {
	if mem.outsideGit {
		return refused("%s", mem.outsideGitRefusal())
	}

	var (
		bound home.Bound
		err   error
	)

	switch {
	case req.Fresh && req.Target != "":
		return refused("bind takes a store or --new, not both")
	case req.Fresh:
		bound, err = mem.resolver().BindNew(ctx, mem.facts)
	case req.Target == "":
		return refused("bind needs a store id, or --new for a store of its own")
	case !mem.interactive(ctx) && mem.bindRefusal(req.Target) != "":
		return refused("%s", mem.bindRefusal(req.Target))
	default:
		bound, err = mem.resolver().Bind(ctx, mem.facts, req.Target)
	}

	if err != nil {
		return refused("%v", err)
	}

	lines := []string{fmt.Sprintf("bound %s to the store %s (%s)", mem.facts.Root, bound.Store.ID, bound.Store.Name())}

	for _, moved := range bound.Moved {
		lines = append(lines, fmt.Sprintf("moved %s from the store %s (%s); `%s merge %s %s` joins the two stores",
			strings.Join(moved.Identifiers, ", "), moved.From.ID, moved.From.Name(), self, moved.From.ID, bound.Store.ID))
	}

	return done(strings.Join(lines, "\n")).with(boundFields{Store: bound.Store.ID})
}

type boundFields struct {
	Store string `json:"store"`
}

// mergeStores joins the store from into the store into: memory's entries,
// seats and shelves, the tool library, the checkouts, every id kept.
func (mem *memory) mergeStores(ctx context.Context, from string, into string) result {
	if !mem.interactive(ctx) && mem.mergeRefusal(from, into) != "" {
		return refused("%s", mem.mergeRefusal(from, into))
	}

	joined, err := mem.resolver().Merge(ctx, from, into, map[string]home.Joiner{
		home.Memory: mem.joinMemory,
		home.Tools:  tools.Join,
	})
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("joined the store %s into %s (%s): checkouts %s", from, joined.ID, joined.Name(),
		strings.Join(joined.Paths(), ", "))).with(boundFields{Store: joined.ID})
}

// joinMemory joins one store's memory into another's, both brought to the
// current format first: the log's events appended, seats and shelves added
// under their own ids, a name both stores use numbered apart, claims and
// attachment refs carried over. An entry id both logs hold refuses the join
// before anything changes.
func (mem *memory) joinMemory(ctx context.Context, _ string, fromDir string, intoDir string) error {
	source, target := mem.sibling(storeIDOf(fromDir)), mem.sibling(storeIDOf(intoDir))

	for _, held := range []*memory{source, target} {
		err := held.prepare(ctx)
		if err != nil {
			return fmt.Errorf("bringing %s to the current format: %w", held.key, err)
		}
	}

	unlockTarget, err := target.lockStore(ctx)
	if err != nil {
		return err
	}

	defer unlockTarget()

	unlockSource, err := source.lockStore(ctx)
	if err != nil {
		return err
	}

	defer unlockSource()

	events, err := source.allEvents(ctx)
	if err != nil {
		return err
	}

	existing, err := target.allEvents(ctx)
	if err != nil {
		return err
	}

	err = sharedEntries(existing, events)
	if err != nil {
		return err
	}

	err = joinRegistries(target.env.Roots.Data, source.store(), target.store())
	if err != nil {
		return err
	}

	err = carrySeatFiles(target.env.Roots.Data, source.seatsDir(), target.seatsDir())
	if err != nil {
		return err
	}

	err = joinRefs(target.env.Roots.Data, source.refsPath(), target.refsPath())
	if err != nil {
		return err
	}

	if len(events) == 0 {
		return nil
	}

	log, err := target.openLog()
	if err != nil {
		return err
	}

	err = log.Append(ctx, events...)
	if err != nil {
		return fmt.Errorf("appending the joined entries: %w", err)
	}

	return nil
}

// storeIDOf is the store id a component folder `<data>/stores/<id>/memory`
// belongs to.
func storeIDOf(component string) string {
	return filepath.Base(filepath.Dir(component))
}

func sharedEntries(existing []memorylog.Event, joining []memorylog.Event) error {
	held := map[string]bool{}

	for _, event := range existing {
		if event.Entry != "" {
			held[event.Entry] = true
		}
	}

	var shared []string

	for _, event := range joining {
		if event.Entry != "" && held[event.Entry] && !slices.Contains(shared, event.Entry) {
			shared = append(shared, event.Entry)
		}
	}

	if len(shared) == 0 {
		return nil
	}

	slices.Sort(shared)

	return fmt.Errorf("both stores hold the entry ids %s; nothing was joined", strings.Join(shared, ", "))
}

// joinRegistries adds the source's seats and shelves to the target's under
// their own ids; a name the target already uses gets the first free `-<n>`.
func joinRegistries(root string, source string, target string) error {
	from, _, err := seat.LoadRegistry(source)
	if err != nil {
		return err
	}

	into, _, err := seat.LoadRegistry(target)
	if err != nil {
		return err
	}

	for _, id := range from.IDs() {
		if _, taken := into.Seats[id]; taken {
			continue
		}

		entry := from.Seats[id]
		entry.Name = freeName(entry.Name, func(name string) bool {
			_, used := into.IDOf(name)

			return used
		})
		into.Seats[id] = entry
	}

	fromShelves, _, err := seat.LoadShelves(source)
	if err != nil {
		return err
	}

	intoShelves, _, err := seat.LoadShelves(target)
	if err != nil {
		return err
	}

	for _, id := range fromShelves.IDs() {
		if _, taken := intoShelves.Shelves[id]; taken {
			continue
		}

		shelf := fromShelves.Shelves[id]
		shelf.Name = freeName(shelf.Name, func(name string) bool {
			_, used := intoShelves.IDOf(name)

			return used
		})
		intoShelves.Shelves[id] = shelf
	}

	return errors.Join(into.Save(root), intoShelves.Save(root))
}

func freeName(name string, used func(string) bool) string {
	if !used(name) {
		return name
	}

	for count := 2; ; count++ {
		candidate := name + "-" + strconv.Itoa(count)
		if !used(candidate) {
			return candidate
		}
	}
}

// carrySeatFiles copies each claim and slice the target lacks.
func carrySeatFiles(root string, source string, target string) error {
	listed, err := os.ReadDir(source)
	if err != nil {
		return nil
	}

	err = safefs.MkdirAll(root, target, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", target, err)
	}

	for _, entry := range listed {
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		_, statErr := os.Stat(filepath.Join(target, entry.Name()))
		if statErr == nil {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", entry.Name(), readErr)
		}

		writeErr := safefs.WriteFile(root, filepath.Join(target, entry.Name()), data, 0o600)
		if writeErr != nil {
			return fmt.Errorf("writing %s: %w", entry.Name(), writeErr)
		}
	}

	return nil
}

func joinRefs(root string, source string, target string) error {
	var from, into []string

	readJSON(source, &from)
	readJSON(target, &into)

	if len(from) == 0 {
		return nil
	}

	joined := append(into, from...)
	slices.Sort(joined)

	return writeJSON(root, target, slices.Compact(joined))
}
