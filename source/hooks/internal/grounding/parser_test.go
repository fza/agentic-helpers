package grounding_test

import (
	"testing"
)

func TestTheParserReadsEveryQuoting(t *testing.T) {
	cases := []struct {
		name    string
		command string
		writes  bool
	}{
		{name: "an ANSI-C quoted command word", command: "$'sdd' new d tac body", writes: true},
		{name: "an escape inside double quotes", command: `"s\dd" new d tac body`, writes: true},
		{name: "a heredoc delimiter no word pattern covers", command: "cat <<'EOF-1'\nsdd new d tac body\nEOF-1", writes: false},
		{name: "a call after such a heredoc", command: "cat <<'EOF-1'\nnotes\nEOF-1\nsdd new d tac body", writes: true},
		{name: "a call bash -c runs", command: "bash -c 'sdd new d tac body'", writes: true},
		{name: "a call eval runs", command: "eval 'sdd new d tac body'", writes: true},
		{name: "a call fed to a shell", command: "bash <<'EOF'\nsdd new d tac body\nEOF", writes: true},
		{name: "an expansion in code bash -c runs", command: `bash -c "sdd new d tac $body"`, writes: true},
		{name: "an assignment before a launched capture", command: "env X=1 agentic-capture d.md", writes: true},
		{name: "a capture past a launcher's options", command: "echo d.md | xargs -n 1 agentic-capture", writes: true},
		{name: "a capture find runs", command: "find . -maxdepth 0 -exec agentic-capture d.md \\;", writes: true},
		{name: "a quoted mention", command: `echo "sdd new"`, writes: false},
		{name: "an escaped quote before a mention", command: `echo "a \" b; sdd new d tac body"`, writes: false},
		{name: "a launched command's argument", command: "git ls-files | xargs grep -l agentic-capture", writes: false},
		{name: "a name find looks for", command: "find . -name agentic-capture", writes: false},
		{name: "a name command looks up", command: "command -v agentic-capture", writes: false},
		{name: "a capture past options the walk does not know", command: "flock /tmp/lock agentic-capture d.md", writes: true},
		{name: "an unparseable command naming sdd", command: "sdd new d tac 'body", writes: true},
		{name: "unparseable code bash -c runs", command: `bash -c "sdd new d tac 'body"`, writes: true},
		{name: "an unparseable command naming agentic-capture", command: "agentic-capture d.md )", writes: true},
		{name: "an unparseable command naming neither", command: "echo 'it", writes: false},
		{name: "an unparseable command naming the graph directory", command: "ls .sdd/ 'x", writes: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := areaProject(t)
			onDisk(t, env, "d.md")
			got := run(t, env, "gate", bash(tc.command))

			if !tc.writes {
				passes(t, got)

				return
			}

			refuses(t, got, "neither read")

			ground(t, env)
			passes(t, run(t, env, "gate", bash(tc.command)))
		})
	}
}

func TestOnlyTheShellsOwnCallsAreCredited(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    credited
	}{
		{name: "an ANSI-C quoted read", command: "$'sdd' search --term x", want: credited{Search: 1, Term: 1}},
		{name: "a read in a command substitution", command: "echo \"$(sdd search --query 'a subject')\"", want: credited{Search: 1, Query: 1}},
		{name: "two searches in one command", command: "sdd search --term x; sdd search --query 'a subject'", want: credited{Search: 1, Query: 1, Term: 1}},
		{name: "a read bash -c runs", command: "bash -c 'sdd search --term x'", want: credited{}},
		{name: "a read fed to a shell", command: "bash <<'EOF'\nsdd search --term x\nEOF", want: credited{}},
		{name: "a read that does not parse", command: "sdd search --term 'x", want: credited{}},
		{name: "a word a launcher may hand an option", command: "echo f | xargs grep -rl sdd search --term x --query q", want: credited{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := configured(t, "area-prefix.yaml")
			record(t, env, bash(tc.command))

			got := ledgerOf(t, env)
			if got.Search != tc.want.Search || got.Query != tc.want.Query || got.Term != tc.want.Term {
				t.Errorf("the reads credited should be the shell's own calls, got: %+v", got)
			}
		})
	}
}

func TestAStreamDiscardedInAnotherStretchIsNotTheReads(t *testing.T) {
	for _, command := range []string{
		"bash -c 'sdd search --term x' 2>/dev/null",
		"cat <<'EOF'\nsdd search --term x 2>/dev/null\nEOF",
	} {
		t.Run(command, func(t *testing.T) {
			passes(t, run(t, areaProject(t), "gate", bash(command)))
		})
	}

	refuses(t, run(t, areaProject(t), "gate", bash("$'sdd' search --term x 2>/dev/null")), "discarded stream")
	refuses(t, run(t, areaProject(t), "gate", bash("sdd search --term x >& /dev/null")), "discarded stream")
	refuses(t, run(t, areaProject(t), "gate", bash("sdd search --term x &>/dev/null")), "discarded stream")
	passes(t, run(t, areaProject(t), "gate", bash("sdd search --term x >/tmp/out")))
	passes(t, run(t, areaProject(t), "gate", bash("sdd --version 2>/dev/null")))
}

func TestAShowThatDoesNotParseKeepsItsDepth(t *testing.T) {
	refuses(t, run(t, areaProject(t), "gate", bash("sdd show 20260907-164635-d-tac-vkm --down 0 'x")), "--down 2 --up 1")
	refuses(t, run(t, areaProject(t), "gate", bash("$'sdd' show 20260907-164635-d-tac-vkm --down 0")), "--down 2 --up 1")
	passes(t, run(t, areaProject(t), "gate", bash("echo 'sdd show 20260907-164635-d-tac-vkm --down 0'")))
}
