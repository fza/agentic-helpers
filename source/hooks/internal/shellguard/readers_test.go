package shellguard_test

import (
	"os"
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
		{"cat a dotfile", "{project}", "cat .env", true},
		{"cat standard input", "{project}", "cat -", true},
		{"cat a file then standard input", "{project}", "cat notes.md -", true},
		{"an option after an operand", "{project}", "grep x notes.md -n", false},
		{"an end of options after an operand", "{project}", "cat notes.md -- .env", false},
		{"a dash for a file jq --rawfile takes", "{project}", "jq --rawfile text - -n '$text'", false},
		{"a dash for a file sort -o takes", "{project}", "sort -o - notes.md", false},
		{"a dash for a directory sort -T takes", "{project}", "sort -T - notes.md", false},
		{"a dash for sort --random-source", "{project}", "sort --random-source=- notes.md", false},
		{"a dash for grep --exclude-from", "{project}", "grep --exclude-from=- -r token", false},
		{"a dash where jq reads no standard input", "{project}", "jq . -", false},
		{"sed -l, read differently by GNU and BSD", "{project}", "sed -l 5 p notes.md", false},
		{"sed --line-length", "{project}", "sed --line-length=5 p notes.md", false},
		{"sed checking every script it is given", "{project}", "sed -e p -e '1r config' notes.md", false},
		{"sed checking a grouped script", "{project}", "sed -n -ne p -e 'w copy' notes.md", false},
		{"grep a dotfile", "{project}", "grep -n key .env", true},
		{"grep -f taking the rest of its group inside", "{project}", "grep -fpatterns.txt notes.md", true},
		{"grep -e taking the next word", "{project}", "grep -ne token notes.md .env", true},
		{"head by an old-style count", "{project}", "head -5 notes.md", true},
		{"tail with an attached count", "{project}", "tail -n5 notes.md", true},
		{"sort writing its output inside", "{project}", "sort -o notes.md notes.md", true},
		{"jq reading a file into a variable", "{project}", "jq --rawfile text notes.md -n '$text'", true},

		{"cat outside", "{project}", "cat {outside}/config.yaml", false},
		{"cat through a linked directory", "{project}", "cat link/config.yaml", false},
		{"cat the home directory", "{project}", "cat ~/config.yaml", false},
		{"cat a parent step", "{project}", "cat ../config.yaml", false},
		{"cat through $HOME", "{project}", `cat "$HOME"/config.yaml`, false},
		{"cat a glob", "{project}", "cat *.md", false},
		{"a glob as grep's pattern", "{project}", "grep * notes.md", false},
		{"a sed script from a variable", "{project}", `sed "$SCRIPT" notes.md`, false},
		{"a jq filter from a variable", "{project}", `jq -n "$FILTER"`, false},
		{"cd then cat", "{project}", "cd {outside} && cat config.yaml", false},
		{"the working directory outside", "{outside}", "cat config.yaml", false},
		{"grep of the working directory outside", "{outside}", "grep -rn token", false},
		{"pushd then cat", "{project}", "pushd {outside} && cat config.yaml", false},
		{"output to a file through >&", "{project}", "cat notes.md >& {outside}/copy", false},
		{"output to a variable", "{project}", `cat notes.md > "$OUT"`, false},
		{"an option's value outside", "{project}", "grep --file={outside}/patterns notes.md", false},
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
		{"a here-string expanding a variable", "{project}", `cat <<< "$TOKEN"`, false},
		{"a loop", "{project}", "for f in a b; do cat notes.md; done", false},
		{"env runs the reader", "{project}", "env cat notes.md", false},
		{"awk reads any file", "{project}", "awk '{print}' notes.md", false},
		{"echo is no reader", "{project}", "echo hello", false},
		{"grep -R follows links out", "{project}", "grep -Rn token .", false},
		{"sort reading names from a file", "{project}", "sort --files0-from=names.txt notes.md", false},
		{"sort reading names through an abbreviated option", "{project}", "sort --files0=names.txt notes.md", false},
		{"wc reading names from a file", "{project}", "wc --files0-from=names.txt", false},
		{"sort running a program", "{project}", "sort --compress-program=gzip notes.md", false},
		{"grep following links through an abbreviated option", "{project}", "grep --deref token .", false},
		{"grep -e taking the rest of its group", "{project}", "grep -ef pattern {outside}/config.yaml", false},
		{"grep -f taking the rest of its group", "{project}", "grep -fe {outside}/config.yaml notes.md", false},
		{"grep reading patterns from outside", "{project}", "grep -f {outside}/config.yaml notes.md", false},
		{"grep -e leaving a file outside", "{project}", "grep -e token {outside}/config.yaml", false},
		{"jq reading a file outside into a variable", "{project}", "jq --rawfile text {outside}/config.yaml -n '$text'", false},
		{"an option off the list", "{project}", "cat -Z notes.md", false},
		{"a long flag given a value", "{project}", "grep --count=1 x notes.md", false},
		{"a long option abbreviated", "{project}", "grep --line-num x notes.md", false},
		{"a dangling link inside the project pointing outside", "{project}", "cat dangling", false},
		{"a file that does not exist", "{project}", "cat missing.md", false},
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
			projectFiles(t, g)

			got := shellguard.ReadsOnlyProject(g.placeholders.Replace(tc.command), g.placeholders.Replace(tc.cwd), g.env)
			if got != tc.project {
				t.Errorf("only a known reader of project files should count as showing the project, got %v", got)
			}
		})
	}
}

// projectFiles writes the files the reader cases name inside the project, and
// a dangling link inside it pointing outside.
func projectFiles(t *testing.T, g guard) {
	t.Helper()

	for _, name := range []string{"notes.md", "data.json", "patterns.txt", "names.txt", ".env"} {
		test.WriteFile(t, filepath.Join(g.tree.Project, name), "key: value\n")
	}

	test.WriteFile(t, filepath.Join(g.tree.Outside, "config.yaml"), "key: value\n")

	err := os.Symlink(filepath.Join(g.tree.Outside, "missing"), filepath.Join(g.tree.Project, "dangling"))
	if err != nil {
		t.Fatalf("linking outside: %v", err)
	}
}

func TestNoProjectShowsNothingAsTheProject(t *testing.T) {
	tree := test.NewGuardTree(t, false)

	for _, root := range []string{"", "/", tree.Home} {
		env := shellguard.Env{ProjectDir: root, Home: tree.Home}

		for _, cwd := range []string{filepath.Join(tree.Home, "project"), root} {
			if shellguard.ReadsOnlyProject("grep -rn token", cwd, env) {
				t.Errorf("a session with no project should count no output as the project's, root %q in %q", root, cwd)
			}
		}
	}
}
