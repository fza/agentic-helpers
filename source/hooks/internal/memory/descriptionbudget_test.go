package memory_test

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
	{"ack", 170, "155 when set: the one-of rule between done, decline, undo, remove, add and order"},
	{"brief", 230, "206 when set: the fold rule"},
	{"claim", 110, "92 when set: the rule between name and shelf"},
	{"confirm", 50, "39 when set"},
	{"due", 80, "71 when set: the rule between due and clear"},
	{"landed", 110, "93 when set: that the entries stay"},
	{"link", 140, "121 when set: the rule between target and tool"},
	{"new", 110, "96 when set"},
	{"release", 20, "14 when set"},
	{"retire", 70, "62 when set"},
	{"retitle", 20, "16 when set"},
	{"search", 250, "225 when set: the rule between term, query and status, and the fold rule"},
	{"show", 300, "270 when set, the largest: the fold rule"},
	{"state", 100, "87 when set"},
	{"supersede", 130, "112 when set: the rule between by and because"},
	{"tag", 30, "22 when set"},
	{"topics", 40, "32 when set"},
	{"untag", 60, "49 when set"},
	{"view", 80, "71 when set: the rule between layout and explain"},
	{"who", 80, "66 when set"},
}

// keyBudget caps every key description but those in keyBudgets: 150, about a
// tenth above new's item (135) and until (130), the largest, which name the
// shapes they take since values are taken exactly.
const keyBudget = 150

// keyBudgets caps a key by its bare name on every tool carrying it. agent is
// on every tool, so its growth is paid once per tool listed: 30, three above
// its 27 characters when set ("set by the hook, never sent").
var keyBudgets = map[string]int{"agent": 30}

func listingLine(t *testing.T) string {
	t.Helper()

	h := newHarness(t)

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	for _, line := range strings.Split(h.must(lines, "mcp"), "\n") {
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

	return test.OverBudget(sizes, test.DescriptionBudgets(sizes, tools, keyBudgets, keyBudget))
}

func TestEveryDescriptionFitsItsBudget(t *testing.T) {
	for _, breach := range descriptionBreaches(t, listingLine(t)) {
		t.Errorf("every tool and key description should fit its budget: %s", breach)
	}
}

func TestADescriptionPaddedPastItsBudgetIsCaught(t *testing.T) {
	line := listingLine(t)

	// agent is padded past its own budget but not past the shared key budget, so
	// only its own figure catches it.
	for _, padded := range []struct {
		name, budget string
		padding      int
	}{
		{"show", "tool", keyBudget},
		{"new.item", "key", keyBudget},
		{"new.title", "key", keyBudget},
		{"claim.agent", "agent key", keyBudgets["agent"] / 2},
	} {
		t.Run(padded.name, func(t *testing.T) {
			grown, err := test.PadDescription(line, padded.name, padded.padding)
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

func TestAToolWithoutABudgetIsCaught(t *testing.T) {
	sizes := map[string]int{"show": 1, "unbudgeted": 1}
	breaches := test.OverBudget(sizes, test.DescriptionBudgets(sizes, map[string]int{"show": 300}, keyBudgets, keyBudget))

	if len(breaches) != 1 || !strings.Contains(breaches[0], "unbudgeted: 1 characters, no budget") {
		t.Errorf("a tool listed with no budget should be reported, got: %v", breaches)
	}
}
