package memory_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSkillAndDocsNameNoMCPJSONKey(t *testing.T) {
	for _, tree := range []string{"../../../../plugin/skills/agent-memory", "../../../../docs/memory"} {
		hits, err := test.MCPJSONKeyMentions(tree)
		if err != nil {
			t.Fatalf("reading %s: %v", tree, err)
		}

		for _, hit := range hits {
			t.Errorf("the skill and docs should name no MCP json key, since every answer is JSON: %s", hit)
		}
	}
}
