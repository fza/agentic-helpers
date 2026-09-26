package grounding_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const wrapper = "python3 scripts/graph.py"

func TestRefusalsWithoutAReadCommandAreUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		env     func(t *testing.T) result
		fixture string
	}{
		{name: "missing reads, every topic", env: func(t *testing.T) result { return run(t, bare(t), "gate", ask("s1")) }, fixture: "refusals/reads-every-topic.txt"},
		{name: "missing reads, area listing", env: func(t *testing.T) result { return run(t, areaProject(t), "gate", ask("s1")) }, fixture: "refusals/reads-area.txt"},
		{name: "discarded stream", env: func(t *testing.T) result { return run(t, bare(t), "gate", bash("sdd search --term x 2>/dev/null")) }, fixture: "refusals/stream.txt"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.env(t)
			if got.stderr != test.Fixture(t, tc.fixture) {
				t.Errorf("a project naming no read command should get the refusal it always got, got: %s", got.stderr)
			}
		})
	}
}

func TestRefusalsSuggestTheReadCommand(t *testing.T) {
	t.Run("missing reads", func(t *testing.T) {
		got := run(t, configured(t, "read-command.yaml"), "gate", ask("s1"))

		for _, line := range []string{
			"  " + wrapper + " search --term '<literal>' --entry <entry>\n",
			"  " + wrapper + " search --query '<subject>' --entry <entry>\n",
			"  " + wrapper + ` view --layout "topic(area-<name>):as-list" --entry <entry>` + "\n",
		} {
			if !strings.Contains(got.stderr, line) {
				t.Errorf("every suggested read should run through the command and end with the suffix, got: %s", got.stderr)
			}
		}

		if strings.Contains(got.stderr, "  sdd ") {
			t.Errorf("no suggested read should name bare sdd, got: %s", got.stderr)
		}
	})

	t.Run("discarded stream", func(t *testing.T) {
		got := run(t, configured(t, "read-command.yaml"), "gate", bash(wrapper+" search --term x 2>/dev/null"))

		refuses(t, got, "  "+wrapper+" search --term '<literal>' --limit 8 --entry <entry> | grep -E '^  [0-9]'\n")
	})

	t.Run("a suffix without a command", func(t *testing.T) {
		got := run(t, configured(t, "read-suffix.yaml"), "gate", ask("s1"))

		refuses(t, got, "  sdd search --term '<literal>' --entry <entry>\n")
		refuses(t, got, `  sdd view --layout "active:as-counts" --entry <entry>`+"\n")
	})

	t.Run("a command without a suffix", func(t *testing.T) {
		got := run(t, configured(t, "read-command-spaced.yaml"), "gate", ask("s1"))

		refuses(t, got, "  "+wrapper+" search --query '<subject>'\n")
	})
}

func TestReadsThroughTheReadCommand(t *testing.T) {
	grounds := [][]string{
		{wrapper + " search --term x --entry e", wrapper + " search --query 'a subject' --entry e", wrapper + ` view --layout "topic(area-hooks):as-list" --entry e`},
		{wrapper + " search --term x", wrapper + " search --query 'a subject'", wrapper + ` view --layout "topic(area-hooks):as-list"`},
		{"sdd search --term x", "sdd search --query 'a subject'", `sdd view --layout "topic(area-hooks):as-list"`},
		{"cd /x && python3   scripts/graph.py search --term x", "env " + wrapper + " search --query 'a subject'", "(/usr/bin/" + wrapper + ` view --layout "topic(area-hooks):as-list")`},
	}

	for _, reads := range grounds {
		t.Run(reads[0], func(t *testing.T) {
			env := configured(t, "read-command.yaml")
			for _, read := range reads {
				record(t, env, bash(read))
			}

			passes(t, run(t, env, "gate", ask("s1")))
		})
	}

	t.Run("a mention of the command counts for nothing", func(t *testing.T) {
		env := configured(t, "read-command.yaml")
		record(t, env, bash("echo '"+wrapper+" search --query foo --term bar'"), bash(`echo '`+wrapper+` view --layout "topic(area-hooks):as-list"'`))

		refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	})

	for _, command := range []string{
		wrapper + " search --term x 2>/dev/null",
		"cd /x && " + wrapper + " info >/dev/null 2>&1",
		"bash -c '" + wrapper + " search --term x 2>/dev/null'",
		"(" + wrapper + " search --term x 2>/dev/null)",
		"env " + wrapper + " search --term x 2>/dev/null",
	} {
		t.Run("discarding "+command, func(t *testing.T) {
			refuses(t, run(t, configured(t, "read-command.yaml"), "gate", bash(command)), "discarded stream")
		})
	}

	for _, command := range []string{
		"grep -rn 'scripts/graph.py search' docs 2>/dev/null",
		"python3 scripts/other.py search --term x 2>/dev/null",
	} {
		t.Run("not a read "+command, func(t *testing.T) {
			passes(t, run(t, configured(t, "read-command.yaml"), "gate", bash(command)))
		})
	}

	t.Run("a show short of the depth", func(t *testing.T) {
		refuses(t, run(t, configured(t, "read-command.yaml"), "gate", bash(wrapper+" show 20260907-164635-d-tac-vkm --down 1 --entry e")), "reads short of `--down 2 --up 1`")
		passes(t, run(t, configured(t, "read-command.yaml"), "gate", bash(wrapper+" show 20260907-164635-d-tac-vkm --down 2 --up 1 --entry e")))
	})

	t.Run("a plain sdd new is still a write, the command's new is not", func(t *testing.T) {
		refuses(t, run(t, configured(t, "read-command.yaml"), "gate", bash("sdd new d tac body")), "neither read")
		passes(t, run(t, configured(t, "read-command.yaml"), "gate", bash(wrapper+" new d tac body")))
	})
}

func TestTheWrapperIsNoReadWithoutTheConfig(t *testing.T) {
	env := areaProject(t)
	record(t, env, bash(wrapper+" search --term x"), bash(wrapper+" search --query 'a subject'"), bash(wrapper+` view --layout "topic(area-hooks):as-list"`))

	refuses(t, run(t, env, "gate", ask("s1")), "neither read")
	passes(t, run(t, env, "gate", bash(wrapper+" search --term x 2>/dev/null")))
	passes(t, run(t, env, "gate", bash(wrapper+" show 20260907-164635-d-tac-vkm --down 0")))
}

func TestAMalformedReadCommandIsAConfigError(t *testing.T) {
	for _, fixture := range []string{
		"read-command-list.yaml",
		"read-command-quoted.yaml",
		"read-command-operator.yaml",
		"read-command-variable.yaml",
		"read-command-lines.yaml",
		"read-suffix-lines.yaml",
	} {
		t.Run(fixture, func(t *testing.T) {
			env := configured(t, fixture)

			refuses(t, run(t, env, "gate", ask("s1")), "Fix `.sdd/grounding.yaml`")
			refuses(t, run(t, env, "gate", bash(shownEntry+" --down 2 --up 1")), "Fix `.sdd/grounding.yaml`")
			refuses(t, run(t, env, "gate", bash("sdd search --term x 2>/dev/null")), "discarded stream")
			passes(t, run(t, env, "gate", bash("ls 2>/dev/null")))
		})
	}
}
