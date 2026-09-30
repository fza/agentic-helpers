package grounding_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/grounding"
)

const (
	gather  = "pilot graph gather"
	subject = "20260926-161520-d-tac-kvr"
)

type credited struct {
	Search   int      `json:"search"`
	Query    int      `json:"query"`
	Term     int      `json:"term"`
	Listings []string `json:"listings"`
}

func ledgerOf(t *testing.T, env grounding.Env) credited {
	t.Helper()

	held := credited{Listings: []string{}}

	data, err := os.ReadFile(filepath.Join(env.ProjectDir, ".sdd", "tmp", "grounding-gate", "s1.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return held
	}

	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}

	err = json.Unmarshal(data, &held)
	if err != nil {
		t.Fatalf("decoding the ledger: %v", err)
	}

	return held
}

func TestAGatherCallCreditsWhatItCarries(t *testing.T) {
	full := gather + " " + subject + " --query 'a host key that changed' --term ssh_host_key_changed --entry " + subject

	cases := []struct {
		name    string
		fixture string
		command string
		want    credited
	}{
		{name: "every read, the subject's areas", fixture: "gather-command.yaml", command: full, want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{"areas of " + subject}}},
		{name: "no term", fixture: "gather-command.yaml", command: gather + " s --query 'a subject' --entry " + subject, want: credited{Search: 1, Query: 1, Listings: []string{"areas of " + subject}}},
		{name: "no query", fixture: "gather-command.yaml", command: gather + " s --term x --term y --entry " + subject, want: credited{Search: 1, Term: 1, Listings: []string{"areas of " + subject}}},
		{name: "named areas", fixture: "gather-command.yaml", command: gather + " s --query q --term x --area area-hooks --area='area-bare host' --area \"area-docs\" --entry " + subject, want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{"area-hooks", "area-bare host", "area-docs"}}},
		{name: "an area without the prefix", fixture: "gather-command.yaml", command: gather + " s --query q --term x --area hooks --entry " + subject, want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{}}},
		{name: "no area and no subject", fixture: "gather-command.yaml", command: gather + " s --query q --term x", want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{}}},
		{name: "a flag quoted in a phrase", fixture: "gather-command.yaml", command: gather + " s --query 'why --term matters' --entry " + subject, want: credited{Search: 1, Query: 1, Listings: []string{"areas of " + subject}}},
		{name: "after a runner", fixture: "gather-command.yaml", command: "cd /x && env /usr/local/bin/" + full + " | head -40", want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{"areas of " + subject}}},
		{name: "in a subshell", fixture: "gather-command.yaml", command: "(" + gather + " s --query q --term x --area area-hooks)", want: credited{Search: 1, Query: 1, Term: 1, Listings: []string{"area-hooks"}}},
		{name: "a flag after the call ends", fixture: "gather-command.yaml", command: gather + " s --query q; echo --term x", want: credited{Search: 1, Query: 1, Listings: []string{}}},
		{name: "a mention", fixture: "gather-command.yaml", command: "echo '" + full + "'", want: credited{Listings: []string{}}},
		{name: "a mention after a separator", fixture: "gather-command.yaml", command: "echo 'run it; " + full + "'", want: credited{Listings: []string{}}},
		{name: "a heredoc body", fixture: "gather-command.yaml", command: "cat <<'EOF'\n" + full + "\nEOF", want: credited{Listings: []string{}}},
		{name: "a word before the parenthesis", fixture: "gather-command.yaml", command: "printf %s Bash(" + gather + " s --query q --term x --entry " + subject + ")", want: credited{Listings: []string{}}},
		{name: "a longer command word", fixture: "gather-command.yaml", command: gather + "er s --query q --term x --entry " + subject, want: credited{Listings: []string{}}},
		{name: "no gather command configured", fixture: "read-command.yaml", command: full, want: credited{Listings: []string{}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := configured(t, tc.fixture)
			record(t, env, bash(tc.command))

			got := ledgerOf(t, env)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("the call should be credited with exactly the reads it carries, got: %+v", got)
			}
		})
	}

	t.Run("a failed call", func(t *testing.T) {
		env := configured(t, "gather-command.yaml")
		record(t, env, with(bash(full), "tool_response", map[string]any{"is_error": true}))

		got := ledgerOf(t, env)
		if !reflect.DeepEqual(got, credited{Listings: []string{}}) {
			t.Errorf("a failed call should credit nothing, got: %+v", got)
		}
	})
}

func TestAGatherCallGroundsTheTurn(t *testing.T) {
	t.Run("every read in one call", func(t *testing.T) {
		env := configured(t, "gather-command.yaml")
		record(t, env, bash(gather+" "+subject+" --query 'a subject' --term x --entry "+subject))

		passes(t, run(t, env, "gate", ask("s1")))
	})

	t.Run("no term still owes the literal search", func(t *testing.T) {
		env := configured(t, "gather-command.yaml")
		record(t, env, bash(gather+" "+subject+" --query 'a subject' --entry "+subject))

		got := run(t, env, "gate", ask("s1"))
		refuses(t, got, "No `--term` search ran this turn.")

		if strings.Contains(got.stderr, "No `--query`") || strings.Contains(got.stderr, "listing ran") {
			t.Errorf("only the missing mode should be owed, got: %s", got.stderr)
		}

		record(t, env, bash("pilot graph search --term x"))
		passes(t, run(t, env, "gate", ask("s1")))
	})

	t.Run("an area without the prefix still owes the listing", func(t *testing.T) {
		env := configured(t, "gather-command.yaml")
		record(t, env, bash(gather+" s --query q --term x --area hooks --entry "+subject))

		refuses(t, run(t, env, "gate", ask("s1")), "No `area-` listing ran this turn.")
	})

	t.Run("a failed call", func(t *testing.T) {
		env := configured(t, "gather-command.yaml")
		record(t, env, with(bash(gather+" s --query q --term x --entry "+subject), "tool_response", map[string]any{"exit_code": 1}))

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})

	t.Run("no gather command configured", func(t *testing.T) {
		env := configured(t, "read-command.yaml")
		record(t, env, bash(gather+" s --query q --term x --entry "+subject))

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})
}

func TestRefusalsOfferTheGatherCall(t *testing.T) {
	got := run(t, configured(t, "gather-command.yaml"), "gate", ask("s1"))

	for _, line := range []string{
		"One call runs all three, then ask again:\n\n  pilot graph gather <entry> --query '<subject>' --term '<literal>' --entry <entry>\n",
		"Or run both + the listing apart:\n\n  pilot graph search --term '<literal>' --entry <entry>\n",
		"  pilot graph search --query '<subject>' --entry <entry>\n",
		`  pilot graph view --layout "topic(area-<name>):as-list" --entry <entry>` + "\n",
	} {
		if !strings.Contains(got.stderr, line) {
			t.Errorf("the refusal should offer the gather call before the separate reads, got: %s", got.stderr)
		}
	}
}

func TestAMalformedGatherCommandIsAConfigError(t *testing.T) {
	for _, fixture := range []string{
		"gather-command-quoted.yaml",
		"gather-command-operator.yaml",
		"gather-command-lines.yaml",
	} {
		t.Run(fixture, func(t *testing.T) {
			got := run(t, configured(t, fixture), "gate", ask("s1"))

			refuses(t, got, "Fix `.sdd/grounding.yaml`")
			refuses(t, got, "`gather_command`")
		})
	}
}
