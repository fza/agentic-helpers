package shellguard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/shellguard"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type verdictCase struct {
	name     string
	cwd      string
	command  string
	expected string
}

type guard struct {
	t            *testing.T
	tree         test.GuardTree
	env          shellguard.Env
	placeholders *strings.Replacer
	session      string
}

func newGuard(t *testing.T, scratch bool, options string) guard {
	t.Helper()

	tree := test.NewGuardTree(t, scratch)
	settings := filepath.Join(t.TempDir(), "settings.json")
	test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+options+`}}}}`)

	return guard{
		t: t, tree: tree,
		env: shellguard.Env{ProjectDir: tree.Project, Home: tree.Home, Tmp: tree.Tmp, Settings: []string{settings}},
		placeholders: strings.NewReplacer(
			"{project}", tree.Project, "{tmp}", tree.Tmp, "{home}", tree.Home, "{outside}", tree.Outside,
		),
	}
}

type reply struct {
	HookSpecificOutput struct {
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// judge is the decision, allow where the guard answered nothing, and the reason.
func (g guard) judge(command string, cwd string) (string, string) {
	g.t.Helper()

	return g.answer(command, cwd, "")
}

// judgeIn is the decision on the command in the permission mode.
func (g guard) judgeIn(command string, mode string) (string, string) {
	g.t.Helper()

	return g.answer(command, "{project}", mode)
}

// inSession is the guard judging every call in a session that keeps its state.
func (g guard) inSession(session string) guard {
	g.t.Helper()

	if g.env.StateDir == "" {
		g.env.StateDir = g.t.TempDir()
	}

	g.session = session

	return g
}

func (g guard) answer(command string, cwd string, mode string) (string, string) {
	g.t.Helper()

	call := map[string]any{
		"cwd": g.placeholders.Replace(cwd), "tool_input": map[string]string{"command": g.placeholders.Replace(command)},
	}
	if mode != "" {
		call["permission_mode"] = mode
	}

	if g.session != "" {
		call["session_id"] = g.session
	}

	input, err := json.Marshal(call)
	if err != nil {
		g.t.Fatalf("encoding the call: %v", err)
	}

	var out, errs bytes.Buffer

	status := shellguard.Main(context.Background(), g.env, shellguard.Streams{In: bytes.NewReader(input), Out: &out, Err: &errs})
	if status != 0 {
		g.t.Fatalf("the guard should answer, exit %d: %s", status, errs.String())
	}

	if out.Len() == 0 {
		return "allow", ""
	}

	var decoded reply

	err = json.Unmarshal(out.Bytes(), &decoded)
	if err != nil {
		g.t.Fatalf("decoding the answer: %v", err)
	}

	return decoded.HookSpecificOutput.PermissionDecision, decoded.HookSpecificOutput.PermissionDecisionReason
}

func TestTheDefaultsJudgeEveryCommandAsTheMachineGuard(t *testing.T) {
	cases := []verdictCase{
		{"plain command", "{project}", "ls -la", "allow"},
		{"cd then command", "{project}", "cd {tmp} && ls", "allow"},
		{"lone rm inside the project", "{project}", "rm foo", "allow"},
		{"lone rm of several project targets", "{project}", "rm -rf sub/x .tmp/probe", "allow"},
		{"relative target from a project subdirectory", "{project}/sub", "rm a", "allow"},
		{"git rm alone", "{project}", "git rm --cached file", "allow"},
		{"target after end of options", "{project}", "rm -- -weird", "allow"},
		{"absolute project target", "{project}", "rm -f {project}/sub/x", "allow"},
		{"rm by path", "{project}", "/bin/rm sub/x", "allow"},
		{"escaped rm", "{project}", `\rm sub/x`, "allow"},
		{"quoted target", "{project}", `rm "sub/my file"`, "allow"},
		{"pipeline without removal", "{project}", `echo "done" | tee log`, "allow"},
		{"rm inside a sed script", "{project}", `sed -i 's/ rm / x /' file`, "allow"},
		{"rm inside a grep pattern", "{project}", `grep -rn 'rm -rf' src`, "allow"},
		{"rm as a grep argument", "{project}", "grep -rn rm src", "allow"},
		{"rm inside a commit message", "{project}", `git commit -m "drop rm usage" && git log -1`, "allow"},
		{"mktemp inside quoted text", "{project}", `grep -n 'mktemp' notes.md`, "allow"},
		{"rm inside a heredoc body", "{project}", "cat > note.md <<'EOF'\nrm -rf /\nEOF", "allow"},
		{"target under tmp", "{project}", "rm {tmp}/x", "allow"},
		{"several targets under tmp", "{project}", "rm -rf {tmp}/x {tmp}/y/z", "allow"},
		{"rmdir under tmp", "{project}", "rmdir {tmp}/d", "allow"},
		{"relative target inside tmp", "{tmp}", "rm x", "allow"},
		{"tmp target with no project", "{project}", "rm {tmp}/x", "allow"},
		{"shell code without removal", "{project}", `bash -c 'echo hi'`, "allow"},
		{"heredoc to bash without removal", "{project}", "bash <<'EOF'\necho hi\nEOF", "allow"},
		{"input piped to a bash script", "{project}", "echo x | bash script.sh", "allow"},
		{"input piped to a bash script with an option value", "{project}", "echo x | bash -o errexit script.sh", "allow"},
		{"trap listing", "{project}", "trap -p", "allow"},
		{"glob in a project directory", "{project}", "rm -rf sub/*", "allow"},

		{"mktemp", "{project}", "mktemp", "deny"},
		{"mktemp in a substitution", "{project}", "dir=$(mktemp -d)", "deny"},
		{"mktemp by path", "{project}", "/usr/bin/mktemp -t x", "deny"},
		{"mktemp in a backtick", "{project}", "echo `mktemp`", "deny"},
		{"variable target", "{project}", "rm $f", "deny"},
		{"quoted variable target", "{project}", `rm -rf "$dir"`, "deny"},
		{"braced variable target", "{project}", "rm -f ${file}", "deny"},
		{"substituted target", "{project}", "rm $(cat list)", "deny"},
		{"backtick target", "{project}", "rm `cat list`", "deny"},
		{"ansi-c quoted target", "{project}", "rm $'a\\nb'", "allow"},
		{"ansi-c quoted rm", "{project}", "$'rm' sub/x", "allow"},
		{"ansi-c quoted target with a control escape", "{project}", "rm $'a\\cb'", "deny"},
		{"ansi-c quoted target past one byte", "{project}", "rm $'a\\400'", "deny"},
		{"ansi-c quoted target with an octal escape", "{project}", "rm $'a\\18'", "deny"},
		{"ansi-c quoted target with a nul", "{project}", "rm $'sub/x\\x00/../../etc'", "deny"},
		{"ansi-c quoted target naming no character", "{project}", "rm $'a\\U110000'", "deny"},
		{"ansi-c quoted rm after a command", "{project}", "ls && $'rm' a", "deny"},
		{"ansi-c quoted rm under a launcher", "{project}", "sudo $'\\x72m' a", "deny"},
		{"ansi-c quoted mktemp", "{project}", "$'mktemp' -d", "deny"},
		{"ansi-c quoted git rm after a command", "{project}", "ls && git $'rm' b", "deny"},
		{"ansi-c quoted shell code with a removal", "{project}", "bash -c $'rm x'", "deny"},
		{"shell code with a variable removal", "{project}", `sh -c 'rm -rf $X'`, "deny"},
		{"shell code with a removal", "{project}", `sh -c 'rm -rf sub/x'`, "deny"},
		{"login shell code with a removal", "{project}", `bash -lc 'rm x'`, "deny"},
		{"unreadable shell code", "{project}", `bash -c "$CODE"`, "deny"},
		{"eval of a removal", "{project}", `eval "rm x"`, "deny"},
		{"cd before rm", "{project}", "cd {project} && rm a", "deny"},
		{"command before rm", "{project}", "ls && rm a", "deny"},
		{"command after rm", "{project}", "rm a; ls", "deny"},
		{"rm with a fallback", "{project}", "rm a || true", "deny"},
		{"rm in the background", "{project}", "rm a & wait", "deny"},
		{"rm piped", "{project}", "rm a | cat", "deny"},
		{"rm in a subshell", "{project}", "(rm a)", "deny"},
		{"rm in a group", "{project}", "{ rm a; }", "deny"},
		{"rm in a substitution", "{project}", "echo $(rm a)", "deny"},
		{"rm in a backtick", "{project}", "echo `rm a`", "deny"},
		{"rm redirected", "{project}", "rm a > log", "deny"},
		{"rm with stderr redirected", "{project}", "rm a 2>/dev/null", "deny"},
		{"rm on two lines", "{project}", "rm a\nrm b", "deny"},
		{"rm with an assignment", "{project}", "LC_ALL=C rm a", "deny"},
		{"negated rm", "{project}", "! rm a", "deny"},
		{"rm under sudo", "{project}", "sudo rm a", "deny"},
		{"rm under command", "{project}", "command rm a", "deny"},
		{"rm under timeout", "{project}", "timeout 10 rm a", "deny"},
		{"rm fed by xargs", "{project}", "ls | xargs rm", "deny"},
		{"rm fed by xargs with options", "{project}", "ls | xargs -n1 rm -f", "deny"},
		{"rm run by find", "{project}", `find . -name x -exec rm {} \;`, "deny"},
		{"shell code under sudo", "{project}", `sudo sh -c 'rm a'`, "deny"},
		{"shell code run by find", "{project}", `find . -exec sh -c 'rm "$1"' _ {} \;`, "deny"},
		{"git rm after a command", "{project}", "git add a && git rm b", "deny"},
		{"git rm with global options", "{project}", "git -C sub rm b", "deny"},
		{"rm in a function", "{project}", "f() { rm a; }; f", "deny"},
		{"rm in a loop", "{project}", "for f in a b; do rm $f; done", "deny"},
		{"python code naming rm", "{project}", `python3 -c 'import os; os.system("rm x")'`, "deny"},
		{"perl code naming unlink", "{project}", `perl -e 'unlink "x"'`, "deny"},
		{"awk code naming rm", "{project}", `awk '{system("rm " $0)}' list`, "deny"},
		{"remote command naming rm", "{project}", `ssh host 'rm -rf /srv/x'`, "deny"},
		{"inline code naming mktemp", "{project}", `python3 -c 'import os; os.system("mktemp")'`, "deny"},
		{"unparsable command", "{project}", "echo 'unterminated", "deny"},
		{"a denial outranks a later question", "{project}", "mktemp && find . -delete", "deny"},
		{"heredoc to bash with a removal", "{project}", "bash <<'EOF'\nrm -rf x\nEOF", "deny"},
		{"here-string to sh with a removal", "{project}", "sh <<< 'rm x'", "deny"},
		{"heredoc to python naming rmtree", "{project}", "python3 - <<'EOF'\nimport shutil; shutil.rmtree('x')\nEOF", "deny"},
		{"code piped to sh", "{project}", "echo 'rm x' | sh", "deny"},
		{"code piped to bash -s", "{project}", "echo 'rm x' | bash -s", "deny"},
		{"code piped to bash -s with arguments", "{project}", "echo 'rm x' | bash -s foo", "deny"},
		{"code piped to a launched shell", "{project}", "echo 'rm x' | sudo sh", "deny"},
		{"code piped to bash with an option value", "{project}", "echo 'rm x' | bash -o errexit", "deny"},
		{"code piped to bash with a grouped option value", "{project}", "echo 'rm x' | bash -eo pipefail", "deny"},
		{"fish init command with a removal", "{project}", "fish -C 'rm x'", "deny"},
		{"code piped to fish with init code", "{project}", "echo 'rm x' | fish -C 'echo hi'", "deny"},
		{"code piped to bash after an unknown long option", "{project}", "echo 'rm x' | bash --dump-po-strings x", "deny"},
		{"shell --command with a removal", "{project}", "fish --command 'rm x'", "deny"},
		{"inline code for an interpreter it does not know", "{project}", `rust-script -e 'std::fs::remove_dir_all("x")'`, "allow"},
		{"ruby code naming rm_f", "{project}", `ruby -e 'FileUtils.rm_f("x")'`, "deny"},
		{"code piped to bash with an rcfile", "{project}", "echo 'rm x' | bash --rcfile rc", "deny"},
		{"input piped to a bash script after a long option", "{project}", "echo x | bash --norc script.sh", "allow"},
		{"code piped to bash -es with arguments", "{project}", "echo 'rm x' | bash -es foo", "deny"},
		{"trap after its options", "{project}", "trap -- 'rm -f x' EXIT", "deny"},
		{"trap under a launcher", "{project}", "command trap 'rm -f x' EXIT", "deny"},
		{"python code naming removedirs", "{project}", `python3 -c 'import os; os.removedirs("x")'`, "deny"},
		{"ruby code naming upper-case RM_RF", "{project}", `ruby -e 'FileUtils.RM_RF("x")'`, "deny"},
		{"trap running a removal", "{project}", "trap 'rm -f x' EXIT", "deny"},
		{"python code naming rmtree", "{project}", `python3 -c 'import shutil; shutil.rmtree("x")'`, "deny"},
		{"node code naming rmSync", "{project}", `node -e 'require("fs").rmSync("x")'`, "deny"},

		{"parent directory", "{project}", "rm -rf ../other", "ask"},
		{"home target", "{project}", "rm ~/x", "ask"},
		{"system file", "{project}", "/bin/rm /etc/hosts", "ask"},
		{"escaped rm outside", "{project}", `\rm /x`, "ask"},
		{"rmdir outside", "{project}", "rmdir {outside}/d", "ask"},
		{"unlink outside", "{project}", "unlink {outside}/f", "ask"},
		{"tmp itself", "{project}", "rm -rf {tmp}", "ask"},
		{"tmp itself with a slash", "{project}", "rm -rf {tmp}/", "ask"},
		{"escaping tmp", "{project}", "rm -rf {tmp}/../outside/x", "ask"},
		{"tmp name prefix", "{project}", "rm {tmp}foo/x", "ask"},
		{"one target outside", "{project}", "rm {tmp}/x {outside}/y", "ask"},
		{"find delete", "{project}", "find {tmp} -name x -delete", "ask"},
		{"relative target outside", "{outside}", "rm x", "ask"},
		{"symlink leading outside", "{project}", "rm -rf link/share", "ask"},
		{"the project itself", "{project}", "rm -rf {project}", "ask"},
		{"root glob", "{project}", "rm -rf /*", "ask"},
		{"no target", "{project}", "rm -f", "ask"},
		{"brace target", "{project}", "rm sub/{a,b}", "ask"},
		{"other user's home", "{project}", "rm ~root/x", "ask"},
		{"upper-case rm outside", "{project}", "RM -rf ~/x", "ask"},
		{"dot glob", "{project}", "rm -rf .*", "ask"},
		{"a link followed by a parent step", "{project}", "rm -rf link/../x", "ask"},
		{"a parent step back into the project", "{project}/sub", "rm -rf ../sub/x", "ask"},
		{"dot glob in a subdirectory", "{project}", "rm -rf sub/.[!.]*", "ask"},
		{"ansi-c quoted rm outside", "{project}", "$'rm' -rf /", "ask"},
		{"ansi-c escaped rm outside", "{project}", "$'\\x72m' -rf ~/x", "ask"},
		{"ansi-c quoted target outside", "{project}", "rm $'/etc/hosts'", "ask"},
		{"ansi-c quoted find delete", "{project}", "find {tmp} $'-delete'", "ask"},
	}

	g := newGuard(t, true, "")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, _ := g.judge(tc.command, tc.cwd)
			if decision != tc.expected {
				t.Errorf("the command should be judged differently, got %s", decision)
			}
		})
	}
}

func TestEachOptionChangesOnlyItsOwnGuard(t *testing.T) {
	// Every case pairs a command only its option's guard judges with one every
	// other guard judges, so a setting reaching past its guard shows.
	others := map[string]string{
		"outside": "rm ~/x", "find": "find . -delete", "lone": "ls && rm a", "expansion": "rm $f",
		"nested": `bash -c 'rm x'`, "mktemp": "mktemp",
	}
	defaults := map[string]string{
		"outside": "ask", "find": "ask", "lone": "deny", "expansion": "deny", "nested": "deny", "mktemp": "deny",
	}

	cases := []struct {
		option string
		guard  string
		want   string
	}{
		{`"shell_guard_outside_project":"ask"`, "outside", "ask"},
		{`"shell_guard_outside_project":"deny"`, "outside", "deny"},
		{`"shell_guard_outside_project":"off"`, "outside", "allow"},
		{`"shell_guard_find_delete":"ask"`, "find", "ask"},
		{`"shell_guard_find_delete":"deny"`, "find", "deny"},
		{`"shell_guard_find_delete":"off"`, "find", "allow"},
		{`"shell_guard_lone_removal":true`, "lone", "deny"},
		{`"shell_guard_lone_removal":false`, "lone", "ask"},
		{`"shell_guard_expansion":true`, "expansion", "deny"},
		{`"shell_guard_expansion":false`, "expansion", "ask"},
		{`"shell_guard_nested_code":true`, "nested", "deny"},
		{`"shell_guard_nested_code":false`, "nested", "ask"},
		{`"shell_guard_mktemp":true`, "mktemp", "deny"},
		{`"shell_guard_mktemp":false`, "mktemp", "allow"},
	}

	for _, tc := range cases {
		t.Run(tc.option, func(t *testing.T) {
			g := newGuard(t, true, tc.option)

			for guard, command := range others {
				want := defaults[guard]
				if guard == tc.guard {
					want = tc.want
				}

				decision, _ := g.judge(command, "{project}")
				if decision != want {
					t.Errorf("%s should take the %s guard's verdict under this setting, got %s", command, guard, decision)
				}
			}
		})
	}
}

func TestASwitchedOffSupportingGuardFallsToTheOutsideVerdict(t *testing.T) {
	cases := []struct {
		option  string
		command string
		reason  string
	}{
		{`"shell_guard_lone_removal":false`, "cd ~ && rm -rf x", "cannot place: -rf x (not alone)"},
		{`"shell_guard_expansion":false`, `rm -rf "$HOME/x"`, `cannot place: "$HOME/x" (expansion)`},
		{`"shell_guard_nested_code":false`, `bash -c 'rm -rf ~/x'`, "cannot place: code handed to bash -c (nested code)"},
	}

	for _, tc := range cases {
		for _, outside := range []string{"ask", "deny"} {
			t.Run(tc.option+" "+outside, func(t *testing.T) {
				g := newGuard(t, true, tc.option+`,"shell_guard_outside_project":"`+outside+`"`)

				decision, reason := g.judge(tc.command, "{project}")
				if decision != outside || reason != "agentic-shell-guard: "+tc.reason {
					t.Errorf("a removal the guard no longer denies should take the outside verdict, saying why, got %s %q", decision, reason)
				}
			})
		}

		t.Run(tc.option+" off", func(t *testing.T) {
			g := newGuard(t, true, tc.option+`,"shell_guard_outside_project":"off"`)

			if decision, _ := g.judge(tc.command, "{project}"); decision != "allow" {
				t.Errorf("with the outside-project guard off as well, the removal should pass, got %s", decision)
			}
		})
	}
}

func TestACommandNameThatIsNotFixedTextTakesTheOutsideVerdict(t *testing.T) {
	judged := []struct {
		name    string
		command string
		word    string
	}{
		{"an octal escape spelling rm", `$'\162m' -rf /`, `$'\162m'`},
		{"a variable", "$CMD -rf /", "$CMD"},
		{"a quoted variable", `"$tool" build`, `"$tool"`},
		{"a command substitution", "$(which rm) -rf /", "$(which rm)"},
		{"a variable after another command", "ls && $CMD x", "$CMD"},
		{"a variable after an assignment", "LC_ALL=C $CMD x", "$CMD"},
		{"a variable in shell code", `bash -c '$CMD -rf /'`, "$CMD"},
		{"a variable under a launcher", "sudo -u root $CMD -rf /", "$CMD"},
		{"an unquoted variable as sudo's option value", "sudo -u $user make build", "$user"},
		{"an unquoted variable as nice's option value", "nice -n $n make build", "$n"},
		{"a quoted array as an option value", `sudo -u "${a[@]}" make build`, `"${a[@]}"`},
		{"a quoted star expansion as an option value", `sudo -u "$*" make build`, `"$*"`},
		{"a quoted indirection as an option value", `sudo -u "${!name}" make build`, `"${!name}"`},
		{"a partly quoted option value", `sudo -u x"$user" make build`, `x"$user"`},
		{"a quoted option value followed by an unquoted one", `sudo -u "$user"$x make build`, `"$user"$x`},
		{"a brace as an option value", "sudo -u {root,rm} -rf /", "{root,rm}"},
		{"a glob as an option value", "nice -n ?? make build", "??"},
		{"a variable run by find", `find . -exec "$cmd" {} \;`, `"$cmd"`},
		{"a variable alone", "$CMD", "$CMD"},
		{"a variable alone with a redirection", "$CMD > out.log", "$CMD"},
		{"a brace alone", "{rm,-rf,/}", "{rm,-rf,/}"},
		{"a glob alone", "./*", "./*"},
		{"a command substitution alone", "$(cat cmd)", "$(cat cmd)"},
		{"a quoted array alone", `"${cmd[@]}"`, `"${cmd[@]}"`},
		{"an unquoted variable alone under a launcher", "sudo $CMD", "$CMD"},
		{"an unquoted variable as an option value with nothing after it", "sudo -u $user", "$user"},
	}

	unchanged := []struct {
		name     string
		command  string
		expected string
	}{
		{"a quoted variable alone", `"$tool"`, "allow"},
		{"a quoted variable alone with a redirection", `"$tool" > out.log`, "allow"},
		{"a quoted variable alone under a launcher", `sudo "$tool"`, "allow"},
		{"a quoted variable as sudo's option value", `sudo -u "$user" ls -la`, "allow"},
		{"a quoted variable as nice's option value", `nice -n "$n" make build`, "allow"},
		{"a quoted command substitution as an option value", `sudo -u "$(whoami)" ls -la`, "allow"},
		{"a variable in a find expression", `find . -name "$pattern" -print`, "allow"},
		{"a lone removal inside the project", "rm -rf ./build", "allow"},
		{"a fixed name with a variable argument", `echo "$CMD" -rf /`, "allow"},
	}

	for _, outside := range []string{"ask", "deny", "off"} {
		g := newGuard(t, true, `"shell_guard_outside_project":"`+outside+`"`)

		// A session that keeps no count is on its first attempt, so an ask refuses.
		want, suffix := outside, ""
		switch outside {
		case "ask":
			want, suffix = "deny", "; rewrite the command (attempt 1 of 3)"
		case "off":
			want = "allow"
		}

		for _, tc := range judged {
			t.Run(outside+" "+tc.name, func(t *testing.T) {
				reason := ""
				if want != "allow" {
					reason = "agentic-shell-guard: cannot judge: the command name " + tc.word + " is not fixed text" + suffix
				}

				decision, said := g.judge(tc.command, "{project}")
				if decision != want || said != reason {
					t.Errorf("a command name the shell builds should take the outside verdict, naming the word, got %s %q", decision, said)
				}
			})
		}

		for _, tc := range unchanged {
			t.Run(outside+" "+tc.name, func(t *testing.T) {
				if decision, said := g.judge(tc.command, "{project}"); decision != tc.expected {
					t.Errorf("the command should keep its verdict, got %s %q", decision, said)
				}
			})
		}
	}
}

func TestAnUnparsableCommandIsDeniedWithEveryGuardOff(t *testing.T) {
	g := newGuard(t, true, `"shell_guard_outside_project":"off","shell_guard_find_delete":"off",`+
		`"shell_guard_lone_removal":false,"shell_guard_expansion":false,"shell_guard_nested_code":false,"shell_guard_mktemp":false`)

	decision, reason := g.judge("echo 'unterminated", "{project}")
	if decision != "deny" || reason != "agentic-shell-guard: cannot parse the command; refusing" {
		t.Errorf("a command the guard cannot parse should be denied whatever the options, got %s %q", decision, reason)
	}
}

func TestTheMktempRefusalPointsAtTmp(t *testing.T) {
	cases := []struct {
		name    string
		scratch bool
		reason  string
	}{
		{"with .tmp", true, "agentic-shell-guard: mktemp is banned; write scratch files under the project's .tmp/ instead"},
		{"without .tmp", false, "agentic-shell-guard: mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reason := newGuard(t, tc.scratch, "").judge("mktemp -d", "{project}")
			if reason != tc.reason {
				t.Errorf("the mktemp refusal should point at .tmp/, got %q", reason)
			}
		})
	}
}

func TestTheGuardNamesWhatItAsksAbout(t *testing.T) {
	g := newGuard(t, true, "")

	if _, reason := g.judge("rm -f /etc/a /etc/b", "{project}"); reason != "agentic-shell-guard: outside project: /etc/a /etc/b" {
		t.Errorf("the prompt should list the outside targets as typed, got %q", reason)
	}

	if _, reason := g.judge("ls && rm a", "{project}"); !strings.HasPrefix(reason, "agentic-shell-guard: rm runs alone: ") {
		t.Errorf("the refusal should say a removal runs alone, got %q", reason)
	}
}

func TestAProjectAtHomeOrRootIsNone(t *testing.T) {
	for name, at := range map[string]string{"home": "home", "root": "root", "unset": "unset"} {
		t.Run(name, func(t *testing.T) {
			g := newGuard(t, true, "")
			g.env.ProjectDir = map[string]string{"home": g.tree.Home, "root": "/", "unset": ""}[at]

			if decision, _ := g.judge("rm foo", "{project}"); decision != "ask" {
				t.Errorf("every removal should ask without a project, got %s", decision)
			}

			if decision, _ := g.judge("rm {tmp}/x", "{project}"); decision != "allow" {
				t.Errorf("a removal under tmp should pass without a project, got %s", decision)
			}
		})
	}
}

func TestAnUnreadableCallIsRefused(t *testing.T) {
	for name, input := range map[string]string{"not json": "not json", "no command": `{"cwd": "/"}`} {
		t.Run(name, func(t *testing.T) {
			var out, errs bytes.Buffer

			status := shellguard.Main(context.Background(), shellguard.Env{}, shellguard.Streams{
				In: strings.NewReader(input), Out: &out, Err: &errs,
			})
			if status != 2 || !strings.Contains(errs.String(), "agentic-shell-guard: could not read the command; refusing") {
				t.Errorf("an unreadable call should block the command, exit %d %q", status, errs.String())
			}
		})
	}
}

func TestShellGuardOffOnTheMachine(t *testing.T) {
	g := newGuard(t, true, `"shell_guard_enabled":false`)

	for _, command := range []string{"rm -rf ~/x", "mktemp", "echo 'unterminated", "ls && rm a"} {
		if decision, _ := g.judge(command, "{project}"); decision != "allow" {
			t.Errorf("shell-guard off on the machine should let %q run, got %s", command, decision)
		}
	}
}

func TestTheMktempBanRestsOnTheScratchSweep(t *testing.T) {
	g := newGuard(t, true, `"scratch_enabled":false,"shell_guard_mktemp":true`)

	if decision, _ := g.judge("mktemp -d", "{project}"); decision != "allow" {
		t.Errorf("with the scratch sweep off, mktemp should pass, got %s", decision)
	}

	if decision, _ := g.judge("rm -rf ~/x", "{project}"); decision != "ask" {
		t.Errorf("with the scratch sweep off, every other guard should still judge, got %s", decision)
	}
}
