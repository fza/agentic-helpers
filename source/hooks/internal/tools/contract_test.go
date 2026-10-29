package tools_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSkillAndDocsNameNoMCPJSONKey(t *testing.T) {
	for _, tree := range []string{"../../../../plugin/skills/agent-tools", "../../../../docs/tools"} {
		hits, err := test.MCPJSONKeyMentions(tree)
		if err != nil {
			t.Fatalf("reading %s: %v", tree, err)
		}

		for _, hit := range hits {
			t.Errorf("the skill and docs should name no MCP json key, since every answer is JSON: %s", hit)
		}
	}
}
