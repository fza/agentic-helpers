package shellguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/alternatives"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
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

const skillDirectory = "../../../../plugin/skills/agent-shell-guard"

// TestTheSkillQuotesEveryRefusal holds every reason an agent meets to the
// skill's core, SKILL.md, since an agent at a refusal reads the core first; the
// settings and figures a reason points to may live in a reference file.
func TestTheSkillQuotesEveryRefusal(t *testing.T) {
	skill, err := os.ReadFile(skillDirectory + "/SKILL.md")
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}

	references, err := filepath.Glob(skillDirectory + "/*.md")
	if err != nil {
		t.Fatalf("listing the skill's files: %v", err)
	}

	var whole strings.Builder

	for _, reference := range references {
		content, err := os.ReadFile(reference)
		if err != nil {
			t.Fatalf("reading %s: %v", reference, err)
		}

		whole.Write(content)
	}

	quoted := []string{
		reasonMktemp, reasonMktempNoTmp, reasonNotAlone, reasonExpansion, reasonUnparsable, reasonFindDelete,
		labelCannotPlace, labelOutsideProject, labelNoProject, "unresolvable: ", "no target",
		"(" + whyNotAlone + ")", "(" + whyExpansion + ")", "(" + whyNestedCode + ")",
		"code handed to <runner> holds a removal or mktemp; refusing",
		"inline code for <interpreter> names a removal or mktemp; refusing",
		"cannot read the code handed to <runner>; refusing", "cannot parse the code handed to <runner>; refusing",
		"cannot read the code piped to <shell>; refusing",
		fmt.Sprintf(reasonUnseen, "<name>"),
		fmt.Sprintf(reasonScriptUnread, "<name>", "<why>"), fmt.Sprintf(reasonInScript, "<name>", "<reason>"),
		unreadUnparsed, shellwalk.UnreadMissing, shellwalk.UnreadOpen, shellwalk.UnreadBuilt, shellwalk.UnreadDirectory,
		shellwalk.UnreadLarge, shellwalk.UnreadWritten, shellwalk.UnreadDeep,
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
		"agentic-shell-guard output",
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadMissing, "timeout"), "<rewritten command>"),
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadMissing, "timeout"), generic(bundledBound(t))),
		fmt.Sprintf(reasonUse, fmt.Sprintf(leadDialect, "<command>", alternatives.FlavourBSD, "<command>", "<usage>"),
			"<rewritten command>"),
		fmt.Sprintf(leadDialect, "<command>", alternatives.FlavourBSD, "<command>", "<usage>") + "; " +
			fmt.Sprintf(insteadForm, "<form>"),
		"agentic-helpers: this machine: ",
	}

	for _, entry := range alternatives.Table {
		for _, usage := range entry.Usages {
			quoted = append(quoted, usage.Says, usage.Instead)
		}
	}

	for _, reason := range quoted {
		if !strings.Contains(string(skill), reason) {
			t.Errorf("the skill's core should quote the refusal %q as the binary prints it", reason)
		}
	}

	for _, wording := range []string{
		"shell_guard_secret_output", "shell_guard_secret_output_skip", "shell_guard_alternatives", "124",
		fmt.Sprintf(reasonInScript, "x.fish", "inline code for fish names a removal or mktemp; refusing"),
	} {
		if !strings.Contains(whole.String(), wording) {
			t.Errorf("the skill or one of its reference files should quote %q as the binary prints it", wording)
		}
	}
}
