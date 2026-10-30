package grounding

import (
	"os"
	"strings"
	"testing"
)

func TestTheSkillQuotesEveryCredentialRefusal(t *testing.T) {
	skill, err := os.ReadFile("../../../../plugin/skills/agent-grounding/SKILL.md")
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}

	for name, placeholders := range map[string][]string{
		"credential.txt":             {"{rules}", "<rule ids>", "{where}", "<where>", "{line}", "<n>"},
		"credential-unreadable.txt":  {"{where}", "<file>"},
		"credential-stdin.txt":       {"{name}", "<name>"},
		"credential-no-draft.txt":    {},
		"credential-unresolved.txt":  {"{where}", "<file>", "{why}", "<why>"},
		"credential-undecodable.txt": {"{tool}", "<tool>"},
		"credential-interrupted.txt": {},
	} {
		quoted := strings.TrimSpace(refusal(name, placeholders...))
		if !strings.Contains(string(skill), quoted) {
			t.Errorf("the skill should quote the refusal %q as the gate prints it", quoted)
		}
	}
}

func TestTheSkillsAndDocsQuoteTheStagedBodyRefusal(t *testing.T) {
	quoted, _, _ := strings.Cut(refusal("engine-handle.txt", "{handle}", "<handle>"), "\n")

	for _, path := range []string{
		"../../../../plugin/skills/agent-capture/SKILL.md",
		"../../../../plugin/skills/agent-grounding/SKILL.md",
		"../../../../docs/grounding-gate/usage.md",
	} {
		text, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		if !strings.Contains(strings.Join(strings.Fields(string(text)), " "), quoted) {
			t.Errorf("%s should quote the staged body refusal as the gate prints it", path)
		}
	}
}
