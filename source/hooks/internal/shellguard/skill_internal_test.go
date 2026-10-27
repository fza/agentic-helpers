package shellguard

import (
	"fmt"
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
		"cannot read the code piped to <shell>; refusing",
		self + ": could not read the command; refusing", self + ": internal failure; refusing",
		self + ": could not answer; refusing",
		"nobody answered within <n>s; <reason>",
		fmt.Sprintf("%d countdowns ran out unanswered since the last prompt; <reason>", unansweredLimit),
		"`" + modeDontAsk + "`", "`" + modeAuto + "`", "`" + modeBypass + "`",
		strings.NewReplacer("%s", "<rule ids>", "%d", "<n>").Replace(reasonSecrets), "shell_guard_secrets",
		reasonSecretsCut, fmt.Sprintf(reasonNotFixed, "<word>"),
		fmt.Sprintf(strings.Replace(noteRewrite, "%d", "<n>", 1), "<reason>", rewriteLimit),
		fmt.Sprintf(markerWithheld, "<rule ids>"),
		strings.NewReplacer("%s", "<tool>", "%d", "<n>").Replace(noteScanUnfinished),
		strings.NewReplacer("%d", "<n>", "%s", "<tool>").Replace(noteWithheld),
		self + ": " + reasonOutputUnread, self + ": " + reasonOutputFailure, self + ": " + reasonOutputAnswer,
		"shell_guard_secret_output", "shell_guard_secret_output_skip", "agentic-shell-guard output",
	}

	for _, reason := range quoted {
		if !strings.Contains(string(skill), reason) {
			t.Errorf("the skill should quote the refusal %q as the binary prints it", reason)
		}
	}
}
