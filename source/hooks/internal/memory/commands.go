package memory

import (
	"context"
	"errors"
	"fmt"
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
	Retention      string   `json:"retention,omitempty"`
	From           string   `json:"from,omitempty"`
	About          []string `json:"about,omitempty"`
	Terms          []string `json:"term,omitempty"`
	Query          string   `json:"query,omitempty"`
	Seats          []string `json:"seat,omitempty"`
	AllSeats       bool     `json:"all_seats,omitempty"`
	WithSuperseded bool     `json:"with_superseded,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	By             string   `json:"by,omitempty"`
	Because        string   `json:"because,omitempty"`
	Level          string   `json:"level,omitempty"`
	Into           string   `json:"into,omitempty"`
	FromTopic      string   `json:"from_topic,omitempty"`
	Name           string   `json:"name,omitempty"`
	Session        string   `json:"session,omitempty"`
	Force          bool     `json:"force,omitempty"`
	JSON           bool     `json:"json,omitempty"`
	NoHeat         bool     `json:"no_heat,omitempty"`

	given map[string]bool
}

type result struct {
	text   string
	code   int
	failed bool
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
		"by": &req.By, "because": &req.Because, "session": &req.Session,
	}
	lists := map[string]*[]string{"topic": &req.Topics, "about": &req.About, "term": &req.Terms, "seat": &req.Seats}
	switches := map[string]*bool{
		"all-seats": &req.AllSeats, "with-superseded": &req.WithSuperseded, "force": &req.Force, "json": &req.JSON,
		"no-heat": &req.NoHeat,
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
			usage:       "new --title '<3-10 words>' --topic <name>... [options]",
			description: "file an entry; body on stdin",
			flags: []flagSpec{
				{"title", flagText}, {"topic", flagList}, {"retention", flagText}, {"from", flagText},
				{"about", flagList},
			},
			readsBody: true,
			schema: `{"type":"object","properties":{` +
				`"title":{"type":"string","description":"3-10 words"},` +
				`"body":{"type":"string"},` +
				`"topic":{"type":"array","items":{"type":"string"}},` +
				`"retention":{"type":"string","enum":["standing","long","normal","short","scratch"]},` +
				`"from":{"type":"string","enum":["user","agent","tool"]},` +
				`"about":{"type":"array","items":{"type":"string"}}},"required":["title","body"]}`,
			run: (*memory).newEntry,
		},
		{
			name:        "search",
			usage:       "search [--term <regex>]... [--query <phrase>] [options]",
			description: "keyword, semantic or hybrid search",
			flags: []flagSpec{
				{"term", flagList}, {"query", flagText}, {"topic", flagList}, {"seat", flagList},
				{"all-seats", flagSwitch}, {"with-superseded", flagSwitch}, {"limit", flagNumber},
				{"no-heat", flagSwitch},
			},
			schema: `{"type":"object","properties":{` +
				`"term":{"type":"array","items":{"type":"string"}},"query":{"type":"string"},` +
				`"topic":{"type":"array","items":{"type":"string"}},"seat":{"type":"array","items":{"type":"string"}},` +
				`"all_seats":{"type":"boolean"},"with_superseded":{"type":"boolean"},"limit":{"type":"integer","minimum":1},` +
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
			name: "confirm", usage: "confirm <id>", description: "re-anchor a verified entry to the tree",
			positional: oneID, run: (*memory).confirm,
			schema: `{"type":"object","properties":{"id":` + idSchema + `},"required":["id"]}`,
		},
		{
			name: "retain", usage: "retain <id> <level>", description: "change an entry's retention",
			positional: func(req *request) error { return exactly(2, &req.ID, &req.Level)(req) },
			run:        (*memory).retain,
			schema: `{"type":"object","properties":{"id":` + idSchema + `,"level":{"type":"string",` +
				`"enum":["standing","long","normal","short","scratch"]}},"required":["id","level"]}`,
		},
		{
			name: "retire", usage: "retire <id>...", description: "keep entries in results, flagged, out of the brief",
			positional: someIDs, run: (*memory).retire,
			schema: `{"type":"object","properties":{"ids":{"type":"array","items":` + idSchema + `}},"required":["ids"]}`,
		},
		{
			name: "supersede", usage: "supersede <id> --by <id> | --because '<why>'",
			description: "take an entry out of results",
			flags:       []flagSpec{{"by", flagText}, {"because", flagText}}, positional: oneID, run: (*memory).supersede,
			schema: `{"type":"object","properties":{"id":` + idSchema + `,"by":` + idSchema +
				`,"because":{"type":"string"}},"required":["id"]}`,
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
			name: "claim", usage: "claim <seat> [--force]", description: "take a seat; prints the brief and the slice",
			flags: []flagSpec{{"force", flagSwitch}}, positional: func(req *request) error { return exactly(1, &req.Name)(req) },
			run: (*memory).claim,
			schema: `{"type":"object","properties":{"name":{"type":"string"},"force":{"type":"boolean"}},` +
				`"required":["name"]}`,
		},
		{
			name: "release", usage: "release [<seat>]", description: "give a seat up",
			positional: optionalName, run: (*memory).release,
			schema: `{"type":"object","properties":{"name":{"type":"string"}}}`,
		},
		{
			name: "sweep", usage: "sweep", description: "list every seat, free each whose holder is gone",
			run: (*memory).sweepCommand, schema: `{"type":"object","properties":{}}`,
		},
	}
}
