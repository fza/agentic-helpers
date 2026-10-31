package shellguard

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
)

func bundledBound(t *testing.T) alternatives.Alternative {
	t.Helper()

	entry, _ := alternatives.Lookup("timeout")
	for _, alternative := range entry.Alternatives {
		if alternative.Bundled && alternative.Subcommand == "bound" {
			return alternative
		}
	}

	t.Fatal("timeout should have the plugin's bound among its alternatives")

	return alternatives.Alternative{}
}

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
		"nobody answered within <span>; <reason>",
		fmt.Sprintf("%d countdowns ran out unanswered since the last prompt; <reason>", unansweredLimit),
		"`" + modeDontAsk + "`", "`" + modeAuto + "`", "`" + modeBypass + "`",
		strings.NewReplacer("%s", "<rule ids>", "%d", "<n>").Replace(reasonSecrets), "shell_guard_secrets",
		reasonSecretsCut, fmt.Sprintf(reasonNotFixed, "<word>"),
		fmt.Sprintf(strings.Replace(noteRewrite, "%d", "<n>", 1), "<reason>", rewriteLimit),
		fmt.Sprintf(markerWithheld, "<rule ids>"),
		strings.NewReplacer("%s", "<tool>", "%d", "<n>").Replace(noteScanUnfinished),
		self + ": " + reasonOutputUnread, self + ": " + reasonOutputFailure, self + ": " + reasonOutputAnswer,
		"shell_guard_secret_output", "shell_guard_secret_output_skip", "agentic-shell-guard output",
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadMissing, "timeout"), "<rewritten command>"),
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadMissing, "timeout"), generic(bundledBound(t))),
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadDialect, "<command>", alternatives.FlavourBSD, "<command>", "<usage>"),
			"<rewritten command>"),
		fmt.Sprintf(leadDialect, "<command>", alternatives.FlavourBSD, "<command>", "<usage>") + "; " +
			fmt.Sprintf(insteadForm, "<form>"),
		"agentic-helpers: this machine: ", "shell_guard_alternatives", "124",
	}

	for _, entry := range alternatives.Table {
		for _, usage := range entry.Usages {
			quoted = append(quoted, usage.Says, usage.Instead)
		}
	}

	for _, reason := range quoted {
		if !strings.Contains(string(skill), reason) {
			t.Errorf("the skill should quote the refusal %q as the binary prints it", reason)
		}
	}
}
