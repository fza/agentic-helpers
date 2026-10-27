# Shell-guard: architecture

`agentic-shell-guard` is the plugin's `PreToolUse` hook on `Bash` (`plugin/hooks/hooks.json`, through
`plugin/hooks/run`). `cmd/agentic-shell-guard` resolves `$CLAUDE_PROJECT_DIR`, the home directory,
`/tmp` and the settings files, and `internal/shellguard` decides. It reads the call's `cwd` and
`tool_input.command`, and answers `permissionDecision` `deny` or `ask` with a reason, or nothing to
let the call run. A call it cannot read, and a panic, exit 2, which blocks the call; any other exit
would let it run. `agentic-shell-guard permission` is its `PermissionRequest` hook on `Bash`, which
ends an ask in `auto` and `bypassPermissions` mode (see Permission modes).

## Parsing

The command is parsed with `mvdan.cc/sh/v3/syntax` as bash: a parse error denies. Words are judged
by their literal value: plain text, single quotes, double quotes around plain text, and ANSI-C
quoting (`$'...'`) read as their value; anything with an expansion (`$x`, `$(...)`, `` `...` ``)
has none. A `$'...'` is decoded only where every escape it holds is one whose bash reading is a
single fixed character whatever follows: `\n`, `\t`, `\r`, `\\`, `\'`, `\"`, and `\x` with exactly
two hex digits other than `00`. Any other escape leaves it no value, as an expansion: bash's octal
(one to three digits, wrapped to a byte), `\u`, `\U`, `\c`, a one-digit `\x` and a NUL truncating
the word each read in ways a decoder could get subtly wrong, and a guard reading one string where
bash runs another would let that one through. The reading is
`shellwalk.Literal`, shared with the grounding gate, so `$'rm'` is `rm` to both. A command's name
is the base name of its first word, lower-cased, so `/bin/rm`, `\rm` and `RM` are `rm`.

## Lone removals

A command is a lone removal when it is one statement, not negated, not in the background, without
redirections or assignments, whose name is `rm`, `rmdir` or `unlink`, or `git` with `rm` as its first
argument. Its arguments after `--`, or not starting with `-`, are its targets. Each resolves to a
physical absolute path: `~` and `~/` against the home directory, a relative one against `cwd`; `~user`
and a brace leave it unresolvable. The deepest existing directory of a path resolves through its
symlinks. A component starting with a dot and holding a glob character (`*?[`) leaves the target
unresolvable, as bash without `globskipdots` matches `..` with `.*`; so does a `..` component, which
the kernel takes after any symlink before it, where a lexical clean would not. A target passes when it lies strictly below the project or `/tmp`: the directory itself is
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
stood alone. A call whose name has no literal value (`$CMD`, `"$tool"`, `$(...)`, a `$'...'` holding
an escape outside the decoded set) could be any command, a removal among them: passed at least one
argument, it takes `shell_guard_outside_project`'s verdict, reason `cannot judge: the command name
<word> is not fixed text`, the word as the command spells it; with none it runs. Under a launcher
only the word `shellwalk` marks as the command it certainly runs counts, past the launcher's
options, their values, assignments and counts, since any other word may be an option's value (`sudo
-u $user`, `find -name "$x"`). The walk that finds calls, launchers, nested code and pipe feeds lives in
`internal/shellwalk`, which the grounding gate walks commands through too.

## Credentials

Every command that parses, a lone removal included, has its whole text scanned by
`internal/secrets` (`secrets.Scan` with no file name, so path-scoped rules never fire). A finding
takes `shell_guard_secrets`'s verdict, reason `the command holds what looks like a credential (<rule
ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file`, naming
the rules on the first line found (`secrets.Describe`) and never the matched text. A scan that could
not finish is a finding with the rule `scan-incomplete`. Once the hook's own context has ended, a
finding names no credential, since the scan it cut short found none: it takes the same verdict with
the reason `the hook was cut short before its credential scan finished; nothing says whether the
command holds one`. The guard is added after every other, so
another guard's ask keeps its own reason, while a deny outranks it.

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

`internal/pluginconfig` reads every `shell_guard_*` option from the user and managed settings
files, as every other piece does. `shell_guard_ask_timeout` below 0 reads as 0 and above 3600 as
3600, under the hook's own `timeout` in `hooks.json` (3660 seconds), past which Claude Code would
stop the hook before it denies. A mode other than `ask`, `deny` or `off`, or a file it cannot read,
leaves every option at its default, so a typo never switches a guard off.

## Permission modes

A hook's `ask` forces a prompt in every mode and Claude Code gives it no time limit, so a session
nobody watches stalls on it. The hook input's `permission_mode` decides how an ask is put; a
missing one reads as `default`.

- `default`, `acceptEdits`, `plan`: the `PreToolUse` hook answers `ask`, and the prompt waits for
  the person.
- `dontAsk`: the `PreToolUse` hook answers `deny` with the ask's reason. Claude Code would turn the
  prompt into a deny the agent cannot act on.
- `auto`, `bypassPermissions`: the `PreToolUse` hook answers `ask`. When the prompt is about to
  show, Claude Code runs the `PermissionRequest` hook, `agentic-shell-guard permission`. It judges
  the call again with the same options; for a call shell-guard asks about, it waits
  `shell_guard_ask_timeout` seconds and answers `deny`, message
  `agentic-shell-guard: nobody answered within <n>s; <reason>`. Whichever of the person and the hook
  answers first decides, and a hook stopped by a signal before its countdown ends answers nothing. Claude Code's own prompt for a critical-path removal takes the same shape in
  these modes. With the option at `0`, for a call shell-guard did not ask about, or in any other
  mode, the hook answers nothing and the prompt is the person's.

A countdown that ran out is noted per session under
`$XDG_CACHE_HOME/agentic-helpers/shell-guard/<session>.json` (`~/.cache` where the variable is
unset). The session's last prompt is the newest typed prompt in its transcript (`transcript_path`):
with three countdowns run out since it, the hook denies at once, message
`agentic-shell-guard: 3 countdowns ran out unanswered since the last prompt; <reason>`, until the
person prompts again. A note it cannot read or write counts nothing, which costs only the
deny-at-once.
