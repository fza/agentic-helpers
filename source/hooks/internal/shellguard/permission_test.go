package shellguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	askedRemoval = "rm -rf {outside}/build"
	askedFind    = "find {project} -name '*.o' -delete"
	askedName    = "$CMD -rf /"
)

func TestAnAskFollowsThePermissionMode(t *testing.T) {
	modes := []struct {
		mode string
		want string
	}{
		{mode: "", want: "ask"},
		{mode: "default", want: "ask"},
		{mode: "acceptEdits", want: "ask"},
		{mode: "plan", want: "ask"},
		{mode: "auto", want: "ask"},
		{mode: "bypassPermissions", want: "ask"},
		{mode: "dontAsk", want: "deny"},
	}

	// A rewritable ask prompts on its third attempt, the first two refused.
	refusedFirst := map[string]int{askedRemoval: 0, askedFind: 0, askedName: 2}

	for command, refused := range refusedFirst {
		for _, tc := range modes {
			t.Run(command+" in "+tc.mode, func(t *testing.T) {
				g := newGuard(t, false, "").inSession(test.Session)

				for range refused {
					g.judgeIn(command, tc.mode)
				}

				decision, reason := g.judgeIn(command, tc.mode)
				if decision != tc.want {
					t.Errorf("the ask should follow the permission mode, got %s: %s", decision, reason)
				}

				_, asked := g.judgeIn(command, "default")
				if reason != asked {
					t.Errorf("the reason should be the guard's own whatever the mode, got %q", reason)
				}
			})
		}
	}

	t.Run("a deny stays a deny", func(t *testing.T) {
		decision, _ := newGuard(t, false, "").judgeIn("mktemp", "default")
		if decision != "deny" {
			t.Errorf("a guard that denies should deny in any mode, got %s", decision)
		}
	})

	t.Run("a call nothing asks about runs", func(t *testing.T) {
		decision, _ := newGuard(t, false, "").judgeIn("ls", "dontAsk")
		if decision != "allow" {
			t.Errorf("a call no guard judges should run, got %s", decision)
		}
	})
}

// countdown is one PermissionRequest hook run.
type countdown struct {
	guard      guard
	clock      *test.CountdownClock
	transcript string
	session    string
}

func newCountdown(t *testing.T, options string) countdown {
	t.Helper()

	g := newGuard(t, false, options)
	clock := &test.CountdownClock{At: time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)}
	g.env.StateDir = t.TempDir()
	g.env.Clock = clock

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	test.WriteFile(t, transcript, test.Fixture(t, "shellguard/prompted-early.jsonl"))

	return countdown{guard: g, clock: clock, transcript: transcript, session: test.Session}
}

type permissionReply struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// request runs the hook and returns the deny message, empty where it left the
// prompt to the person.
func (c countdown) request(ctx context.Context, command string, mode string) string {
	c.guard.t.Helper()

	input, err := json.Marshal(map[string]any{
		"session_id": c.session, "transcript_path": c.transcript, "permission_mode": mode,
		"hook_event_name": "PermissionRequest", "tool_name": "Bash", "cwd": c.guard.tree.Project,
		"tool_input": map[string]string{"command": c.guard.placeholders.Replace(command)},
	})
	if err != nil {
		c.guard.t.Fatalf("encoding the request: %v", err)
	}

	var out, errs bytes.Buffer

	status := shellguard.Run(ctx, []string{"permission"}, c.guard.env, shellguard.Streams{In: bytes.NewReader(input), Out: &out, Err: &errs})
	if status != 0 {
		c.guard.t.Fatalf("the hook should leave the flow to its answer, exit %d: %s", status, errs.String())
	}

	if out.Len() == 0 {
		return ""
	}

	var decoded permissionReply

	err = json.Unmarshal(out.Bytes(), &decoded)
	if err != nil {
		c.guard.t.Fatalf("decoding the answer: %v", err)
	}

	if decoded.HookSpecificOutput.HookEventName != "PermissionRequest" || decoded.HookSpecificOutput.Decision.Behavior != "deny" {
		c.guard.t.Fatalf("the hook should only ever deny, got: %s", out.String())
	}

	return decoded.HookSpecificOutput.Decision.Message
}

func TestACountdownDeniesAnAskNobodyAnswers(t *testing.T) {
	for _, mode := range []string{"auto", "bypassPermissions"} {
		t.Run(mode, func(t *testing.T) {
			c := newCountdown(t, "")

			said := c.request(context.Background(), askedFind, mode)
			if said != "agentic-shell-guard: nobody answered within 120s; find -delete" {
				t.Errorf("the deny should name the countdown and the guard's reason, got %q", said)
			}

			if len(c.clock.Waited) != 1 || c.clock.Waited[0] != 120*time.Second {
				t.Errorf("the hook should wait out the default countdown first, waited %v", c.clock.Waited)
			}
		})
	}

	t.Run("a command name that is not fixed text", func(t *testing.T) {
		c := newCountdown(t, "")

		said := c.request(context.Background(), askedName, "auto")
		if said != "agentic-shell-guard: nobody answered within 120s; cannot judge: the command name $CMD is not fixed text" {
			t.Errorf("the deny should name the countdown and the unjudged name, got %q", said)
		}
	})

	t.Run("the countdown is the option's", func(t *testing.T) {
		c := newCountdown(t, `"shell_guard_ask_timeout":30`)

		said := c.request(context.Background(), askedRemoval, "auto")
		if !strings.HasPrefix(said, "agentic-shell-guard: nobody answered within 30s; outside project: ") {
			t.Errorf("the deny should name the option's countdown and the guard's reason, got %q", said)
		}
	})
}

func TestACountdownLeavesThePromptAlone(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name    string
		options string
		command string
		mode    string
		ctx     context.Context
	}{
		{name: "in default mode", command: askedFind, mode: "default", ctx: context.Background()},
		{name: "in acceptEdits mode", command: askedFind, mode: "acceptEdits", ctx: context.Background()},
		{name: "in plan mode", command: askedFind, mode: "plan", ctx: context.Background()},
		{name: "with no mode", command: askedFind, mode: "", ctx: context.Background()},
		{name: "with no limit", options: `"shell_guard_ask_timeout":0`, command: askedFind, mode: "auto", ctx: context.Background()},
		{name: "a call shell-guard lets run", command: "ls", mode: "auto", ctx: context.Background()},
		{name: "a call shell-guard denies", command: "mktemp", mode: "auto", ctx: context.Background()},
		{name: "a guard switched off", options: `"shell_guard_find_delete":"off"`, command: askedFind, mode: "auto", ctx: context.Background()},
		{name: "shell-guard off", options: `"shell_guard_enabled":false`, command: askedFind, mode: "auto", ctx: context.Background()},
		{name: "an answer before the countdown ends", command: askedFind, mode: "auto", ctx: cancelled},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCountdown(t, tc.options)

			said := c.request(tc.ctx, tc.command, tc.mode)
			if said != "" {
				t.Errorf("the hook should leave the prompt to the person, said %q", said)
			}

			if tc.ctx == context.Background() && len(c.clock.Waited) > 0 {
				t.Errorf("the hook should start no countdown, waited %v", c.clock.Waited)
			}
		})
	}
}

func TestThreeUnansweredCountdownsDenyAtOnce(t *testing.T) {
	c := newCountdown(t, "")

	for range 3 {
		c.request(context.Background(), askedFind, "auto")
	}

	said := c.request(context.Background(), askedRemoval, "auto")
	if !strings.HasPrefix(said, "agentic-shell-guard: 3 countdowns ran out unanswered since the last prompt; outside project: ") {
		t.Errorf("the fourth ask should deny with the reason it denies at once, got %q", said)
	}

	if len(c.clock.Waited) != 3 {
		t.Errorf("the fourth ask should start no countdown, waited %v", c.clock.Waited)
	}

	t.Run("until the next prompt", func(t *testing.T) {
		c.clock.At = time.Date(2026, 10, 26, 11, 0, 1, 0, time.UTC)
		test.WriteFile(t, c.transcript, test.Fixture(t, "shellguard/prompted-again.jsonl"))

		said := c.request(context.Background(), askedFind, "auto")
		if !strings.HasPrefix(said, "agentic-shell-guard: nobody answered within 120s; ") {
			t.Errorf("a prompt should start the count again, got %q", said)
		}

		if len(c.clock.Waited) != 4 {
			t.Errorf("the ask after a prompt should wait out its countdown, waited %v", c.clock.Waited)
		}
	})

	t.Run("each session counts its own", func(t *testing.T) {
		other := newCountdown(t, "")
		other.guard.env.StateDir = c.guard.env.StateDir
		other.session = test.OtherSession

		said := other.request(context.Background(), askedFind, "auto")
		if !strings.HasPrefix(said, "agentic-shell-guard: nobody answered within 120s; ") {
			t.Errorf("a fresh countdown state should wait, got %q", said)
		}
	})
}

func TestACountdownWithNowhereToNoteItKeepsWaiting(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(c *countdown)
	}{
		{name: "no state directory", prepare: func(c *countdown) { c.guard.env.StateDir = "" }},
		{name: "a session id that is no file name", prepare: func(c *countdown) { c.session = "../escape" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCountdown(t, "")
			tc.prepare(&c)

			for range 4 {
				said := c.request(context.Background(), askedFind, "auto")
				if !strings.HasPrefix(said, "agentic-shell-guard: nobody answered within 120s; ") {
					t.Errorf("every ask should wait out its countdown, got %q", said)
				}
			}
		})
	}
}

func TestTheHookArgumentsAreChecked(t *testing.T) {
	var out, errs bytes.Buffer

	status := shellguard.Run(context.Background(), []string{"sometimes"}, shellguard.Env{}, shellguard.Streams{
		In: strings.NewReader("{}"), Out: &out, Err: &errs,
	})
	if status != 2 || !strings.Contains(errs.String(), "usage: agentic-shell-guard [permission|output]") {
		t.Errorf("an unknown event should refuse with the usage, exit %d: %s", status, errs.String())
	}
}
