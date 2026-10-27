package shellguard_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const rewriteNote = "; rewrite the command (attempt %d of 3)"

type rewritableAsk struct {
	name    string
	command string
	reason  string
}

func rewritableAsks(t *testing.T) []rewritableAsk {
	t.Helper()

	credential := carrying(test.SecretFixture(t, "positive/github-pat.txt"))

	return []rewritableAsk{
		{"a command name that is not fixed text", askedName, "agentic-shell-guard: cannot judge: the command name $CMD is not fixed text"},
		{"a launcher value that may split", "sudo -u $user make build", "agentic-shell-guard: cannot judge: the command name $user is not fixed text"},
		{"a command name that may split alone", "$CMD", "agentic-shell-guard: cannot judge: the command name $CMD is not fixed text"},
		{"a credential", credential, credentialReason + `generic-api-key, github-pat, line 2); history, logs and files keep it; reference it instead: "$NAME" or a file`},
	}
}

func TestARewritableAskRefusesTwiceThenPrompts(t *testing.T) {
	for _, tc := range rewritableAsks(t) {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuard(t, false, "").inSession(test.Session)

			for attempt := 1; attempt <= 2; attempt++ {
				decision, reason := g.judge(tc.command, "{project}")
				if decision != "deny" || reason != tc.reason+fmt.Sprintf(rewriteNote, attempt) {
					t.Errorf("an early attempt should be refused with its reason and count, got %s %q", decision, reason)
				}
			}

			for range 2 {
				decision, reason := g.judge(tc.command, "{project}")
				if decision != "ask" || reason != tc.reason {
					t.Errorf("from the third attempt the person should be asked, got %s %q", decision, reason)
				}
			}
		})
	}
}

func TestTheThirdAttemptFollowsThePermissionMode(t *testing.T) {
	for _, tc := range rewritableAsks(t) {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuard(t, false, "").inSession(test.Session)

			for range 2 {
				g.judgeIn(tc.command, "dontAsk")
			}

			decision, reason := g.judgeIn(tc.command, "dontAsk")
			if decision != "deny" || reason != tc.reason {
				t.Errorf("dontAsk should deny the third attempt with the guard's reason alone, got %s %q", decision, reason)
			}
		})
	}
}

func TestEachSessionAndReasonCountsApart(t *testing.T) {
	credential := carrying(test.SecretFixture(t, "positive/github-pat.txt"))

	g := newGuard(t, false, "").inSession(test.Session)
	for range 2 {
		g.judge(askedName, "{project}")
	}

	if decision, reason := g.judge(credential, "{project}"); !strings.HasSuffix(reason, "(attempt 1 of 3)") {
		t.Errorf("another reason should count from its own first attempt, got %s %q", decision, reason)
	}

	other := g.inSession(test.OtherSession)
	if _, reason := other.judge(askedName, "{project}"); !strings.HasSuffix(reason, "(attempt 1 of 3)") {
		t.Errorf("a new session should count from its first attempt, got %q", reason)
	}

	if decision, _ := g.judge(askedName, "{project}"); decision != "ask" {
		t.Errorf("the first session should keep its own count, got %s", decision)
	}
}

func TestASessionWithoutACountIsOnItsFirstAttempt(t *testing.T) {
	cases := []struct {
		name  string
		guard func(g guard) guard
	}{
		{"no session id", func(g guard) guard { return g.inSession("") }},
		{"a session id that is no file name", func(g guard) guard { return g.inSession("../escape") }},
		{"no state directory", func(g guard) guard {
			g = g.inSession(test.Session)
			g.env.StateDir = ""

			return g
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.guard(newGuard(t, false, ""))

			for range 4 {
				_, reason := g.judge(askedName, "{project}")
				if reason != "agentic-shell-guard: cannot judge: the command name $CMD is not fixed text"+fmt.Sprintf(rewriteNote, 1) {
					t.Errorf("a call with no count to keep should be refused as a first attempt, got %q", reason)
				}
			}
		})
	}
}

func TestAnAskNoRewritingFixesPromptsAtOnce(t *testing.T) {
	for _, command := range []string{"rm ~/x", "find / -delete", askedRemoval, askedFind} {
		t.Run(command, func(t *testing.T) {
			decision, reason := newGuard(t, false, "").inSession(test.Session).judge(command, "{project}")
			if decision != "ask" {
				t.Errorf("the first attempt should prompt, got %s %q", decision, reason)
			}
		})
	}
}

func TestARewritableAskSettingDecidesEveryTime(t *testing.T) {
	cases := []struct {
		options string
		want    string
	}{
		{`"shell_guard_outside_project":"deny"`, "deny"},
		{`"shell_guard_outside_project":"off"`, "allow"},
	}

	for _, tc := range cases {
		t.Run(tc.options, func(t *testing.T) {
			g := newGuard(t, false, tc.options).inSession(test.Session)

			for range 4 {
				decision, reason := g.judge(askedName, "{project}")
				if decision != tc.want || reason != "" && reason != "agentic-shell-guard: cannot judge: the command name $CMD is not fixed text" {
					t.Errorf("the setting should decide every attempt, uncounted, got %s %q", decision, reason)
				}
			}
		})
	}
}

func TestTheCountAndTheCountdownsShareTheStateFile(t *testing.T) {
	t.Run("a countdown keeps the count", func(t *testing.T) {
		c := newCountdown(t, "")
		g := c.guard.inSession(c.session)

		for range 2 {
			g.judge(askedName, "{project}")
		}

		c.request(context.Background(), askedFind, "auto")

		if decision, reason := g.judge(askedName, "{project}"); decision != "ask" {
			t.Errorf("a countdown noted between attempts should keep their count, got %s %q", decision, reason)
		}
	})

	t.Run("a count keeps the countdowns", func(t *testing.T) {
		c := newCountdown(t, "")
		g := c.guard.inSession(c.session)

		for range 3 {
			c.request(context.Background(), askedFind, "auto")
		}

		g.judge(askedName, "{project}")

		said := c.request(context.Background(), askedFind, "auto")
		if !strings.HasPrefix(said, "agentic-shell-guard: 3 countdowns ran out") {
			t.Errorf("a refusal counted between countdowns should keep them, got %q", said)
		}
	})
}
