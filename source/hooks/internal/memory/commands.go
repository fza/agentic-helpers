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
	Retention      string   `json:"retention,omitempty"`
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
	Absorbs        []string `json:"absorbs,omitempty"`
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
	JSON           bool     `json:"json,omitempty"`
	NoHeat         bool     `json:"no_heat,omitempty"`

	given map[string]bool
}

type result struct {
	text   string
	code   int
	failed bool
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
		"  dump <segment>|--tail                                       a log segment or the tail, one event per line")

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
		"title": &req.Title, "retention": &req.Retention, "from": &req.From, "query": &req.Query,
		"by": &req.By, "because": &req.Because, "session": &req.Session, "shelf": &req.Shelf,
		"lifetime": &req.Lifetime,
	}
	lists := map[string]*[]string{
		"topic": &req.Topics, "about": &req.About, "term": &req.Terms, "seat": &req.Seats, "status": &req.Statuses,
		"for": &req.For, "link": &req.Links, "absorbs": &req.Absorbs,
	}
	switches := map[string]*bool{
		"all-seats": &req.AllSeats, "with-superseded": &req.WithSuperseded, "json": &req.JSON,
		"no-heat": &req.NoHeat, "all": &req.All,
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

		req.Limit = number
	case flagCount:
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("--%s takes a number of 0 or more, not %q", spec.name, value)
		}

		req.Skip = number
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

func commands() []command {
	return []command{
		{
			name:        "new",
			usage:       "new --title '<3-10 words>' --topic <name>... [--for <seat>|all]... [--link <id>]... [--absorbs <id>]... [options]",
			description: "file an entry; body on stdin; --for shares it with other seats",
			flags: []flagSpec{
				{"title", flagText}, {"topic", flagList}, {"retention", flagText}, {"from", flagText},
				{"about", flagList}, {"for", flagList}, {"link", flagList}, {"absorbs", flagList},
			},
			readsBody: true,
			schema: `{"type":"object","properties":{` +
				`"title":{"type":"string","description":"3-10 words"},` +
				`"body":{"type":"string"},` +
				`"topic":{"type":"array","items":{"type":"string"}},` +
				`"retention":{"type":"string","enum":["standing","long","normal","short","scratch"]},` +
				`"from":{"type":"string","enum":["user","agent","tool"]},` +
				`"about":{"type":"array","items":{"type":"string"}},` +
				`"for":{"type":"array","items":{"type":"string"},"description":"seats the entry is for besides the filer, or all"},` +
				`"link":{"type":"array","items":{"type":"string"},"description":"full graph ids or memory ids the entry links to"},` +
				`"absorbs":{"type":"array","items":` + idSchema + `,"description":"entries the new one supersedes"}},` +
				`"required":["title","body"]}`,
			run: (*memory).newEntry,
		},
		{
			name:        "search",
			usage:       "search [--term <regex>]... [--query <phrase>] [--status live|retired|superseded]... [--skip <n>] [options]",
			description: "keyword, semantic or hybrid search; with --status alone, a listing",
			flags: []flagSpec{
				{"term", flagList}, {"query", flagText}, {"topic", flagList}, {"seat", flagList},
				{"all-seats", flagSwitch}, {"with-superseded", flagSwitch}, {"limit", flagNumber},
				{"no-heat", flagSwitch}, {"status", flagList}, {"skip", flagCount},
			},
			schema: `{"type":"object","properties":{` +
				`"term":{"type":"array","items":{"type":"string"}},"query":{"type":"string"},` +
				`"topic":{"type":"array","items":{"type":"string"}},"seat":{"type":"array","items":{"type":"string"}},` +
				`"all_seats":{"type":"boolean"},"with_superseded":{"type":"boolean"},"limit":{"type":"integer","minimum":1},` +
				`"status":{"type":"array","items":{"type":"string","enum":["live","retired","superseded"]}},` +
				`"skip":{"type":"integer","minimum":0},` +
				`"no_heat":{"type":"boolean","description":"answer without adding heat"}}}`,
			run: (*memory).search,
		},
		{
			name: "show", usage: "show <id>... [--no-heat]", description: "an entry in full: stamps, anchors, flags, events",
			flags: []flagSpec{{"no-heat", flagSwitch}}, positional: someIDs, run: (*memory).show,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `},` +
				`"no_heat":{"type":"boolean","description":"answer without adding heat"}},"required":["ids"]}`,
		},
		{
			name: "brief", usage: "brief [--topic <name>]", description: "the brief, or one topic's entries",
			flags: []flagSpec{{"topic", flagList}}, run: (*memory).briefCommand,
			schema: `{"type":"object","properties":{"topic":{"type":"array","items":{"type":"string"}}}}`,
		},
		{
			name: "topics", usage: "topics", description: "every topic with live counts per seat",
			run: (*memory).topics, schema: `{"type":"object","properties":{}}`,
		},
		{
			name: "confirm", usage: "confirm <id>...", description: "re-anchor verified entries to the tree, all or none",
			positional: someIDs, run: (*memory).confirm,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `}},"required":["ids"]}`,
		},
		{
			name: "retain", usage: "retain <id>... <level>", description: "change entries' retention, all or none",
			positional: func(req *request) error {
				if len(req.Positional) < 2 {
					return errors.New("at least one entry id and a level expected")
				}

				req.IDs, req.Level = req.Positional[:len(req.Positional)-1], req.Positional[len(req.Positional)-1]

				return nil
			},
			run: (*memory).retain,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `},"level":{"type":"string",` +
				`"enum":["standing","long","normal","short","scratch"]}},"required":["ids","level"]}`,
		},
		{
			name: "retire", usage: "retire <id>... [--because '<why>']", description: "keep entries in results, flagged, out of the brief, all or none; --because says why they ended",
			flags: []flagSpec{{"because", flagText}}, positional: someIDs, run: (*memory).retire,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `},` +
				`"because":{"type":"string","description":"what finished the entries, or why they stopped mattering"}},"required":["ids"]}`,
		},
		{
			name: "supersede", usage: "supersede <id>... --by <id> | --because '<why>'",
			description: "take entries out of results, all or none",
			flags:       []flagSpec{{"by", flagText}, {"because", flagText}}, positional: someIDs, run: (*memory).supersede,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `},"by":` + idSchema +
				`,"because":{"type":"string"}},"required":["ids"]}`,
		},
		{
			name: "revive", usage: "revive <id>", description: "reverse the latest retire or supersede",
			positional: oneID, run: (*memory).revive,
			schema: `{"type":"object","properties":{"id":` + idSchema + `},"required":["id"]}`,
		},
		{
			name: "retitle", usage: "retitle <id> '<title>'", description: "give an entry a new title",
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Title)(req) },
			run:        (*memory).retitle,
			schema: `{"type":"object","properties":{"id":` + idSchema +
				`,"title":{"type":"string"}},"required":["id","title"]}`,
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
			run: (*memory).tag,
			schema: `{"type":"object","properties":{"id":` + idSchema +
				`,"topic":{"type":"array","items":{"type":"string"}}},"required":["id","topic"]}`,
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
			schema: `{"type":"object","properties":{"id":` + idSchema +
				`,"topics":{"type":"array","items":{"type":"string"}}},"required":["id","topics"]}`,
		},
		{
			name: "link", usage: "link <id> <target> [--no-heat]", description: "link an entry to a graph entry or another memory entry",
			flags:      []flagSpec{{"no-heat", flagSwitch}},
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Target)(req) }, run: (*memory).link,
			schema: `{"type":"object","properties":{"id":` + idSchema + `,"target":{"type":"string"},` +
				`"no_heat":{"type":"boolean","description":"link without adding heat to a memory target"}},"required":["id","target"]}`,
		},
		{
			name: "unlink", usage: "unlink <id> <target>", description: "drop a link made by --link or link",
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Target)(req) }, run: (*memory).unlink,
			schema: `{"type":"object","properties":{"id":` + idSchema + `,"target":{"type":"string"}},"required":["id","target"]}`,
		},
		{
			name: "trust", usage: "trust <path>", description: "let the checkout at path write this project's memory and tool library",
			positional: func(req *request) error { return exactly(1, &req.Path)(req) }, run: (*memory).trust,
			schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
		},
		{
			name: "who", usage: "who [--all]",
			description: "every seat of this project, held, reserved or free, with its shelf, freeing those whose holder is gone; " +
				"--all adds every other project's seats",
			flags: []flagSpec{{"all", flagSwitch}}, run: (*memory).who,
			schema: `{"type":"object","properties":{"all":{"type":"boolean","description":"add every other project's seats"}}}`,
		},
		{
			name: "follow", usage: "follow <topic>...", description: "receive every entry filed under these topics",
			positional: topicsOnly, run: (*memory).follow,
			schema: `{"type":"object","properties":{"topic":{"type":"array","items":{"type":"string"}}},"required":["topic"]}`,
		},
		{
			name: "unfollow", usage: "unfollow <topic>...", description: "stop receiving entries filed under these topics",
			positional: topicsOnly, run: (*memory).unfollow,
			schema: `{"type":"object","properties":{"topic":{"type":"array","items":{"type":"string"}}},"required":["topic"]}`,
		},
		{
			name: "merge", usage: "merge <from> <into>", description: "merge one topic into another, every seat",
			positional: func(req *request) error { return exactly(2, &req.FromTopic, &req.Into)(req) },
			run:        (*memory).merge,
			schema: `{"type":"object","properties":{"from_topic":{"type":"string"},` +
				`"into":{"type":"string"}},"required":["from_topic","into"]}`,
		},
		{
			name: "holder", usage: "holder <seat> [--json] | holder --session <id> --json",
			description: "who holds a seat; exit 0 held, 1 free",
			flags:       []flagSpec{{"json", flagSwitch}, {"session", flagText}}, positional: optionalName,
			run: (*memory).holder,
			schema: `{"type":"object","properties":{"name":{"type":"string"},"session":{"type":"string"},` +
				`"json":{"type":"boolean"}}}`,
		},
		{
			name: "claim", usage: "claim <seat> [--shelf <shelf>] [--lifetime durable|temporary|<span>] [--session <id>] | claim --shelf <shelf> [...]",
			description: "take a seat, on a shelf with --shelf, or a new seat on that shelf with no name; prints the brief and the slice; " +
				"--session claims it for that session: a reservation it takes over, or a handover where it holds a seat",
			flags:      []flagSpec{{"shelf", flagText}, {"lifetime", flagText}, {"session", flagText}},
			positional: optionalName,
			run:        (*memory).claim,
			schema: `{"type":"object","properties":{"name":{"type":"string"},` +
				`"shelf":{"type":"string","description":"the shelf a new seat files onto; alone, claims a new seat on it"},` +
				`"lifetime":{"type":"string","description":"durable, temporary, or a span such as 90m, 12h, 2d or 0"},` +
				`"session":{"type":"string","description":"the session to claim the seat for, other than the caller's"}}}`,
		},
		{
			name: "rename", usage: "rename '<new name>'", description: "give the seat this session holds a new name; its id and entries stay",
			positional: func(req *request) error { return exactly(1, &req.Name)(req) },
			run:        (*memory).rename,
			schema:     `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`,
		},
		{
			name: "release", usage: "release [<seat>]", description: "give a seat up",
			positional: optionalName, run: (*memory).release,
			schema: `{"type":"object","properties":{"name":{"type":"string"}}}`,
		},
		{
			name: "lifetime", usage: "lifetime durable|temporary|<span>",
			description: "make the shelf of the seat this session holds durable, or temporary: purged the span (default temporary_seat_lifetime) after its last seat closes",
			positional:  func(req *request) error { return exactly(1, &req.Value)(req) },
			run:         (*memory).lifetime,
			schema: `{"type":"object","properties":{"value":{"type":"string",` +
				`"description":"durable, temporary, or a span such as 90m, 12h, 2d or 0"}},"required":["value"]}`,
		},
		{
			name: "drop", usage: "drop <shelf>", description: "purge a temporary shelf no seat uses, with its seats and entries, at once",
			positional: func(req *request) error { return exactly(1, &req.Shelf)(req) },
			run:        (*memory).drop,
			schema:     `{"type":"object","properties":{"shelf":{"type":"string"}},"required":["shelf"]}`,
		},
	}
}
