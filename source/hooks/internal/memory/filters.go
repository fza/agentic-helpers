package memory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/layout"
)

// One filter set serves every read: a view's layout, search's flags and the
// brief's options all compile to the filters below, each defined once with how
// its arguments read, what it keeps, and the costly state it needs. The words
// sdd's view layout language shares keep sdd's meaning.

// need is costly state a filter reads beyond the folded log, so a read
// computes it only where a filter or a returned entry asks for it.
type need int

const (
	needLinks need = 1 << iota
	needSeen
	needFiles
	needGraph
	needGit
)

// predicate is what a filter keeps.
type predicate func(scope *viewScope, held *entry) bool

// filter is one compiled filter: how it was spelled, what it keeps, and what
// it needs. A read runs the cheap ones first.
type filter struct {
	spelled string
	keep    predicate
	needs   need
}

func (held filter) cost() int {
	cost := 0

	for bit := needLinks; bit <= needGit; bit <<= 1 {
		if held.needs&bit != 0 {
			cost = int(bit)
		}
	}

	return cost
}

// arity is how many arguments a filter word takes.
type arity int

const (
	takesNone arity = iota
	takesOne
	takesSome
	takesOptional
)

// filterWord is one word of the filter set. values is its closed set of
// arguments, empty where they are free; suggest lists what a free argument
// may be, for the nearest word a refusal names.
type filterWord struct {
	name     string
	arity    arity
	values   []string
	takes    string
	needs    need
	describe string
	suggest  func(scope *viewScope) []string
	build    func(scope *viewScope, values []string) (predicate, error)
}

// viewScope is what a read compiles and runs against: the folded log, the
// caller's checkout and seat, and the costly state its filters asked for,
// each computed once.
type viewScope struct {
	mem    *memory
	ctx    context.Context
	folded *state
	here   tree
	reader string
	// inbox is the seat the section being compiled names in inbox(<seat>),
	// whose seen mark unread reads.
	inbox string

	// reasons is each entry's returns, computed the first time a filter or a
	// section asks; probe answers the end conditions they check.
	reasons map[string][]returnReason
	probe   *endProbe

	heatsRead bool
	heatsHeld map[string]heatValue
}

func (mem *memory) scope(ctx context.Context, folded *state, reader string) *viewScope {
	return &viewScope{mem: mem, ctx: ctx, folded: folded, here: mem.checkout(ctx), reader: reader}
}

func (scope *viewScope) heat(held *entry) float64 {
	if !scope.heatsRead {
		scope.heatsHeld, scope.heatsRead = scope.mem.heats(), true
	}

	return scope.mem.heat(scope.heatsHeld, held)
}

// argumentError is a refused argument, before the layout's refusal names its
// token and position: what was wrong and the nearest word.
type argumentError struct {
	problem string
	nearest string
}

func (problem *argumentError) Error() string { return problem.problem }

var sinceSpan = regexp.MustCompile(`^([0-9]+)([hdwmy])$`)

var filterWords = []filterWord{
	returnsWord,
	{
		name: "active", arity: takesNone, describe: "live entries",
		build: func(*viewScope, []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return held.status == statusLive }, nil
		},
	},
	{
		name: "status", arity: takesSome, values: searchStatuses, describe: "entries in any of the statuses",
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return slices.Contains(values, held.status) }, nil
		},
	},
	{
		name: "kind", arity: takesSome, values: entryKinds, describe: "entries of any of the kinds",
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return slices.Contains(values, held.kind) }, nil
		},
	},
	{
		name: "from", arity: takesSome, values: sources, describe: "entries filed from any of the sources",
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return slices.Contains(values, held.from) }, nil
		},
	},
	{
		name: "topic", arity: takesSome, takes: "a topic", describe: "entries under a topic, component-wise prefix, read through merges",
		suggest: func(scope *viewScope) []string { return mapKeys(scope.mem.topicCounts(scope.folded)) },
		build: func(scope *viewScope, values []string) (predicate, error) {
			var wanted []string
			for _, value := range values {
				wanted = append(wanted, scope.folded.resolve(value))
			}

			return func(scope *viewScope, held *entry) bool {
				return slices.ContainsFunc(scope.folded.topicsOf(held), func(topic string) bool {
					return slices.ContainsFunc(wanted, func(prefix string) bool { return underPath(topic, prefix) })
				})
			}, nil
		},
	},
	{
		name: "since", arity: takesOne, takes: `a span such as "7d" or a date`,
		describe: "entries filed since the span or the date",
		suggest:  func(*viewScope) []string { return []string{"7d", "24h", "2w", "1m"} },
		build: func(scope *viewScope, values []string) (predicate, error) {
			from, err := sinceTime(values[0], scope.mem.now())
			if err != nil {
				return nil, err
			}

			return func(_ *viewScope, held *entry) bool { return !held.filed.Before(from) }, nil
		},
	},
	{
		name: "id", arity: takesSome, takes: "an entry id", describe: "the listed entries",
		suggest: func(scope *viewScope) []string { return slices.Clone(scope.folded.order) },
		build: func(_ *viewScope, values []string) (predicate, error) {
			for _, value := range values {
				if !strings.HasPrefix(value, "m-") || !linkTarget.MatchString(value) {
					return nil, &argumentError{problem: "id takes an entry id"}
				}
			}

			return func(_ *viewScope, held *entry) bool { return slices.Contains(values, held.id) }, nil
		},
	},
	{
		name: "seat", arity: takesSome, takes: "a seat", describe: "entries filed by any of the seats",
		suggest: seatNames,
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return slices.Contains(values, held.seat) }, nil
		},
	},
	{
		name: "shelf", arity: takesSome, takes: "a shelf", describe: "entries on any of the shelves, a seat naming its own",
		suggest: shelfNames,
		build: func(scope *viewScope, values []string) (predicate, error) {
			shelves := scope.mem.shelvesNamed(values)

			return func(scope *viewScope, held *entry) bool {
				return slices.Contains(shelves, scope.mem.entryShelf(held))
			}, nil
		},
	},
	{
		name: "for", arity: takesSome, takes: "all or a shelf", describe: "entries filed for every seat, or for any of the shelves",
		suggest: func(scope *viewScope) []string { return append([]string{forAllSeats}, shelfNames(scope)...) },
		build: func(scope *viewScope, values []string) (predicate, error) {
			everyone := slices.Contains(values, forAllSeats)
			shelves := scope.mem.shelvesNamed(values)

			return func(scope *viewScope, held *entry) bool {
				return (everyone && held.forAll) || slices.ContainsFunc(held.forKeys, func(key string) bool {
					return slices.Contains(shelves, scope.mem.recipientShelf(key))
				})
			}, nil
		},
	},
	{
		name: "mine", arity: takesNone, describe: "the calling seat's shelf and what it receives, as search and the brief cover",
		build: func(scope *viewScope, _ []string) (predicate, error) {
			if scope.reader == "" {
				return nil, &argumentError{problem: "mine needs a seat: claim one first", nearest: "seat"}
			}

			return func(scope *viewScope, held *entry) bool {
				return scope.mem.ownOrReceived(scope.folded, held, scope.reader)
			}, nil
		},
	},
	{
		name: "inbox", arity: takesOne, takes: "a seat",
		describe: "entries the seat is told of: a shelf mate's, or another shelf's filed for all, for its shelf, or under a topic its shelf follows; never its own",
		suggest:  seatNames,
		build: func(scope *viewScope, values []string) (predicate, error) {
			if scope.mem.shelfOf(values[0]) == "" {
				return nil, &argumentError{problem: "inbox takes a seat of this project"}
			}

			return func(scope *viewScope, held *entry) bool {
				return scope.mem.arrives(scope.folded, held, values[0])
			}, nil
		},
	},
	{
		name: "unread", arity: takesNone, needs: needSeen,
		describe: "entries filed after the seen mark of the seat inbox names in the same section; a seat without a mark has seen everything",
		build: func(scope *viewScope, _ []string) (predicate, error) {
			if scope.inbox == "" {
				return nil, &argumentError{problem: "unread needs inbox(<seat>) in its section", nearest: "inbox"}
			}

			var mark seenMark
			if !readJSON(scope.mem.seenPath(scope.inbox), &mark) {
				return func(*viewScope, *entry) bool { return false }, nil
			}

			return func(_ *viewScope, held *entry) bool { return held.filed.After(mark.At) }, nil
		},
	},
	{
		name: "branch", arity: takesOne, takes: "a branch", describe: "entries filed on the branch",
		suggest: func(scope *viewScope) []string { return []string{scope.here.branch} },
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool { return held.branch == values[0] }, nil
		},
	},
	{
		name: "linked", arity: takesOne, takes: "an entry id or a graph id", describe: "entries linking to the id",
		suggest: func(scope *viewScope) []string { return slices.Clone(scope.folded.order) },
		build: func(_ *viewScope, values []string) (predicate, error) {
			return func(_ *viewScope, held *entry) bool {
				return slices.ContainsFunc(held.links(), func(target string) bool {
					_, bare, qualified := splitQualified(target)

					return target == values[0] || (qualified && bare == values[0])
				})
			}, nil
		},
	},
	{
		name: "anchored", arity: takesOne, takes: "a path in the work tree", describe: "entries anchored to the path, or to a file under it",
		suggest: anchoredPaths,
		build: func(_ *viewScope, values []string) (predicate, error) {
			wanted := strings.Trim(values[0], "/")

			return func(_ *viewScope, held *entry) bool {
				return slices.ContainsFunc(held.anchors, func(anchored anchor) bool { return underPath(anchored.Path, wanted) })
			}, nil
		},
	},
}

// underPath is whether name is prefix or lies under it, component by
// component: `infra` holds `infra/ci`, never `infrastructure`.
func underPath(name string, prefix string) bool {
	return name == prefix || strings.HasPrefix(name, prefix+"/")
}

func sinceTime(value string, now time.Time) (time.Time, error) {
	span := sinceSpan.FindStringSubmatch(value)
	if span != nil {
		count, _ := strconv.Atoi(span[1])

		switch span[2] {
		case "h":
			return now.Add(-time.Duration(count) * time.Hour), nil
		case "d":
			return now.AddDate(0, 0, -count), nil
		case "w":
			return now.AddDate(0, 0, -7*count), nil
		case "m":
			return now.AddDate(0, -count, 0), nil
		default:
			return now.AddDate(-count, 0, 0), nil
		}
	}

	at, err := parseEndTime(value)
	if err != nil {
		return time.Time{}, &argumentError{problem: `since takes a span such as "7d" or a date`, nearest: "7d"}
	}

	return at, nil
}

func mapKeys[V any](held map[string]V) []string {
	keys := make([]string, 0, len(held))
	for key := range held {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func seatNames(scope *viewScope) []string {
	registry, _ := scope.mem.registry()

	var names []string

	for _, id := range registry.IDs() {
		names = append(names, registry.NameOf(id))
	}

	return names
}

func shelfNames(scope *viewScope) []string {
	shelves, _ := scope.mem.shelves()

	var names []string

	for _, id := range shelves.IDs() {
		names = append(names, shelves.Shelves[id].Name)
	}

	return names
}

func anchoredPaths(scope *viewScope) []string {
	var paths []string

	for _, held := range scope.folded.entries {
		for _, anchored := range held.anchors {
			if !slices.Contains(paths, anchored.Path) {
				paths = append(paths, anchored.Path)
			}
		}
	}

	slices.Sort(paths)

	return paths
}

// shelvesNamed is the shelf each name names: a shelf by its name, else the
// shelf of the seat of that name.
func (mem *memory) shelvesNamed(names []string) []string {
	shelves, _ := mem.shelves()

	var found []string

	for _, name := range names {
		shelf, known := shelves.IDOf(name)
		if !known {
			shelf = mem.shelfOf(name)
		}

		if shelf != "" {
			found = append(found, shelf)
		}
	}

	return found
}

func filterWordNamed(name string) (filterWord, bool) {
	for _, word := range filterWords {
		if word.name == name {
			return word, true
		}
	}

	return filterWord{}, false
}

// compileFilter builds the filter a call names, refusing a wrong argument with
// its token and the nearest word.
func (scope *viewScope) compileFilter(call layout.Call) (filter, error) {
	if call.Name == "not" {
		return scope.compileNot(call)
	}

	word, _ := filterWordNamed(call.Name)

	values, err := scope.filterValues(word, call)
	if err != nil {
		return filter{}, err
	}

	keep, err := word.build(scope, values)
	if err != nil {
		var problem *argumentError
		if errors.As(err, &problem) {
			at := call.Position
			token := call.Name

			if len(call.Args) > 0 {
				at, token = call.Args[0].Position, strings.Join(values, ",")
			}

			return filter{}, &layoutRefusal{problem: problem.problem, token: token, position: at,
				nearest: cmp.Or(problem.nearest, nearestWord(token, suggestionsOf(scope, word)))}
		}

		return filter{}, err
	}

	return filter{spelled: callText(call), keep: keep, needs: word.needs}, nil
}

func suggestionsOf(scope *viewScope, word filterWord) []string {
	if len(word.values) > 0 {
		return word.values
	}

	if word.suggest != nil {
		return word.suggest(scope)
	}

	return nil
}

// filterValues reads a filter's arguments as words, checked against its arity
// and its closed set.
func (scope *viewScope) filterValues(word filterWord, call layout.Call) ([]string, error) {
	count := len(call.Args)

	switch {
	case word.arity == takesNone && call.Parens:
		return nil, &layoutRefusal{problem: word.name + " takes no arguments", token: callText(call),
			position: call.Position, nearest: word.name}
	case (word.arity == takesOne || word.arity == takesSome) && count == 0:
		return nil, &layoutRefusal{problem: fmt.Sprintf("%s takes %s", word.name, takesOf(word)), token: callText(call),
			position: call.Position, nearest: word.name + "(" + firstOr(suggestionsOf(scope, word), "") + ")"}
	case word.arity == takesOne && count > 1:
		return nil, &layoutRefusal{problem: word.name + " takes one argument", token: callText(call),
			position: call.Args[1].Position, nearest: word.name + "(" + firstWord(call.Args[0]) + ")"}
	}

	values := make([]string, 0, count)

	for _, arg := range call.Args {
		value, isWord := arg.Word()
		if !isWord {
			return nil, &layoutRefusal{problem: fmt.Sprintf("%s takes %s, not a function", word.name, takesOf(word)),
				token: callText(*arg.Call), position: arg.Position, nearest: nearestWord(arg.Call.Name, suggestionsOf(scope, word))}
		}

		if len(word.values) > 0 && !slices.Contains(word.values, value) {
			return nil, &layoutRefusal{problem: fmt.Sprintf("%s takes %s", word.name, takesOf(word)), token: value,
				position: arg.Position, nearest: nearestWord(value, word.values)}
		}

		values = append(values, value)
	}

	return values, nil
}

func takesOf(word filterWord) string {
	if len(word.values) > 0 {
		return orList(word.values)
	}

	return word.takes
}

// orList spells a set as prose: "a, b or c".
func orList(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}

	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}

func firstOr(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}

	return values[0]
}

func firstWord(arg layout.Arg) string {
	word, _ := arg.Word()

	return word
}

// compileNot builds not(<filter>): what the inner filter refuses.
func (scope *viewScope) compileNot(call layout.Call) (filter, error) {
	if len(call.Args) != 1 || call.Args[0].Call == nil {
		return filter{}, &layoutRefusal{problem: "not takes one filter", token: callText(call), position: call.Position,
			nearest: "not(kind(note))"}
	}

	inner := *call.Args[0].Call
	if _, known := filterWordNamed(inner.Name); !known && inner.Name != "not" {
		return filter{}, &layoutRefusal{problem: "not takes a filter", token: inner.Name, position: inner.Position,
			nearest: nearestWord(inner.Name, filterNames())}
	}

	held, err := scope.compileFilter(inner)
	if err != nil {
		return filter{}, err
	}

	return negated(held), nil
}

// negated is not(<filter>): what held refuses.
func negated(held filter) filter {
	return filter{
		spelled: "not(" + held.spelled + ")",
		keep:    func(scope *viewScope, entry *entry) bool { return !held.keep(scope, entry) },
		needs:   held.needs,
	}
}

// filterOf builds a filter from a flag's values, the way search's and the
// brief's options compile to the filter set.
func (scope *viewScope) filterOf(name string, values ...string) (filter, error) {
	call := layout.Call{Name: name, Parens: len(values) > 0}
	for _, value := range values {
		call.Args = append(call.Args, layout.Arg{Quoted: true, Text: value})
	}

	return scope.compileFilter(call)
}

func filterNames() []string {
	names := []string{"not"}
	for _, word := range filterWords {
		names = append(names, word.name)
	}

	return names
}

// callText spells a call back as a layout writes it.
func callText(call layout.Call) string {
	if !call.Parens {
		return call.Name
	}

	parts := make([]string, 0, len(call.Args))

	for _, arg := range call.Args {
		switch {
		case arg.Quoted && layout.IsWord(arg.Text):
			parts = append(parts, arg.Text)
		case arg.Quoted:
			parts = append(parts, strconv.Quote(arg.Text))
		default:
			parts = append(parts, callText(*arg.Call))
		}
	}

	return call.Name + "(" + strings.Join(parts, ",") + ")"
}

// nearestWord is the candidate closest to token by edit distance, the first
// listed among equals.
func nearestWord(token string, candidates []string) string {
	best, distance := "", -1

	for _, candidate := range candidates {
		apart := levenshtein(strings.ToLower(token), strings.ToLower(candidate))
		if distance < 0 || apart < distance {
			best, distance = candidate, apart
		}
	}

	return best
}

// keeps is whether every filter keeps the entry, the cheap ones asked first.
func keeps(scope *viewScope, filters []filter, held *entry) bool {
	for _, one := range filters {
		if !one.keep(scope, held) {
			return false
		}
	}

	return true
}

func cheapFirst(filters []filter) []filter {
	ordered := slices.Clone(filters)
	slices.SortStableFunc(ordered, func(a, b filter) int { return cmp.Compare(a.cost(), b.cost()) })

	return ordered
}

// selected is every entry the filters keep, shadowed ones aside: an entry
// superseded by another reads as its successor and never lists.
func (scope *viewScope) selected(filters []filter) []*entry {
	ordered := cheapFirst(filters)

	var kept []*entry

	for _, held := range scope.folded.entryList() {
		if !held.shadowed() && keeps(scope, ordered, held) {
			kept = append(kept, held)
		}
	}

	return kept
}
