package tools_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// toolBudgets caps each tool's description in characters. The listing loads
// whenever an agent asks for a schema, and a caller without the skill forms its
// call from it, so no description is shortened to fit: each figure sits about
// a tenth above the description's size when the budget was set, rounded up to
// ten, so the tree passes with room for a clause. Raising one is a change of
// its own, stating what the added text earns.
var toolBudgets = []struct {
	tool   string
	budget int
	reason string
}{
	{"add", 170, "155 when set"},
	{"find", 340, "305 when set, the largest"},
	{"list", 250, "220 when set"},
	{"merge", 100, "88 when set"},
	{"reindex", 70, "58 when set"},
	{"take", 120, "101 when set"},
	{"update", 250, "226 when set"},
}

// keyBudget caps every key description: 60, about a tenth above add's
// distinct (54), the largest.
const keyBudget = 60

func listingLine(t *testing.T) string {
	t.Helper()

	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must2(lines, "mcp"), "\n") {
		if strings.Contains(line, `"id":2`) {
			return line
		}
	}

	t.Fatal("the server should answer the tool listing")

	return ""
}

func descriptionBreaches(t *testing.T, line string) []string {
	t.Helper()

	sizes, err := test.DescriptionSizes(line)
	if err != nil {
		t.Fatalf("measuring the descriptions: %v", err)
	}

	tools := map[string]int{}
	for _, entry := range toolBudgets {
		tools[entry.tool] = entry.budget
	}

	return test.OverBudget(sizes, test.DescriptionBudgets(sizes, tools, nil, keyBudget))
}

func TestEveryDescriptionFitsItsBudget(t *testing.T) {
	for _, breach := range descriptionBreaches(t, listingLine(t)) {
		t.Errorf("every tool and key description should fit its budget: %s", breach)
	}
}

func TestADescriptionPaddedPastItsBudgetIsCaught(t *testing.T) {
	line := listingLine(t)

	for _, padded := range []struct{ name, budget string }{
		{"find", "tool"},
		{"add.distinct", "key"},
	} {
		t.Run(padded.name, func(t *testing.T) {
			grown, err := test.PadDescription(line, padded.name, keyBudget)
			if err != nil {
				t.Fatalf("padding the description: %v", err)
			}

			breaches := descriptionBreaches(t, grown)
			if len(breaches) != 1 || !strings.HasPrefix(breaches[0], padded.name+":") {
				t.Errorf("a description padded past its %s budget should be the one breach reported, got: %v",
					padded.budget, breaches)
			}
		})
	}
}
