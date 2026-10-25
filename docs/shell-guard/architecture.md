# Shell-guard: architecture

`agentic-shell-guard` is the plugin's `PreToolUse` hook on `Bash` (`plugin/hooks/hooks.json`, through
`plugin/hooks/run`). `cmd/agentic-shell-guard` resolves `$CLAUDE_PROJECT_DIR`, the home directory,
`/tmp` and the settings files, and `internal/shellguard` decides. It reads the call's `cwd` and
`tool_input.command`, and answers `permissionDecision` `deny` or `ask` with a reason, or nothing to
let the call run. A call it cannot read, and a panic, exit 2, which blocks the call; any other exit
would let it run.

## Parsing

The command is parsed with `mvdan.cc/sh/v3/syntax` as bash: a parse error denies. Words are judged
by their literal value: plain text, single quotes, and double quotes around plain text read as their
value; anything with an expansion (`$x`, `$(...)`, `` `...` ``, `$'...'`) has none. A command's name
is the base name of its first word, lower-cased, so `/bin/rm`, `\rm` and `RM` are `rm`.

## Lone removals

A command is a lone removal when it is one statement, not negated, not in the background, without
redirections or assignments, whose name is `rm`, `rmdir` or `unlink`, or `git` with `rm` as its first
argument. Its arguments after `--`, or not starting with `-`, are its targets. Each resolves to a
physical absolute path: `~` and `~/` against the home directory, a relative one against `cwd`; `~user`
and a brace leave it unresolvable. The deepest existing directory of a path resolves through its
symlinks. A component starting with a dot and holding a glob character (`*?[`) leaves the target
unresolvable, as bash without `globskipdots` matches `..` with `.*`. A target passes when it lies strictly below the project or `/tmp`: the directory itself is
never a target. The project is `$CLAUDE_PROJECT_DIR`, and none where it is unset, `/` or the home
directory. Any target that fails, or no target, takes `shell_guard_outside_project`'s verdict
naming each kind; a non-literal target denies under `shell_guard_expansion`, else takes that verdict
as `cannot place`.

## Everything else

Any other command is walked call by call. A removal (or `git ... rm`) denies under
`shell_guard_lone_removal`, else takes the outside-project verdict as `cannot place: <targets> (not
alone)`. `mktemp` denies under `shell_guard_mktemp`, its reason naming `.tmp/` by whether the project
carries one. `find` with `-delete` takes `shell_guard_find_delete`'s verdict. A launcher (`sudo`,
`env`, `xargs`, `timeout`, `find`, ...) has every judged command among its arguments judged as if it
stood alone.

Code another program runs as shell, `eval`'s arguments, the handler `trap` sets past its options, the word after a shell's
`-c`-carrying option, and the body of a here-document or here-string a shell reads, is joined and
parsed again, and walked with removals and `mktemp` only noted. Under
`shell_guard_nested_code` a noted one denies, and so does code that is no literal or does not parse;
with it off, a noted removal and unreadable code take the outside-project verdict as `cannot place:
code handed to <runner> (nested code)`, and a noted `mktemp` passes. Inline code for another
interpreter cannot be parsed: an argument whose text names `rm`, `rmdir`, `unlink` or `mktemp` as a
word denies under the nested-code guard, and with it off a removal takes the same `cannot place`
verdict. A shell at the end of a pipe, through any launchers before it, that is named no `-c` code and no
script, or is told `-s`, reads its code from the pipe: code it cannot read. Its first operand past
its options is the script, an option taking a value (`-o`, `-O`, fish's `-C`, grouped as `-eo`, and
every long option but the shells' known flags such as `--norc`) taking the next word with it. Only
`-c` or `--command` replaces the input as the code source; fish's `-C` init code runs before it. A shell's code is the word after an option carrying `c`,
or fish's `C`, or after `--command` or `--init-command`. A here-document or here-string fed to another interpreter is judged as its
inline code. Any other guard's verdict found inside nested code counts as found outside it.

Every guard adds a verdict; `deny` outranks `ask`, which outranks letting the call run, and the first
reason given for the strongest stands.

## Options

`internal/pluginconfig` reads the six `shell_guard_*` options from the user and managed settings
files, as every other piece does. A mode other than `ask`, `deny` or `off`, or a file it cannot read,
leaves every option at its default, so a typo never switches a guard off.
