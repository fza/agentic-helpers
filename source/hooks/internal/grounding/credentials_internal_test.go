package grounding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

// Claude Code hands a hook a tool's input as a JSON object, so input that is
// no object reaches the scan only through this call, never through Main.
func TestAnEngineInputThatIsNoObjectIsRefused(t *testing.T) {
	for _, tool := range []string{mcpNext, mcpStartProcedure, mcpStageAttachment} {
		t.Run(tool, func(t *testing.T) {
			payload := hookio.Payload{ToolName: tool, ToolInput: hookio.ToolInput{Raw: json.RawMessage(`["report"]`)}}

			leaked, unscanned := leaksCredential(context.Background(), Env{}, payload)
			if leaked != "" || !strings.HasPrefix(unscanned, "GROUNDING GATE: refused. The "+tool+" call's input cannot be read to scan for a credential") {
				t.Errorf("input the gate cannot read should be refused as unscanned, got %q / %q", leaked, unscanned)
			}
		})
	}
}
