package shellguard_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestPathsTheTextFixesAreJudgedAsWrittenOut(t *testing.T) {
	cases := []struct {
		name     string
		cwd      string
		command  string
		decision string
		reason   string
	}{
		{name: "a script fixing its paths and entering its parent folder", cwd: "{project}", command: "timeout 300 scripts/dev-setup.sh", decision: "allow"},
		{name: "the same script bash runs", cwd: "{project}", command: "bash scripts/dev-setup.sh", decision: "allow"},
		{name: "the same script run from another folder", cwd: "{project}/sub", command: "../scripts/dev-setup.sh", decision: "allow"},
		{
			name: "a path assigned inside an if", cwd: "{project}", command: "bash scripts/assigned-in-if.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/assigned-in-if.sh: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a path assigned inside a function", cwd: "{project}", command: "bash scripts/assigned-in-function.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/assigned-in-function.sh: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a path assigned behind another command", cwd: "{project}", command: "bash scripts/assigned-behind-a-command.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/assigned-behind-a-command.sh: refusing rm with a shell variable or command substitution",
		},
		{name: "a path used in a function called after it changes", cwd: "{project}", command: "bash scripts/used-in-function.sh", decision: "ask"},
		{name: "a path a loop reassigns after its use", cwd: "{project}", command: "bash scripts/reassigned-in-loop.sh", decision: "ask"},
		{
			name: "a fixed path outside the project", cwd: "{project}", command: "bash scripts/fixed-outside.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/fixed-outside.sh: outside project: {outside}/x",
		},
		{
			name: "a path from a command substitution", cwd: "{project}", command: "bash scripts/substituted.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/substituted.sh: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a path read from input", cwd: "{project}", command: "bash scripts/read.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/read.sh: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a path never assigned", cwd: "{project}", command: "bash scripts/never-assigned.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script scripts/never-assigned.sh: refusing rm with a shell variable or command substitution",
		},
		{name: "a path eval may reassign", cwd: "{project}", command: "bash scripts/evaluated.sh", decision: "ask"},

		{name: "a cd into the script's folder by dirname", cwd: "{outside}", command: "{project}/scripts/folder-dirname.sh", decision: "allow"},
		{name: "a cd into the script's folder by BASH_SOURCE", cwd: "{outside}", command: "{project}/scripts/folder-bash-source.sh", decision: "allow"},
		{name: "a cd into the script's folder by a suffix cut", cwd: "{outside}", command: "{project}/scripts/folder-cut.sh", decision: "allow"},
		{name: "a cd into the script's folder by cd and pwd", cwd: "{outside}", command: "{project}/scripts/folder-pwd.sh", decision: "allow"},
		{name: "a guarded cd into the script's folder", cwd: "{outside}", command: "{project}/scripts/folder-guarded.sh", decision: "allow"},
		{name: "a cd into the folder of a script sh runs", cwd: "{outside}", command: "sh {project}/scripts/folder-dirname.sh", decision: "allow"},
		{
			name: "a cd into the folder of a script outside by dirname", cwd: "{project}", command: "{outside}/scripts/folder-dirname.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script {outside}/scripts/folder-dirname.sh: outside project: .tools/cache",
		},
		{name: "a cd into the folder of a script outside by BASH_SOURCE", cwd: "{project}", command: "{outside}/scripts/folder-bash-source.sh", decision: "ask"},
		{name: "a cd into the folder of a script outside by a suffix cut", cwd: "{project}", command: "{outside}/scripts/folder-cut.sh", decision: "ask"},
		{name: "a cd into the folder of a script outside by cd and pwd", cwd: "{project}", command: "{outside}/scripts/folder-pwd.sh", decision: "ask"},
		{
			name: "BASH_SOURCE in a script sh runs", cwd: "{outside}", command: "sh {project}/scripts/folder-bash-source.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script {project}/scripts/folder-bash-source.sh: unresolvable: .tools/cache",
		},
		{
			name: "the script's folder in a sourced script", cwd: "{outside}", command: "source {project}/scripts/folder-dirname.sh", decision: "ask",
			reason: "agentic-shell-guard: in the script {project}/scripts/folder-dirname.sh: unresolvable: .tools/cache",
		},

		{name: "a move source a command line fixes", cwd: "{project}", command: `src=sub/a; mv "$src" sub/b`, decision: "allow"},
		{name: "a move source joined from fixed variables", cwd: "{project}", command: `dir=sub; src="$dir/a"; mv "$src" sub/b`, decision: "allow"},
		{
			name: "a move source a command line fixes outside", cwd: "{project}", command: `src={outside}/a; mv "$src" sub/b`, decision: "ask",
			reason: "agentic-shell-guard: outside project: {outside}/a",
		},
		{
			name: "a move source from a command substitution", cwd: "{project}", command: `src=$(pwd)/a; mv "$src" sub/b`, decision: "deny",
			reason: "agentic-shell-guard: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a move source read from input", cwd: "{project}", command: `src=sub/a; read -r src; mv "$src" sub/b`, decision: "deny",
			reason: "agentic-shell-guard: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a move source never assigned", cwd: "{project}", command: `ls; mv "$src" sub/b`, decision: "deny",
			reason: "agentic-shell-guard: refusing rm with a shell variable or command substitution",
		},
		{
			name: "a move source assigned behind another command", cwd: "{project}", command: `ls && src=sub/a; mv "$src" sub/b`, decision: "deny",
			reason: "agentic-shell-guard: refusing rm with a shell variable or command substitution",
		},
		{
			name: "the script's folder on a command line", cwd: "{project}", command: `cd "$(dirname "$0")" && mv sub/a sub/b`, decision: "ask",
			reason: "agentic-shell-guard: unresolvable: sub/a",
		},
	}

	g := newGuard(t, true, "")
	test.FixtureTreeReplacing(t, "shellguard/resolution", g.tree.Project, g.placeholders)
	test.FixtureTreeReplacing(t, "shellguard/resolution", g.tree.Outside, g.placeholders)
	g.env.Getenv = test.Getenv(test.Stubs(t, "timeout"))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, reason := g.judge(tc.command, tc.cwd)
			if decision != tc.decision {
				t.Errorf("the command should be judged differently, got %s: %s", decision, reason)
			}

			if tc.reason != "" && reason != g.placeholders.Replace(tc.reason) {
				t.Errorf("the guard should name what it judged, got: %s", reason)
			}
		})
	}
}
