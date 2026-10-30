package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
)

// request is one call's arguments, from the CLI or from MCP. A pointer field
// is nil where the call left it out, which update needs to carry fields over.
type request struct {
	Name     string   `json:"name"`
	Names    []string `json:"names"`
	Survivor string   `json:"survivor"`
	Members  []string `json:"members"`
	Path     string   `json:"path"`
	From     string   `json:"from"`
	Summary  *string  `json:"summary"`
	Usage    *string  `json:"usage"`
	Inputs   *string  `json:"inputs"`
	Outputs  *string  `json:"outputs"`
	Distinct string   `json:"distinct"`
	Why      string   `json:"why"`
	Query    string   `json:"query"`
	Terms    []string `json:"term"`
	Limit    int      `json:"limit"`
	Page     int      `json:"page"`
	JSON     bool     `json:"json"`
	Again    bool     `json:"again"`
}

type flagKind int

const (
	flagText flagKind = iota
	flagList
	flagNumber
	flagSwitch
)

type flagSpec struct {
	name string
	kind flagKind
}

type command struct {
	name        string
	usage       string
	description string
	flags       []flagSpec
	positional  func(req *request, values []string) error
	run         func(lib *library, ctx context.Context, req request) result
	schema      string
}

// text, names and number are a key's type, described by described.
const (
	text    = `{"type":"string"}`
	names   = `{"type":"array","items":{"type":"string"}}`
	number  = `{"type":"integer","minimum":1}`
	boolean = `{"type":"boolean"}`
)

// described is a key's schema with what the key is for.
func described(schema, description string) string {
	quoted, _ := json.Marshal(description)

	return strings.TrimSuffix(schema, "}") + `,"description":` + string(quoted) + "}"
}

var describing = []flagSpec{{"summary", flagText}, {"usage", flagText}, {"inputs", flagText}, {"outputs", flagText}}

var describingKeys = `,"summary":` + described(text, "what the tool does, one line") +
	`,"usage":` + described(text, "how to run it") + `,"inputs":` + described(text, "what it reads") +
	`,"outputs":` + described(text, "what it writes or prints")

func commands() []command {
	return []command{
		{
			name:        "find",
			usage:       "find [--query '<need>'] [--term '<literal>']... [--limit <n>] [--page <n>] [--again] [--json]",
			description: "search the tool library of every project on the machine, the own project first; one line per tool, its usage comes with take",
			flags: []flagSpec{{"query", flagText}, {"term", flagList}, {"limit", flagNumber}, {"page", flagNumber},
				{"json", flagSwitch}, {"again", flagSwitch}},
			run: (*library).find,
			schema: `{"type":"object","properties":{"query":` + described(text, "a need, matched by meaning") +
				`,"term":` + described(names, "literals, every one required") +
				`,"limit":` + described(number, "tools per page") + `,"page":` + described(number, "the page to answer, from 1") +
				`,"again":` + described(boolean, "print tools already seen in full") + `}}`,
		},
		{
			name: "list", usage: "list [--again] [--json]", description: "list the own project's tools and how many each other project holds",
			flags: []flagSpec{{"json", flagSwitch}, {"again", flagSwitch}}, run: (*library).list,
			schema: `{"type":"object","properties":{"again":` + described(boolean, "print tools already seen in full") + `}}`,
		},
		{
			name: "take", usage: "take <name>...", description: "copy the current version of each named tool into the checkout, all or none, printing each one's usage",
			positional: func(req *request, values []string) error {
				if len(values) == 0 {
					return errors.New("take needs at least one tool name")
				}

				req.Names = values

				return nil
			},
			run:    (*library).take,
			schema: `{"type":"object","properties":{"names":` + described(names, "tool names, an array even for one") + `},"required":["names"]}`,
		},
		{
			name:        "add",
			usage:       "add <name> --from <path> --summary '<one line>' --usage '<how to run it>' [--inputs '<...>'] [--outputs '<...>'] [--distinct '<how it differs>']",
			description: "register a new tool in the own project's library from a file or a folder, at most 1 MB in all; summary, usage and notes terse unless writing_style is plain",
			flags:       append([]flagSpec{{"from", flagText}, {"distinct", flagText}}, describing...),
			positional:  oneName,
			run:         (*library).add,
			schema: `{"type":"object","properties":{"name":` + described(text, "the new tool's name") +
				`,"from":` + described(text, "the file or folder holding the tool") + describingKeys +
				`,"distinct":` + described(text, "how it differs from a near-duplicate the library holds") +
				`},"required":["name","from","summary","usage"]}`,
		},
		{
			name:        "update",
			usage:       "update <name> [--from <path>] [--summary ...] [--usage ...] [--inputs ...] [--outputs ...] --why '<what changed>'",
			description: "write a tool's next version, at most 1 MB in all; a tool of another project is forked into this one first; summary, usage and notes terse unless writing_style is plain",
			flags:       append([]flagSpec{{"from", flagText}, {"why", flagText}}, describing...),
			positional:  oneName,
			run:         (*library).update,
			schema: `{"type":"object","properties":{"name":` + described(text, "the tool's name") +
				`,"from":` + described(text, "the file or folder holding the next version") + describingKeys +
				`,"why":` + described(text, "what changed") +
				`},"required":["name","why"]}`,
		},
		{
			name:        "merge",
			usage:       "merge <survivor> <member>... --from <path> --why '<reason>'",
			description: "fold near-duplicate tools of the own project into one, leaving the members as tombstones",
			flags:       []flagSpec{{"from", flagText}, {"why", flagText}},
			positional: func(req *request, values []string) error {
				if len(values) < 2 {
					return errors.New("merge needs a survivor and at least one member")
				}

				req.Survivor, req.Members = values[0], values[1:]

				return nil
			},
			run: (*library).merge,
			schema: `{"type":"object","properties":{"survivor":` + described(text, "the tool that stays") +
				`,"members":` + described(names, "the tools folded into it, an array even for one") +
				`,"from":` + described(text, "the file or folder holding the merged tool") +
				`,"why":` + described(text, "why they are one tool") + `},"required":["survivor","members","from","why"]}`,
		},
		{
			name: "reindex", usage: "reindex", description: "rebuild the own project's index.json from its tool folders",
			run: (*library).reindex, schema: `{"type":"object","properties":{}}`,
		},
	}
}

func oneName(req *request, values []string) error {
	if len(values) != 1 {
		return errors.New("one tool name expected")
	}

	req.Name = values[0]

	return nil
}

func helpText() string {
	lines := []string{"usage: " + self + " <subcommand>", ""}
	for _, held := range commands() {
		lines = append(lines, fmt.Sprintf("  %s\n      %s", held.usage, held.description))
	}

	lines = append(lines, "", fmt.Sprintf("A tool holds at most %d MB, every file together; every version of every "+
		"library on the machine at most tools_total_mb, the oldest versions neither current nor a fork's origin "+
		"pruned to make room.", pluginconfig.ToolMaxMB))

	return strings.Join(lines, "\n")
}

func commandNamed(name string) (command, bool) {
	for _, held := range commands() {
		if held.name == name {
			return held, true
		}
	}

	return command{}, false
}

// parseArgs reads a CLI call: `--flag value` or `--flag=value`, positionals
// anywhere.
func parseArgs(held command, args []string) (request, error) {
	var (
		req        request
		positional []string
	)

	for index := 0; index < len(args); index++ {
		arg := args[index]

		name, inline, hasInline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") {
			positional = append(positional, arg)

			continue
		}

		spec, known := flagOf(held, name)
		if !known {
			return req, fmt.Errorf("%s takes no --%s; usage: %s %s", held.name, name, self, held.usage)
		}

		if spec.kind == flagSwitch {
			req.JSON = req.JSON || name == "json"
			req.Again = req.Again || name == "again"

			continue
		}

		value := inline
		if !hasInline {
			if index+1 >= len(args) {
				return req, fmt.Errorf("--%s needs a value", name)
			}

			index++
			value = args[index]
		}

		err := req.set(spec, value)
		if err != nil {
			return req, err
		}
	}

	if held.positional == nil && len(positional) > 0 {
		return req, fmt.Errorf("%s takes no positional argument; usage: %s %s", held.name, self, held.usage)
	}

	if held.positional != nil {
		err := held.positional(&req, positional)
		if err != nil {
			return req, fmt.Errorf("%w; usage: %s %s", err, self, held.usage)
		}
	}

	return req, nil
}

func flagOf(held command, name string) (flagSpec, bool) {
	for _, spec := range held.flags {
		if spec.name == name {
			return spec, true
		}
	}

	return flagSpec{}, false
}

func (req *request) set(spec flagSpec, value string) error {
	switch spec.kind {
	case flagNumber:
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return fmt.Errorf("--%s takes a number of 1 or more, not %q", spec.name, value)
		}

		if spec.name == "limit" {
			req.Limit = parsed
		} else {
			req.Page = parsed
		}
	case flagList:
		req.Terms = append(req.Terms, value)
	default:
		texts := map[string]*string{"from": &req.From, "distinct": &req.Distinct, "why": &req.Why, "query": &req.Query}

		target, plain := texts[spec.name]
		if plain {
			*target = value

			return nil
		}

		given := value
		pointers := map[string]**string{"summary": &req.Summary, "usage": &req.Usage, "inputs": &req.Inputs,
			"outputs": &req.Outputs}
		*pointers[spec.name] = &given
	}

	return nil
}
