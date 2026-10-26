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
		"credential-relative.txt":    {"{where}", "<file>"},
		"credential-undecodable.txt": {"{tool}", "<tool>"},
	} {
		quoted := strings.TrimSpace(refusal(name, placeholders...))
		if !strings.Contains(string(skill), quoted) {
			t.Errorf("the skill should quote the refusal %q as the gate prints it", quoted)
		}
	}
}
