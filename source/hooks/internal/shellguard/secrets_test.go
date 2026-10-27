package shellguard_test

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAnInterruptDuringTheScanIsNoCredential(t *testing.T) {
	const cutShort = "the hook was cut short before its credential scan finished"

	cases := []struct {
		secrets  string
		decision shellguard.Decision
	}{
		{secrets: "ask", decision: shellguard.Ask},
		{secrets: "deny", decision: shellguard.Deny},
		{secrets: "off", decision: shellguard.Allow},
	}

	sample := test.SecretFixture(t, "positive/github-pat.txt")

	for _, tc := range cases {
		for _, command := range []string{"echo token=x", carrying(sample)} {
			t.Run(tc.secrets+" "+command[:4], func(t *testing.T) {
				cancelled, cancel := context.WithCancel(context.Background())
				cancel()

				options := shellguard.Options{Secrets: tc.secrets, OutsideProject: "ask", FindDelete: "ask"}

				verdict := shellguard.Judge(cancelled, command, t.TempDir(), shellguard.Env{}, options)
				if verdict.Decision != tc.decision || strings.Contains(verdict.Reason, "looks like a credential") {
					t.Errorf("an interrupted scan should take the secrets guard's verdict without naming a credential, got %+v", verdict)
				}

				if tc.decision != shellguard.Allow && !strings.Contains(verdict.Reason, cutShort) {
					t.Errorf("an interrupted scan should say the scan was cut short, got %+v", verdict)
				}
			})
		}
	}
}

const credentialReason = "agentic-shell-guard: the command holds what looks like a credential ("

// carrying is a command holding the text where no other guard looks: a
// here-document body no shell runs.
func carrying(text string) string {
	return "cat > /dev/null <<'END-OF-SAMPLE'\n" + text + "\nEND-OF-SAMPLE"
}

func samples(t *testing.T, directory string) map[string]string {
	t.Helper()

	tree := test.SecretFixtures(t)

	entries, err := fs.ReadDir(tree, directory)
	if err != nil {
		t.Fatalf("listing the %s samples: %v", directory, err)
	}

	found := map[string]string{}
	for _, entry := range entries {
		found[entry.Name()] = test.SecretFixture(t, path.Join(directory, entry.Name()))
	}

	return found
}

func TestACommandHoldingACredentialIsJudgedByItsOption(t *testing.T) {
	modes := []struct {
		options string
		want    string
	}{
		{options: "", want: "ask"},
		{options: `"shell_guard_secrets":"ask"`, want: "ask"},
		{options: `"shell_guard_secrets":"deny"`, want: "deny"},
		{options: `"shell_guard_secrets":"off"`, want: "allow"},
	}

	for name, sample := range samples(t, "positive") {
		for _, tc := range modes {
			t.Run(name+" "+tc.options, func(t *testing.T) {
				decision, reason := newGuard(t, false, tc.options).judge(carrying(sample), "{project}")
				if decision != tc.want {
					t.Errorf("a credential should be judged as shell_guard_secrets says, got %s: %s", decision, reason)
				}

				if tc.want != "allow" && !strings.HasPrefix(reason, credentialReason) {
					t.Errorf("the reason should name the credential, got %q", reason)
				}
			})
		}
	}
}

func TestTheCredentialReasonNamesRuleAndLine(t *testing.T) {
	_, reason := newGuard(t, false, "").judge(carrying(test.SecretFixture(t, "positive/github-pat.txt")), "{project}")

	want := credentialReason + `generic-api-key, github-pat, line 2); history, logs and files keep it; reference it instead: "$NAME" or a file`
	if reason != want {
		t.Errorf("the reason should name the rule and the line it starts on, never the text, got %q", reason)
	}
}

func TestACommandHoldingNoCredentialRuns(t *testing.T) {
	cleared := samples(t, "negative")
	cleared["clean.txt"] = test.SecretFixture(t, "clean.txt")

	for name, sample := range cleared {
		t.Run(name, func(t *testing.T) {
			decision, reason := newGuard(t, false, "").judge(carrying(sample), "{project}")
			if decision != "allow" {
				t.Errorf("text holding no credential should run, got %s: %s", decision, reason)
			}
		})
	}
}

func TestACredentialAskFollowsThePermissionMode(t *testing.T) {
	command := carrying(test.SecretFixture(t, "positive/github-pat.txt"))

	decision, reason := newGuard(t, false, "").judgeIn(command, "dontAsk")
	if decision != "deny" || !strings.HasPrefix(reason, credentialReason) {
		t.Errorf("dontAsk should deny a credential ask with its reason, got %s: %s", decision, reason)
	}

	said := newCountdown(t, "").request(context.Background(), command, "auto")
	if !strings.HasPrefix(said, "agentic-shell-guard: nobody answered within 120s; the command holds what looks like a credential (") {
		t.Errorf("a credential ask nobody answers in auto mode should be denied, got %q", said)
	}
}

func TestALoneRemovalHoldingACredentialIsAsked(t *testing.T) {
	decision, reason := newGuard(t, false, "").judge("rm {project}/sub/ghp_IwYFIN7BydgwTrXtXdH03eNopxtfuT1OFgAY", "{project}")
	if decision != "ask" || !strings.HasPrefix(reason, credentialReason) {
		t.Errorf("a removal passing on its own should still be judged for a credential, got %s: %s", decision, reason)
	}
}

func TestAnotherGuardsAskKeepsItsReason(t *testing.T) {
	command := "find {project} -delete; " + test.SecretFixture(t, "positive/github-pat.txt")

	decision, reason := newGuard(t, false, "").judge(command, "{project}")
	if decision != "ask" || reason != "agentic-shell-guard: find -delete" {
		t.Errorf("the first ask found should give the reason, got %s: %s", decision, reason)
	}

	decision, reason = newGuard(t, false, `"shell_guard_secrets":"deny"`).judge(command, "{project}")
	if decision != "deny" || !strings.HasPrefix(reason, credentialReason) {
		t.Errorf("a deny should outrank an ask, got %s: %s", decision, reason)
	}
}
