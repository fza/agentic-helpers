package memory

import (
	"context"
	"errors"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"io"
	"slices"
	"strconv"
	"strings"
)

type flagKind int

const (
	flagText flagKind = iota
	flagList
	flagSwitch
	flagNumber
	flagCount
)

type flagSpec struct {
	name string
	kind flagKind
}

// request is one call's arguments, from a command line or an MCP tool call
// alike. A field a command does not take stays empty.
type request struct {
	Positional     []string `json:"-"`
	IDs            []string `json:"ids,omitempty"`
	ID             string   `json:"id,omitempty"`
	Title          string   `json:"title,omitempty"`
	Body           string   `json:"body,omitempty"`
	Topics         []string `json:"topic,omitempty"`
	Untagged       []string `json:"topics,omitempty"`
	Kind           string   `json:"kind,omitempty"`
	From           string   `json:"from,omitempty"`
	About          []string `json:"about,omitempty"`
	Terms          []string `json:"term,omitempty"`
	Query          string   `json:"query,omitempty"`
	Seats          []string `json:"seat,omitempty"`
	AllSeats       bool     `json:"all_seats,omitempty"`
	WithSuperseded bool     `json:"with_superseded,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Skip           int      `json:"skip,omitempty"`
	Statuses       []string `json:"status,omitempty"`
	For            []string `json:"for,omitempty"`
	Links          []string `json:"link,omitempty"`
	Tools          []string `json:"tool,omitempty"`
	Absorbs        []string `json:"absorbs,omitempty"`
	Until          []string `json:"until,omitempty"`
	Attach         []string `json:"attach,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	File           string   `json:"file,omitempty"`
	Offset         int      `json:"offset,omitempty"`
	MaxBytes       int      `json:"max_bytes,omitempty"`
	To             string   `json:"to,omitempty"`
	When           []string `json:"when,omitempty"`
	On             []string `json:"on,omitempty"`
	Proof          []string `json:"proof,omitempty"`
	Forbids        []string `json:"forbids,omitempty"`
	Done           bool     `json:"done,omitempty"`
	Decline        string   `json:"decline,omitempty"`
	Target         string   `json:"target,omitempty"`
	Path           string   `json:"path,omitempty"`
	By             string   `json:"by,omitempty"`
	Because        string   `json:"because,omitempty"`
	Level          string   `json:"level,omitempty"`
	Into           string   `json:"into,omitempty"`
	FromTopic      string   `json:"from_topic,omitempty"`
	Name           string   `json:"name,omitempty"`
	Session        string   `json:"session,omitempty"`
	Shelf          string   `json:"shelf,omitempty"`
	Lifetime       string   `json:"lifetime,omitempty"`
	Value          string   `json:"value,omitempty"`
	All            bool     `json:"all,omitempty"`
	Unused         bool     `json:"unused,omitempty"`
	Private        bool     `json:"private,omitempty"`
	JSON           bool     `json:"json,omitempty"`
	NoHeat         bool     `json:"no_heat,omitempty"`
	Again          bool     `json:"again,omitempty"`
	Layout         string   `json:"layout,omitempty"`
	Explain        string   `json:"explain,omitempty"`
	Items          []string `json:"item,omitempty"`
	ItemsMode      string   `json:"items,omitempty"`
	Undo           bool     `json:"undo,omitempty"`
	Remove         bool     `json:"remove,omitempty"`
	Add            []string `json:"add,omitempty"`
	Order          string   `json:"order,omitempty"`
	WithRemoved    bool     `json:"with_removed,omitempty"`
	Fresh          bool     `json:"new,omitempty"`
	Due            dueArg   `json:"due"`
	Clear          bool     `json:"clear,omitempty"`

	given map[string]bool
}

type result struct {
	text   string
	code   int
	failed bool
	// fields is what the call produced, answered beside the text over MCP.
	fields any
}

func (answer result) with(fields any) result {
	answer.fields = fields

	return answer
}

// normalize spells every seat name a request carries the one way the store
// keeps it, whichever path the request came by.
func (req *request) normalize() {
	if req.Name != "" {
		req.Name = seat.Normal(req.Name)
	}

	if req.Shelf != "" {
		req.Shelf = seat.Normal(req.Shelf)
	}

	for index, name := range req.Seats {
		req.Seats[index] = seat.Normal(name)
	}

	for index, name := range req.For {
		req.For[index] = seat.Normal(name)
	}
}

func topicsOnly(req *request) error {
	if len(req.Positional) == 0 {
		return errors.New("at least one topic expected")
	}

	req.Topics = req.Positional

	return nil
}

func done(text string) result {
	return result{text: text}
}

func refused(format string, args ...any) result {
	return result{text: fmt.Sprintf(format, args...), code: 1, failed: true}
}

type command struct {
	name        string
	usage       string
	description string
	flags       []flagSpec
	readsBody   bool
	positional  func(req *request) error
	schema      string
	run         func(mem *memory, ctx context.Context, req request) result
	// replaced names, per flag a command no longer takes, what took its place,
	// so a call passing it hears where to go, as a flag and as an MCP key.
	replaced map[string]string
	// cliOnly keeps a command off the MCP server's listing: a command an
	// agent seldom runs costs every session its share of the listing.
	cliOnly bool
}

func commandByName(name string) (command, bool) {
	for _, held := range commands() {
		if held.name == name {
			return held, true
		}
	}

	return command{}, false
}

func helpText() string {
	lines := []string{"usage: " + self + " <subcommand>", ""}
	for _, held := range commands() {
		lines = append(lines, fmt.Sprintf("  %-58s %s", held.usage, held.description))
	}

	lines = append(lines, "",
		"  mcp                                                         the stdio MCP server",
		"  dump <segment>|--tail                                       a log segment or the tail, one event per line",
		"",
		"An attached file holds at most attachment_max_mb; every attachment on the machine together at most "+
			"attachments_total_mb, each file counted once.")

	return strings.Join(lines, "\n")
}

// parse reads a command line, flags and positionals in any order, and the body
// from stdin where the command takes one.
func (held command) parse(args []string, stdin io.Reader) (request, error) {
	req := request{given: map[string]bool{}}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			req.Positional = append(req.Positional, arg)

			continue
		}

		name, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")

		spec, found := held.flag(name)
		if instead, gone := held.replaced[name]; !found && gone {
			return request{}, fmt.Errorf("%s takes no --%s: %s", held.name, name, instead)
		}

		if !found {
			return request{}, fmt.Errorf("%s takes no --%s; usage: %s %s", held.name, name, self, held.usage)
		}

		if spec.kind != flagSwitch && !inline {
			if index+1 >= len(args) {
				return request{}, fmt.Errorf("--%s needs a value; usage: %s %s", name, self, held.usage)
			}

			index++
			value = args[index]
		}

		err := req.set(spec, value)
		if err != nil {
			return request{}, err
		}
	}

	if held.readsBody && stdin != nil {
		body, err := io.ReadAll(stdin)
		if err != nil {
			return request{}, fmt.Errorf("reading the body from stdin: %w", err)
		}

		req.Body = string(body)
	}

	if held.positional != nil {
		err := held.positional(&req)
		if err != nil {
			return request{}, fmt.Errorf("%w; usage: %s %s", err, self, held.usage)
		}
	} else if len(req.Positional) > 0 {
		return request{}, fmt.Errorf("%s takes no argument %q; usage: %s %s", held.name, req.Positional[0], self, held.usage)
	}

	return req, nil
}

func (held command) flag(name string) (flagSpec, bool) {
	for _, spec := range held.flags {
		if spec.name == name {
			return spec, true
		}
	}

	return flagSpec{}, false
}

func (req *request) set(spec flagSpec, value string) error {
	req.given[spec.name] = true

	texts := map[string]*string{
		"title": &req.Title, "kind": &req.Kind, "from": &req.From, "query": &req.Query,
		"by": &req.By, "because": &req.Because, "session": &req.Session, "shelf": &req.Shelf,
		"lifetime": &req.Lifetime, "decline": &req.Decline, "to": &req.To,
		"layout": &req.Layout, "explain": &req.Explain, "items": &req.ItemsMode, "order": &req.Order,
		"due": &req.Due.text,
	}
	lists := map[string]*[]string{
		"topic": &req.Topics, "about": &req.About, "term": &req.Terms, "seat": &req.Seats, "status": &req.Statuses,
		"for": &req.For, "link": &req.Links, "tool": &req.Tools, "absorbs": &req.Absorbs, "until": &req.Until, "attach": &req.Attach,
		"when": &req.When, "on": &req.On, "proof": &req.Proof, "forbids": &req.Forbids,
		"item": &req.Items, "add": &req.Add,
	}
	numbers := map[string]*int{"limit": &req.Limit, "skip": &req.Skip, "offset": &req.Offset, "max-bytes": &req.MaxBytes}
	switches := map[string]*bool{
		"all-seats": &req.AllSeats, "with-superseded": &req.WithSuperseded, "json": &req.JSON,
		"no-heat": &req.NoHeat, "all": &req.All, "done": &req.Done, "unused": &req.Unused,
		"private": &req.Private, "again": &req.Again, "undo": &req.Undo, "remove": &req.Remove,
		"with-removed": &req.WithRemoved, "new": &req.Fresh, "due": &req.Due.listed, "clear": &req.Clear,
	}

	switch spec.kind {
	case flagText:
		*texts[spec.name] = value
	case flagList:
		*lists[spec.name] = append(*lists[spec.name], value)
	case flagSwitch:
		*switches[spec.name] = true
	case flagNumber:
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 {
			return fmt.Errorf("--%s takes a positive number, not %q", spec.name, value)
		}

		*numbers[spec.name] = number
	case flagCount:
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("--%s takes a number of 0 or more, not %q", spec.name, value)
		}

		*numbers[spec.name] = number
	}

	return nil
}

func exactly(count int, names ...*string) func(req *request) error {
	return func(req *request) error {
		if len(req.Positional) != count {
			return errors.New("wrong number of arguments")
		}

		for index, target := range names {
			*target = req.Positional[index]
		}

		return nil
	}
}

func oneID(req *request) error {
	if len(req.Positional) != 1 {
		return errors.New("one entry id expected")
	}

	req.ID = req.Positional[0]

	return nil
}

func someIDs(req *request) error {
	if len(req.Positional) == 0 {
		return errors.New("at least one entry id expected")
	}

	req.IDs = req.Positional

	return nil
}

func optionalName(req *request) error {
	if len(req.Positional) > 1 {
		return errors.New("at most one seat expected")
	}

	if len(req.Positional) == 1 {
		req.Name = req.Positional[0]
	}

	return nil
}

func validRetention(level string) bool {
	return slices.Contains(retentionLevels, level)
}

// idSchema takes every length a store can hold: the four characters ids were
// once minted with, up to the longest entry_id_length allows.
const idSchema = `{"type":"string","pattern":"^m-[a-z0-9]{4,16}$"}`

// idKey and idsKey are the keys naming the entries a tool acts on: one
// entry where the tool takes one, an array where it takes several, even for one.
const (
	idKey    = `"id":{"type":"string","pattern":"^m-[a-z0-9]{4,16}$","description":"entry id, one string"}`
	idsKey   = `"ids":{"type":"array","items":` + idSchema + `,"description":"entry ids, array even for one"}`
	noHeat   = `"no_heat":{"type":"boolean","description":"add no heat"}`
	againKey = `"again":{"type":"boolean","description":"print entries already seen in full"}`
	topicKey = `"topic":{"type":"array","items":{"type":"string"},"description":"topics, array even for one"}`
	seatName = `"name":{"type":"string","description":"seat name"}`
)

func commands() []command {
	return readsAsJSON(allCommands())
}

// jsonReads are the read commands that answer one JSON object under --json,
// and over MCP always.
var jsonReads = []string{"show", "search", "brief", "who", "topics", "view"}

func readsAsJSON(held []command) []command {
	for index, one := range held {
		if !slices.Contains(jsonReads, one.name) {
			continue
		}

		one.usage += " [--json]"
		one.flags = append(slices.Clone(one.flags), flagSpec{"json", flagSwitch})
		held[index] = one
	}

	return held
}

func allCommands() []command {
	return []command{
		{
			name:        "new",
			usage:       "new --title '<3-10 words>' --topic <name>... [--kind rule|trap|todo|note] [--until <condition>]... [--when <trigger>]... [--on claim|compact]... [--proof <trigger>]... [--forbids <regex>]... [--attach <path>]... [--for <seat>|all]... [--link <id>]... [--tool <name>[@<project>]]... [--absorbs <id>]... [--item '<step>']... [--items carry|leave] [--due <time>] [options]",
			description: "file an entry, in the language and style the brief names; kind sets its decay, note unless given",
			flags: []flagSpec{
				{"title", flagText}, {"topic", flagList}, {"kind", flagText}, {"from", flagText},
				{"about", flagList}, {"for", flagList}, {"link", flagList}, {"tool", flagList}, {"absorbs", flagList}, {"until", flagList}, {"attach", flagList},
				{"when", flagList}, {"on", flagList}, {"proof", flagList}, {"forbids", flagList},
				{"item", flagList}, {"items", flagText}, {"due", flagText},
			},
			readsBody: true,
			replaced:  map[string]string{"retention": retentionReplaced},
			schema: `{"type":"object","properties":{` +
				`"title":{"type":"string","description":"3-10 words"},` +
				`"body":{"type":"string","description":"entry text"},` +
				topicKey + `,` +
				`"kind":{"type":"string","enum":["rule","trap","todo","note"],"description":"rule needs from user; note unless given"},` +
				`"from":{"type":"string","enum":["user","agent","tool"],"description":"finding's source; agent unless given"},` +
				`"about":{"type":"array","items":{"type":"string"},"description":"paths it is about, hash-anchored"},` +
				`"for":{"type":"array","items":{"type":"string"},"description":"other seats it is for, or all"},` +
				`"link":{"type":"array","items":{"type":"string"},"description":"graph or memory ids to link"},` +
				`"tool":{"type":"array","items":{"type":"string"},"description":"agentic-tools tools to link, name or name@project; show takes them"},` +
				`"absorbs":{"type":"array","items":` + idSchema + `,"description":"ids it supersedes"},` +
				`"item":{"type":"array","items":{"type":"string"},"description":"todo only: the steps of one piece of work, each text or a memory or graph id; ticked later through ack <todo>#<item>, never a supersede"},` +
				`"items":{"type":"string","enum":["carry","leave"],"description":"an absorbed todo's items move here, or stay"},` +
				`"until":{"type":"array","items":{"type":"string"},"description":"ending conditions: ` + untilForms + `"},` +
				`"due":{"type":"string","description":"todo or note only: when it comes due, ` + dueForms + `"},` +
				`"when":{"type":"array","items":{"type":"string"},"description":"trap triggers: ` + triggerForms + `"},` +
				`"on":{"type":"array","items":{"type":"string","enum":["claim","compact"]},"description":"when a rule is owed"},` +
				`"proof":{"type":"array","items":{"type":"string"},"description":"calls settling an owed rule: ` + triggerForms + `"},` +
				`"forbids":{"type":"array","items":{"type":"string"},"description":"RE2 patterns later entries must not match"},` +
				`"attach":{"type":"array","items":{"type":"string"},"description":"file paths to keep with it, never content; each within attachment_max_mb"}},` +
				`"required":["title","body"]}`,
			run: (*memory).newEntry,
		},
		{
			name:        "search",
			usage:       "search [--term <regex>]... [--query <phrase>] [--status live|retired|superseded]... [--skip <n>] [options]",
			description: "search entries; needs term, query or status; status alone lists by time",
			flags: []flagSpec{
				{"term", flagList}, {"query", flagText}, {"topic", flagList}, {"seat", flagList},
				{"all-seats", flagSwitch}, {"with-superseded", flagSwitch}, {"limit", flagNumber},
				{"no-heat", flagSwitch}, {"status", flagList}, {"skip", flagCount}, {"again", flagSwitch},
			},
			schema: `{"type":"object","properties":{` +
				`"term":{"type":"array","items":{"type":"string"},"description":"RE2 patterns, case-insensitive, all must match"},` +
				`"query":{"type":"string","description":"phrase matched by meaning"},` +
				`"topic":{"type":"array","items":{"type":"string"},"description":"only these topics"},` +
				`"seat":{"type":"array","items":{"type":"string"},"description":"only these seats' entries"},` +
				`"all_seats":{"type":"boolean","description":"every shelf, not just own"},` +
				`"with_superseded":{"type":"boolean","description":"include superseded"},` +
				`"limit":{"type":"integer","minimum":1,"description":"hits per page"},` +
				`"status":{"type":"array","items":{"type":"string","enum":["live","retired","superseded"]},"description":"only these statuses"},` +
				`"skip":{"type":"integer","minimum":0,"description":"hits to skip, for paging"},` +
				noHeat + `,` + againKey + `}}`,
			run: (*memory).search,
		},
		{
			name: "show", usage: "show <id>... [--no-heat] [--with-superseded] [--with-removed] [--again]",
			description: "entries in full: stamps, anchors, flags, events, a todo's items; takes each tool an entry names and prints its usage",
			flags: []flagSpec{
				{"no-heat", flagSwitch}, {"with-superseded", flagSwitch}, {"with-removed", flagSwitch}, {"again", flagSwitch},
			},
			positional: someIDs, run: (*memory).show,
			schema: `{"type":"object","properties":{` + idsKey + `,` + noHeat + `,` + againKey + `,` +
				`"with_superseded":{"type":"boolean","description":"also superseded entries linking here"},` +
				`"with_removed":{"type":"boolean","description":"also a todo's removed items"}},"required":["ids"]}`,
		},
		{
			name: "brief", usage: "brief [--topic <name>] [--kind rule|trap|todo|note] [--due] [--again]",
			description: "the brief, or one topic's, kind's or the due entries",
			flags:       []flagSpec{{"topic", flagList}, {"kind", flagText}, {"due", flagSwitch}, {"again", flagSwitch}}, run: (*memory).briefCommand,
			schema: `{"type":"object","properties":{"topic":{"type":"array","items":{"type":"string"},"description":"one topic's entries"},` +
				`"kind":{"type":"string","enum":["rule","trap","todo","note"],"description":"one kind's live entries whole"},` +
				`"due":{"type":"boolean","description":"every live entry carrying a due time, earliest first"},` + againKey + `}}`,
		},
		{
			name: "view", usage: "view --layout '<layout>' [--again] | view --explain '<layout>'|brief",
			description: "read through an sdd view layout; takes layout or explain; warms nothing",
			flags:       []flagSpec{{"layout", flagText}, {"explain", flagText}, {"again", flagSwitch}}, run: (*memory).view,
			schema: `{"type":"object","properties":{` +
				`"layout":{"type":"string","description":"colon-chained filters, rank, paging, ending in ` + orList(renderWords) + `; commas split sections"},` +
				`"explain":{"type":"string","description":"layout, or brief, to spell out instead of reading"},` + againKey + `}}`,
		},
		{
			name: "topics", usage: "topics", description: "topics with live counts per seat",
			run: (*memory).topics, schema: `{"type":"object","properties":{}}`,
		},
		{
			name: "confirm", usage: "confirm <id>...", description: "re-anchor verified entries, all or none",
			positional: someIDs, run: (*memory).confirm,
			schema: `{"type":"object","properties":{` + idsKey + `},"required":["ids"]}`,
		},
		{
			name: "retain", cliOnly: true, usage: "retain <id>... <level>", description: "change entries' retention, all or none",
			positional: func(req *request) error {
				if len(req.Positional) < 2 {
					return errors.New("at least one entry id and a level expected")
				}

				req.IDs, req.Level = req.Positional[:len(req.Positional)-1], req.Positional[len(req.Positional)-1]

				return nil
			},
			run: (*memory).retain,
			schema: `{"type":"object","properties":{` + idsKey + `,"level":{"type":"string",` +
				`"enum":["standing","long","normal","short","scratch"],"description":"the decay overriding the kind's"}},"required":["ids","level"]}`,
		},
		{
			name: "attach", cliOnly: true, usage: "attach <id> <path>...",
			description: "keep files with an entry, read from disk by path, each within attachment_max_mb, ending when its body ends",
			positional: func(req *request) error {
				if len(req.Positional) < 2 {
					return errors.New("an entry id and at least one path expected")
				}

				req.ID, req.Paths = req.Positional[0], req.Positional[1:]

				return nil
			},
			run: (*memory).attach,
			schema: `{"type":"object","properties":{` + idKey +
				`,"paths":{"type":"array","items":{"type":"string"},"description":"paths of files to keep; never content"}},` +
				`"required":["id","paths"]}`,
		},
		{
			name: "read", cliOnly: true, usage: "read <id> [<name>] [--offset <n>] [--max-bytes <n>] [--to <path>]",
			description: "page a text attachment, framed as data, or write any attachment's bytes to a new path with to",
			flags:       []flagSpec{{"offset", flagCount}, {"max-bytes", flagNumber}, {"to", flagText}},
			positional: func(req *request) error {
				if len(req.Positional) < 1 || len(req.Positional) > 2 {
					return errors.New("an entry id and at most one attachment name expected")
				}

				req.ID = req.Positional[0]
				if len(req.Positional) == 2 {
					req.File = req.Positional[1]
				}

				return nil
			},
			run: (*memory).read,
			schema: `{"type":"object","properties":{` + idKey + `,"file":{"type":"string","description":"the attachment's name"},` +
				`"offset":{"type":"integer","minimum":0,"description":"the byte to start the page at"},` +
				`"max_bytes":{"type":"integer","minimum":1,"description":"the page's size in bytes"},` +
				`"to":{"type":"string","description":"a new path to write the attachment's bytes to"}},"required":["id"]}`,
		},
		{
			name: "ack", usage: "ack <id>... --done | --decline '<why>'  |  ack <todo>#<item>... --done | --undo | --decline '<why>' | --remove  |  ack <todo> --add '<text>'... | --order <item>,<item>...",
			description: "settle pending do-now items, or change a todo's items for good, each id then todo#item; all or none; takes one of done, decline, undo, remove, add or order",
			flags: []flagSpec{
				{"done", flagSwitch}, {"decline", flagText}, {"undo", flagSwitch}, {"remove", flagSwitch},
				{"add", flagList}, {"order", flagText}, {"no-heat", flagSwitch},
			},
			positional: someIDs, run: (*memory).ack,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string","pattern":"^m-[a-z0-9]{4,16}(#[a-z0-9]+)?$"},` +
				`"description":"rule ids, or <todo>#<item> addresses, or one todo id for add and order; array even for one"},` +
				`"done":{"type":"boolean","description":"done, or an item ticked"},"decline":{"type":"string","description":"why not done, or why an item is dropped"},` +
				`"undo":{"type":"boolean","description":"untick items"},"remove":{"type":"boolean","description":"remove items that never belonged"},` +
				`"add":{"type":"array","items":{"type":"string"},"description":"items to add at the end: text, or a memory or graph id"},` +
				`"order":{"type":"string","description":"item ids, comma-separated, to put first in that order"},` +
				noHeat + `},"required":["ids"]}`,
		},
		{
			name: "rekind", cliOnly: true, usage: "rekind <id> rule|trap|todo|note", description: "give an entry another kind; rule only for an entry filed from user",
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Kind)(req) },
			run:        (*memory).rekind,
			schema: `{"type":"object","properties":{` + idKey +
				`,"kind":{"type":"string","enum":["rule","trap","todo","note"],"description":"the entry's new kind"}},"required":["id","kind"]}`,
		},
		{
			name: "state", usage: "state", description: "overwrite this seat's state slot, in the style the brief names; the brief opens with it",
			readsBody: true, run: (*memory).writeState,
			schema: `{"type":"object","properties":{"body":{"type":"string","description":"where the work stands"}},"required":["body"]}`,
		},
		{
			name: "retire", usage: "retire <id>... [--because '<why>']", description: "end entries: flagged in results, out of the brief; all or none",
			flags: []flagSpec{{"because", flagText}}, positional: someIDs, run: (*memory).retire,
			schema: `{"type":"object","properties":{` + idsKey + `,` +
				`"because":{"type":"string","description":"why they ended"}},"required":["ids"]}`,
		},
		{
			name: "supersede", usage: "supersede <id>... [--by <id>] [--because '<why>'] [--items carry|leave]",
			description: "take entries out of results, all or none; takes by, because or both, at least one; a todo with items needs items",
			flags:       []flagSpec{{"by", flagText}, {"because", flagText}, {"items", flagText}}, positional: someIDs, run: (*memory).supersede,
			schema: `{"type":"object","properties":{` + idsKey +
				`,"by":{"type":"string","pattern":"^m-[a-z0-9]{4,16}$","description":"replacing entry id"}` +
				`,"because":{"type":"string","description":"why wrong or replaced"}` +
				`,"items":{"type":"string","enum":["carry","leave"],"description":"a todolist moves to by, or stays"}},"required":["ids"]}`,
		},
		{
			name: "revive", cliOnly: true, usage: "revive <id>", description: "reverse the latest retire or supersede",
			positional: oneID, run: (*memory).revive,
			schema: `{"type":"object","properties":{` + idKey + `},"required":["id"]}`,
		},
		{
			name: "retitle", usage: "retitle <id> '<title>'", description: "retitle an entry",
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Title)(req) },
			run:        (*memory).retitle,
			schema: `{"type":"object","properties":{` + idKey +
				`,"title":{"type":"string","description":"new title, 3-10 words"}},"required":["id","title"]}`,
		},
		{
			name: "due", usage: "due <id> <time> | due <id> --clear", description: "move or clear a todo's or note's due time",
			flags:      []flagSpec{{"clear", flagSwitch}},
			positional: duePositional,
			run:        (*memory).dueCommand,
			schema: `{"type":"object","properties":{` + idKey +
				`,"due":{"type":"string","description":"when it comes due, ` + dueForms + `"}` +
				`,"clear":{"type":"boolean","description":"drop the due time"}},"required":["id"]}`,
		},
		{
			name: "tag", usage: "tag <id> <topic>...", description: "add topics to an entry",
			positional: func(req *request) error {
				if len(req.Positional) < 2 {
					return errors.New("an entry id and at least one topic expected")
				}

				req.ID, req.Topics = req.Positional[0], req.Positional[1:]

				return nil
			},
			run:    (*memory).tag,
			schema: `{"type":"object","properties":{` + idKey + `,` + topicKey + `},"required":["id","topic"]}`,
		},
		{
			name: "untag", usage: "untag <id> <topic>...", description: "remove topics from an entry; its last topic stays",
			positional: func(req *request) error {
				if len(req.Positional) < 2 {
					return errors.New("an entry id and at least one topic expected")
				}

				req.ID, req.Untagged = req.Positional[0], req.Positional[1:]

				return nil
			},
			run: (*memory).untag,
			schema: `{"type":"object","properties":{` + idKey +
				`,"topics":{"type":"array","items":{"type":"string"},"description":"topics to remove, array even for one"}},"required":["id","topics"]}`,
		},
		{
			name: "link", usage: "link <id> <target> | link <id> --tool <name>[@<project>] [--no-heat]",
			description: "link an entry to a graph or memory entry, or to an agentic-tools tool show then takes",
			flags:       []flagSpec{{"no-heat", flagSwitch}, {"tool", flagList}},
			positional:  linkPositional, run: (*memory).link,
			schema: `{"type":"object","properties":{` + idKey + `,"target":{"type":"string","description":"full graph id or memory id"},` +
				`"tool":{"type":"array","items":{"type":"string"},"description":"in place of target: one tool, name or name@project"},` +
				`"no_heat":{"type":"boolean","description":"add no heat to a memory target"}},"required":["id"]}`,
		},
		{
			name: "unlink", cliOnly: true, usage: "unlink <id> <target> | unlink <id> --tool <name>[@<project>]",
			description: "drop a link made by new's link or tool key or by link",
			flags:       []flagSpec{{"tool", flagList}},
			positional:  linkPositional, run: (*memory).unlink,
			schema: `{"type":"object","properties":{` + idKey + `,"target":{"type":"string","description":"the linked graph id or memory id"},` +
				`"tool":{"type":"array","items":{"type":"string"},"description":"in place of target: the linked tool"}},"required":["id"]}`,
		},
		{
			name: "bind", cliOnly: true, usage: "bind <store> | bind --new",
			description: "bind this checkout to a store of the machine, or to a new one",
			flags:       []flagSpec{{"new", flagSwitch}},
			positional: func(req *request) error {
				if len(req.Positional) > 1 {
					return errors.New("at most one store expected")
				}

				if len(req.Positional) == 1 {
					req.Target = req.Positional[0]
				}

				return nil
			},
			run: (*memory).bind,
			schema: `{"type":"object","properties":{"target":{"type":"string","description":"a store id"},` +
				`"new":{"type":"boolean","description":"bind to a new store"}}}`,
		},
		{
			name: "who", usage: "who [--all] [--unused]",
			description: "this project's seats and shelves; frees seats whose holder is gone",
			flags:       []flagSpec{{"all", flagSwitch}, {"unused", flagSwitch}}, run: (*memory).who,
			schema: `{"type":"object","properties":{"all":{"type":"boolean","description":"add other projects' seats"}}}`,
		},
		{
			name: "follow", cliOnly: true, usage: "follow <topic>...", description: "receive every entry filed under these topics",
			positional: topicsOnly, run: (*memory).follow,
			schema: `{"type":"object","properties":{` + topicKey + `},"required":["topic"]}`,
		},
		{
			name: "unfollow", cliOnly: true, usage: "unfollow <topic>...", description: "stop receiving entries filed under these topics",
			positional: topicsOnly, run: (*memory).unfollow,
			schema: `{"type":"object","properties":{` + topicKey + `},"required":["topic"]}`,
		},
		{
			name: "merge", cliOnly: true, usage: "merge <from> <into>",
			description: "merge one topic into another, every seat; given two store ids, join the first store into the second",
			positional:  func(req *request) error { return exactly(2, &req.FromTopic, &req.Into)(req) },
			run:         (*memory).merge,
			schema: `{"type":"object","properties":{"from_topic":{"type":"string","description":"the topic merged away"},` +
				`"into":{"type":"string","description":"the topic it merges into"}},"required":["from_topic","into"]}`,
		},
		{
			name: "holder", cliOnly: true, usage: "holder <seat> [--json] | holder --session <id> --json",
			description: "who holds a seat, named or by session; exit 0 held, 1 free",
			flags:       []flagSpec{{"json", flagSwitch}, {"session", flagText}}, positional: optionalName,
			run: (*memory).holder,
			schema: `{"type":"object","properties":{` + seatName + `,` +
				`"session":{"type":"string","description":"a session id, in place of name: the seat it holds"}}}`,
		},
		{
			name: "claim", usage: "claim <seat> [--shelf <shelf>] [--lifetime durable|temporary|<span>] [--private] [--session <id>] | claim --shelf <shelf> [...]",
			description: "take a seat; prints the brief and the slice",
			flags:       []flagSpec{{"shelf", flagText}, {"lifetime", flagText}, {"private", flagSwitch}, {"session", flagText}},
			positional:  optionalName,
			run:         (*memory).claim,
			schema: `{"type":"object","properties":{` + seatName + `,` +
				`"shelf":{"type":"string","description":"shelf for a new seat; alone, claims a new seat on it"},` +
				`"lifetime":{"type":"string","description":"durable, temporary or a span: 90m, 12h, 2d, 1w, 0"},` +
				`"private":{"type":"boolean","description":"hide the seat from other projects' who"},` +
				`"session":{"type":"string","description":"another session to claim for"}}}`,
		},
		{
			name: "rename", cliOnly: true, usage: "rename '<new name>'", description: "give the seat this session holds a new name; its id and entries stay",
			positional: func(req *request) error { return exactly(1, &req.Name)(req) },
			run:        (*memory).rename,
			schema:     `{"type":"object","properties":{"name":{"type":"string","description":"the seat's new name"}},"required":["name"]}`,
		},
		{
			name: "private", cliOnly: true, usage: "private on|off",
			description: "hide the seat this session holds from every other project's who, or show it again",
			positional:  func(req *request) error { return exactly(1, &req.Value)(req) },
			run:         (*memory).private,
			schema: `{"type":"object","properties":{"value":{"type":"string","enum":["on","off"],` +
				`"description":"on hides the seat from other projects, off shows it"}},"required":["value"]}`,
		},
		{
			name: "release", usage: "release [<seat>]", description: "give a seat up",
			positional: optionalName, run: (*memory).release,
			schema: `{"type":"object","properties":{"name":{"type":"string","description":"seat to give up; own unless given"}}}`,
		},
		{
			name: "lifetime", cliOnly: true, usage: "lifetime durable|temporary|<span>",
			description: "make the shelf of the seat this session holds durable, or temporary: purged the span (default temporary_seat_lifetime) after its last seat closes",
			positional:  func(req *request) error { return exactly(1, &req.Value)(req) },
			run:         (*memory).lifetime,
			schema: `{"type":"object","properties":{"value":{"type":"string",` +
				`"description":"durable, temporary, or a span such as 90m, 12h, 2d, 1w or 0"}},"required":["value"]}`,
		},
		{
			name: "drop", cliOnly: true, usage: "drop <shelf>", description: "purge a temporary shelf no seat uses, with its seats and entries, at once",
			positional: func(req *request) error { return exactly(1, &req.Shelf)(req) },
			run:        (*memory).drop,
			schema:     `{"type":"object","properties":{"shelf":{"type":"string","description":"the temporary shelf to purge"}},"required":["shelf"]}`,
		},
	}
}

// linkPositional is the entry id and its target, or the entry id alone where
// a tool is the target.
func linkPositional(req *request) error {
	if len(req.Tools) > 0 {
		return exactly(1, &req.ID)(req)
	}

	return exactly(2, &req.ID, &req.Target)(req)
}
