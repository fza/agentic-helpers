package shellguard_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestOnlyAReaderOfProjectFilesShowsTheProject(t *testing.T) {
	cases := []struct {
		name    string
		cwd     string
		command string
		project bool
	}{
		{"cat a project file", "{project}", "cat notes.md", true},
		{"cat by absolute path", "{project}", "cat {project}/notes.md", true},
		{"grep a project file", "{project}", "grep -n token notes.md", true},
		{"grep the project recursively", "{project}", "grep -rn token .", true},
		{"grep with no path reads the working directory", "{project}/sub", "grep -rn token", true},
		{"a pipeline of readers", "{project}", "cat notes.md | grep -v x | sort | uniq -c | head -n 5", true},
		{"readers in sequence", "{project}", "head -n 3 notes.md; tail -n 3 notes.md && wc -l notes.md", true},
		{"an option's value inside", "{project}", "grep --file=patterns.txt notes.md", true},
		{"stderr to /dev/null", "{project}", "grep x notes.md 2>/dev/null", true},
		{"stderr to stdout", "{project}", "grep x notes.md 2>&1", true},
		{"input from a project file", "{project}", "wc -l < notes.md", true},
		{"a literal here-string", "{project}", "grep x <<< 'some text'", true},
		{"a quoted here-document", "{project}", "cat <<'EOF'\nplain\nEOF", true},
		{"cut and tr", "{project}", "cut -d: -f1 notes.md | tr a-z A-Z | nl", true},
		{"sed printing lines", "{project}", "sed -n 1,20p notes.md", true},
		{"sed substituting", "{project}", "sed -E -e 's|a/b|c|g' -e '/^#/d' notes.md", true},
		{"sed with a grouped -e", "{project}", "sed -ne '/x/{p;q}' notes.md", true},
		{"jq a project file", "{project}", "jq -r '.items[] | .name' data.json", true},
		{"jq with an argument", "{project}", "jq --arg name x '.[$name]' data.json", true},

		{"cat outside", "{project}", "cat {outside}/config.yaml", false},
		{"cat through a linked directory", "{project}", "cat link/config.yaml", false},
		{"cat the home directory", "{project}", "cat ~/config.yaml", false},
		{"cat a parent step", "{project}", "cat ../config.yaml", false},
		{"cat through $HOME", "{project}", `cat "$HOME"/config.yaml`, false},
		{"cat a glob", "{project}", "cat *.md", false},
		{"cd then cat", "{project}", "cd {outside} && cat config.yaml", false},
		{"the working directory outside", "{outside}", "cat config.yaml", false},
		{"grep of the working directory outside", "{outside}", "grep -rn token", false},
		{"pushd then cat", "{project}", "pushd {outside} && cat config.yaml", false},
		{"output to a file through >&", "{project}", "cat notes.md >& {outside}/copy", false},
		{"a reader fed to a shell", "{project}", "cat notes.md | bash", false},
		{"a script", "{project}", "./show.sh", false},
		{"bash running a script", "{project}", "bash show.sh", false},
		{"bash -c", "{project}", "bash -c 'cat notes.md'", false},
		{"a command substitution", "{project}", "cat $(echo notes.md)", false},
		{"a process substitution", "{project}", "cat <(cat {outside}/config.yaml)", false},
		{"an assignment", "{project}", "X=1 cat notes.md", false},
		{"a reader named by path", "{project}", "/bin/cat notes.md", false},
		{"input from outside", "{project}", "cat < {outside}/config.yaml", false},
		{"output written outside", "{project}", "cat notes.md > {outside}/copy", false},
		{"a here-document expanding a variable", "{project}", "cat <<EOF\n$TOKEN\nEOF", false},
		{"a loop", "{project}", "for f in a b; do cat notes.md; done", false},
		{"env runs the reader", "{project}", "env cat notes.md", false},
		{"awk reads any file", "{project}", "awk '{print}' notes.md", false},
		{"echo is no reader", "{project}", "echo hello", false},
		{"grep -R follows links out", "{project}", "grep -Rn token .", false},
		{"sort reading names from a file", "{project}", "sort --files0-from=names notes.md", false},
		{"sort running a program", "{project}", "sort --compress-program=gzip notes.md", false},
		{"sed reading a file", "{project}", "sed '1r {outside}/config.yaml' notes.md", false},
		{"sed reading a file by a relative name", "{project}", "sed '1r config' notes.md", false},
		{"sed writing a file", "{project}", "sed -n 'w copy' notes.md", false},
		{"sed running a command", "{project}", "sed '1e cat config' notes.md", false},
		{"sed substituting and running", "{project}", "sed 's/x/cat config/e' notes.md", false},
		{"sed reading its script from a file", "{project}", "sed -f script.sed notes.md", false},
		{"sed in place", "{project}", "sed -i 's/a/b/' notes.md", false},
		{"sed with no script", "{project}", "sed -n", false},
		{"jq reading the environment", "{project}", "jq -n env", false},
		{"jq reading $ENV", "{project}", "jq -n '$ENV.TOKEN'", false},
		{"jq importing a module", "{project}", `jq -n 'import "x" as x; x::y'`, false},
		{"jq reading its filter from a file", "{project}", "jq -f filter.jq data.json", false},
		{"jq with a library path", "{project}", "jq -L {outside} -n 1", false},
		{"unparsable", "{project}", "cat 'notes.md", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGuard(t, false, "")

			got := shellguard.ReadsOnlyProject(g.placeholders.Replace(tc.command), g.placeholders.Replace(tc.cwd), g.env)
			if got != tc.project {
				t.Errorf("only a known reader of project files should count as showing the project, got %v", got)
			}
		})
	}
}

func TestNoProjectShowsNothingAsTheProject(t *testing.T) {
	tree := test.NewGuardTree(t, false)

	for _, root := range []string{"", "/", tree.Home} {
		env := shellguard.Env{ProjectDir: root, Home: tree.Home}

		if shellguard.ReadsOnlyProject("cat notes.md", filepath.Join(tree.Home, "project"), env) {
			t.Errorf("a session with no project should count no output as the project's, root %q", root)
		}
	}
}
