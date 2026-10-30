package memory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

// toolPrefix marks a link to a tool of the agentic-tools library. It rides
// the link event rather than an event kind of its own, which every older
// release reading the same log would refuse. The prefix lives in the log
// alone: the flag is `--tool`, since `--when tool:<name>` already names a
// Claude Code tool.
const toolPrefix = "tool:"

// toolBanner opens the tools `show` takes: their text is other agents', never
// the entry's own instruction.
const toolBanner = "tool (registered by other agents):"

func isToolLink(target string) bool { return strings.HasPrefix(target, toolPrefix) }

// toolLinks is every tool the entry names, in the form the library stores.
func (held *entry) toolLinks() []string {
	var named []string

	for _, link := range held.explicit {
		if isToolLink(link.target) {
			named = append(named, strings.TrimPrefix(link.target, toolPrefix))
		}
	}

	return named
}

// linkNote is how a change line names a link target: a tool by its name,
// without the store key a foreign one carries.
func linkNote(target string) string {
	if !isToolLink(target) {
		return target
	}

	name, _, _ := strings.Cut(strings.TrimPrefix(target, toolPrefix), "@")

	return "tool " + name
}

// linkedTools is the tool library, opened once and only for an entry naming
// a tool: opening it reads every store's index.
func (mem *memory) linkedTools(ctx context.Context) *tools.Linked {
	if mem.tools == nil {
		mem.tools = tools.OpenLinked(ctx, tools.Env{
			Base: mem.env.Base, Home: mem.env.Home, Roots: mem.env.Roots, OwnerName: mem.env.OwnerName,
			SessionID: mem.env.SessionID, Settings: mem.env.Settings, Getenv: mem.env.Getenv, Now: mem.env.Now,
			Processes: mem.env.Processes, Checkout: mem.env.Checkout, Git: mem.env.Git, Version: mem.env.Version,
		})
	}

	return mem.tools
}

// toolTargets is the link targets the tools given resolve to, or why one
// resolves to none.
func (mem *memory) toolTargets(ctx context.Context, given []string) ([]string, error) {
	targets := make([]string, 0, len(given))

	for _, name := range given {
		resolved, err := mem.linkedTools(ctx).Resolve(ctx, name)
		if err != nil {
			return nil, err
		}

		targets = append(targets, toolPrefix+resolved)
	}

	return targets, nil
}

// toolFlags is a line's flag per tool the entry names: `→ tool <name>`, and
// what became of a tool no longer in the library.
func (mem *memory) toolFlags(ctx context.Context, held *entry) []string {
	var flags []string

	for _, link := range held.toolLinks() {
		flag := "→ tool " + mem.linkedTools(ctx).Name(ctx, link)

		state := mem.linkedTools(ctx).State(ctx, link)
		if state != "" {
			flag += " " + state
		}

		flags = append(flags, flag)
	}

	return flags
}

// changeToolLink links an entry to one tool, or drops such a link: the tool
// named as the library resolves it, or as a line names it, which reaches a
// tool since gone.
func (mem *memory) changeToolLink(ctx context.Context, req request, kind memorylog.Kind) result {
	if req.Target != "" || len(req.Tools) != 1 {
		return refused("%s takes one target: an entry id, or one --tool", kind)
	}

	given := req.Tools[0]

	var target string

	if kind == memorylog.KindLink {
		targets, err := mem.toolTargets(ctx, req.Tools)
		if err != nil {
			return refused("%v", err)
		}

		target = targets[0]
	}

	if kind == memorylog.KindUnlink {
		folded, err := mem.load(ctx)
		if err != nil {
			return refused("%v", err)
		}

		if held, found := folded.entries[req.ID]; found {
			for _, link := range held.toolLinks() {
				if link == given || mem.linkedTools(ctx).Name(ctx, link) == given {
					target = toolPrefix + link
				}
			}
		}
	}

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(_ *state, held *entry) error {
		if target == "" {
			return fmt.Errorf("%s does not link to tool %s", held.id, given)
		}

		return nil
	}, kind, target, nil)
	if !ok {
		return failure
	}

	verb := "linked to"
	if kind == memorylog.KindUnlink {
		verb = "unlinked from"
	}

	return done(fmt.Sprintf("%s %s tool %s", req.ID, verb, given)).with(linkFields{ID: req.ID, Target: "tool " + given})
}

// takenTools takes every tool the entry names, as `show` does.
func (mem *memory) takenTools(ctx context.Context, held *entry, again bool) []tools.LinkedTake {
	links := held.toolLinks()
	if len(links) == 0 {
		return nil
	}

	return mem.linkedTools(ctx).Take(ctx, links, again)
}

// toolSection is the tools `show` took, each with where its copy lies and
// its usage, once per context.
func (mem *memory) toolSection(here tree, takes []tools.LinkedTake) []string {
	if len(takes) == 0 {
		return nil
	}

	lines := []string{toolBanner}

	for _, take := range takes {
		if take.NotTaken != "" {
			lines = append(lines, fmt.Sprintf("  %s: not taken: %s", take.Name, take.NotTaken))

			continue
		}

		heading := fmt.Sprintf("  %s v%d", take.Name, take.Version)
		if take.Seen {
			heading += " (seen)"
		}

		lines = append(lines, heading+"  taken: "+relativeCopy(here, take.Copy))

		if take.Seen {
			continue
		}

		if take.Foreign {
			lines = append(lines, "    "+tools.ForeignMark)
		}

		parts := strings.Split(strings.TrimRight(take.Usage, "\n"), "\n")
		if strings.TrimSpace(take.Usage) != "" {
			lines = append(lines, "    usage: "+parts[0])

			for _, part := range parts[1:] {
				lines = append(lines, "      "+part)
			}
		}
	}

	return lines
}

// relativeCopy is a copy's path from the checkout's root where it lies inside
// it, else in full.
func relativeCopy(here tree, copied string) string {
	relative, err := filepath.Rel(here.root, copied)
	if here.root == "" || err != nil || strings.HasPrefix(relative, "..") {
		return copied
	}

	return relative
}

type jsonTool struct {
	Name     string `json:"name"`
	State    string `json:"state,omitempty"`
	Version  int    `json:"version,omitempty"`
	Path     string `json:"path,omitempty"`
	Usage    string `json:"usage,omitempty"`
	Foreign  bool   `json:"foreign,omitempty"`
	Seen     bool   `json:"seen,omitempty"`
	NotTaken string `json:"not_taken,omitempty"`
}

// toolsJSON is each tool a line's flags name, with what became of it.
func (held *entry) toolsJSON() []jsonTool {
	var named []jsonTool

	for _, flag := range held.flags() {
		rest, isTool := strings.CutPrefix(flag, "→ tool ")
		if !isTool {
			continue
		}

		name, state, _ := strings.Cut(rest, " ")
		named = append(named, jsonTool{Name: name, State: state})
	}

	return named
}

// takenJSON is the tools `show` took, as JSON.
func takenJSON(here tree, takes []tools.LinkedTake) []jsonTool {
	var taken []jsonTool

	for _, take := range takes {
		held := jsonTool{Name: take.Name, Version: take.Version, Foreign: take.Foreign, Seen: take.Seen,
			NotTaken: take.NotTaken}
		if take.Copy != "" {
			held.Path = relativeCopy(here, take.Copy)
		}

		if !take.Seen {
			held.Usage = take.Usage
		}

		taken = append(taken, held)
	}

	return taken
}
