package grounding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

// sdd's MCP server reads and writes the graph the `sdd` command does, so its
// tools count and owe what the command's subcommands count and owe. The names
// and parameters are sdd's: a rename there stops the counting here.
const (
	mcpSearch         = "mcp__sdd__search"
	mcpView           = "mcp__sdd__view"
	mcpShow           = "mcp__sdd__show"
	mcpStartProcedure = "mcp__sdd__start_procedure"
)

// The depths sdd's MCP show reads where a call names none.
const (
	mcpDefaultDown = 1
	mcpDefaultUp   = 2
)

type mcpInput struct {
	Query     string   `json:"query"`
	Terms     []string `json:"terms"`
	Layout    string   `json:"layout"`
	IDs       []string `json:"ids"`
	Down      *int     `json:"down"`
	Up        *int     `json:"up"`
	Canonical string   `json:"canonical"`
}

func mcpArguments(payload hookio.Payload) mcpInput {
	var input mcpInput

	err := json.Unmarshal(payload.ToolInput.Raw, &input)
	if err != nil {
		return mcpInput{}
	}

	return input
}

func (input mcpInput) hasTerms() bool {
	for _, term := range input.Terms {
		if strings.TrimSpace(term) != "" {
			return true
		}
	}

	return false
}

// recordMCP credits the reads an MCP call performed. Only a call that
// succeeded reaches PostToolUse: a failed one fires PostToolUseFailure.
func recordMCP(ctx context.Context, env Env, payload hookio.Payload) {
	config, err := env.config()
	if err != nil {
		return
	}

	path, err := ledgerPath(env.ledgerDir(), payload)
	if err != nil {
		return
	}

	input := mcpArguments(payload)
	held := loadEvidence(ctx, path)
	held.against(env.graphState())
	changed := false

	if payload.ToolName == mcpSearch {
		semantic := strings.TrimSpace(input.Query) != ""
		literal := input.hasTerms()

		if semantic || literal {
			held.Search++
			changed = true
		}

		if semantic {
			held.Query++
		}

		if literal {
			held.Term++
		}
	}

	if payload.ToolName == mcpView {
		listing := "sdd view --layout '" + input.Layout + "'"
		for _, found := range readsOf(config).listingsIn(config.ListingPrefix, listing) {
			held.addListing(found)
			changed = true
		}
	}

	if changed {
		_ = saveEvidence(ctx, path, held)
	}
}

// gatesCapture reports whether the call starts sdd's capture procedure, the
// MCP way into the graph, which owes the reads `agentic-capture` owes.
func gatesCapture(payload hookio.Payload) bool {
	return payload.ToolName == mcpStartProcedure && mcpArguments(payload).Canonical == "capture"
}

// mcpShowTooShallow refuses an MCP show short of the depth the project owes,
// an omitted depth counting as the server's own default.
func mcpShowTooShallow(ctx context.Context, env Env, payload hookio.Payload, owed graphconfig.ShowDepth) string {
	input := mcpArguments(payload)

	down, up := mcpDefaultDown, mcpDefaultUp
	if input.Down != nil {
		down = *input.Down
	}

	if input.Up != nil {
		up = *input.Up
	}

	var short []string

	if owed.Down > 0 && down < owed.Down {
		short = append(short, fmt.Sprintf("`down %d`", down))
	}

	if owed.Up > 0 && up < owed.Up {
		short = append(short, fmt.Sprintf("`up %d`", up))
	}

	if len(short) == 0 {
		return ""
	}

	// A process-layer rules entry's downstream chain is every entry ever
	// captured under it, and reading that settles nothing.
	layers := map[string]bool{}
	for _, id := range input.IDs {
		for _, match := range entryID.FindAllStringSubmatch(id, -1) {
			layers[match[1]] = true
		}
	}

	if len(layers) == 1 && layers["prc"] {
		return ""
	}

	if env.exempt(ctx, owed.ExemptSeats) {
		return ""
	}

	var wanted []string

	if owed.Down > 0 {
		wanted = append(wanted, fmt.Sprintf(`"down": %d`, owed.Down))
	}

	if owed.Up > 0 {
		wanted = append(wanted, fmt.Sprintf(`"up": %d`, owed.Up))
	}

	call := fmt.Sprintf(`%s {"ids": [%s], "down": %d, "up": %d}`, mcpShow, quoted(input.IDs), down, up)

	return refusal("downstream-mcp.txt", "{missing}",
		strings.Join(short, " and ")+" reads short of `"+strings.Join(wanted, ", ")+"`.",
		"{command}", call, "{depth}", "`"+strings.Join(wanted, ", ")+"`")
}

func quoted(ids []string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%q", id))
	}

	return strings.Join(parts, ", ")
}
