package shellguard

import (
	"os"
	"strings"
	"testing"
)

func TestTheSkillQuotesEveryRefusal(t *testing.T) {
	skill, err := os.ReadFile("../../../../plugin/skills/agent-shell-guard/SKILL.md")
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}

	quoted := []string{
		reasonMktemp, reasonMktempNoTmp, reasonNotAlone, reasonExpansion, reasonUnparsable, reasonFindDelete,
		labelCannotPlace, labelOutsideProject, labelNoProject, "unresolvable: ", "no target",
		"(" + whyNotAlone + ")", "(" + whyExpansion + ")", "(" + whyNestedCode + ")",
		"code handed to <runner> holds a removal or mktemp; refusing",
		"inline code for <interpreter> names a removal or mktemp; refusing",
		"cannot read the code handed to <runner>; refusing", "cannot parse the code handed to <runner>; refusing",
		self + ": could not read the command; refusing", self + ": internal failure; refusing",
		self + ": could not answer; refusing",
	}

	for _, reason := range quoted {
		if !strings.Contains(string(skill), reason) {
			t.Errorf("the skill should quote the refusal %q as the binary prints it", reason)
		}
	}
}
