package grounding

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const mcpNext = "mcp__sdd__next"

var (
	// `sdd new`, run by `agentic-capture`, prints the written entry's file name
	// on a line of its own.
	capturedEntry = regexp.MustCompile(`(?m)^\s*(\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]+)\.md\s*$`)

	// The result ending sdd's capture procedure carries `produced.entryId`,
	// as JSON or as JSON quoted inside a text block.
	producedEntry = regexp.MustCompile(`entryId\\*"\s*:\s*\\*"(\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]+)`)

	entryParts = regexp.MustCompile(`^\d{6}(\d{2})-(\d{6}-[sd]-[a-z]{3}-[a-z0-9]+)$`)
)

type bashResponse struct {
	Stdout string `json:"stdout"`
}

// keepAcrossOwnCapture moves a session's ledger to the graph state its own
// capture left, keeping the reads, when that capture is the graph's only
// change: one entry more, and the newest the one written. Any other change
// leaves the ledger for the next reader to reset, as every change did before.
func keepAcrossOwnCapture(ctx context.Context, env Env, payload hookio.Payload) {
	id := ownEntry(env, payload)
	if id == "" {
		return
	}

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return
	}

	held := loadEvidence(ctx, path)
	now := env.graphState()

	if !followsBy(held.Graph, now, id) {
		return
	}

	held.Graph = now
	_ = saveEvidence(ctx, env.graphRoot(), path, held)
}

func ownEntry(env Env, payload hookio.Payload) string {
	if payload.Failed() {
		return ""
	}

	if payload.ToolName == mcpNext {
		found := producedEntry.FindSubmatch(payload.ToolResponse)
		if found == nil {
			return ""
		}

		return string(found[1])
	}

	if payload.ToolName != "Bash" || writing(env, payload.ToolInput.Command) != writesThroughCapture {
		return ""
	}

	var response bashResponse

	err := json.Unmarshal(payload.ToolResponse, &response)
	if err != nil {
		return ""
	}

	found := capturedEntry.FindStringSubmatch(response.Stdout)
	if found == nil {
		return ""
	}

	return found[1]
}

// followsBy reports whether the graph went from before to now by the one
// entry id: graphState counts entry files and names the newest, whose file
// name is the id's day and time without year and month.
func followsBy(before, now, id string) bool {
	beforeCount, _, ok := splitState(before)
	if !ok {
		return false
	}

	nowCount, newest, ok := splitState(now)
	if !ok {
		return false
	}

	parts := entryParts.FindStringSubmatch(id)
	if parts == nil {
		return false
	}

	return nowCount == beforeCount+1 && newest == parts[1]+"-"+parts[2]+".md"
}

func splitState(state string) (int, string, bool) {
	count, newest, found := strings.Cut(state, ":")
	if !found {
		return 0, "", false
	}

	number, err := strconv.Atoi(count)
	if err != nil {
		return 0, "", false
	}

	return number, newest, true
}
