# Shell-guard: architecture

`agentic-shell-guard` is the plugin's `PreToolUse` hook on `Bash` and `Monitor` (`plugin/hooks/hooks.json`, through
`plugin/hooks/run`): Monitor runs its `command` in the Bash tool's shell, so it is judged the same way. `cmd/agentic-shell-guard` resolves `$CLAUDE_PROJECT_DIR`, the home directory,
`/tmp` and the settings files, and `internal/shellguard` decides. It reads the call's `cwd` and
`tool_input.command`, and answers `permissionDecision` `deny` or `ask` with a reason, or nothing to
let the call run. A call it cannot read, and a panic, exit 2, which blocks the call; any other exit
would let it run. A Monitor call opening a websocket (`ws`) carries no command and runs none: it
passes. `agentic-shell-guard permission` is its `PermissionRequest` hook on `Bash` and `Monitor`, which
ends an ask in `auto` and `bypassPermissions` mode (see Permission modes). `agentic-shell-guard
output` is its `PostToolUse` hook on every tool, which withholds credentials from tool output (see
Tool output).

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
argument. A time limit before the name is passed over (`shellwalk.TimeLimitRuns`): `timeout` and every
alternative the table names for it, `agentic-helpers bound`, `gtimeout`, `uu-timeout` and the exact
perl alarm form, past
their options, option values and duration, as long as every word before the removal is fixed text
with no glob or brace. A word that is not, such as a quoted option value `"$(rm -rf ~/x)"`, which
the walk skips as one word, sends the whole command to the walk below, which reaches the command it
substitutes. A time limit bounds only how long the removal runs, never where or with what targets,
so the removal behind one is judged as standing alone. Its arguments after `--`, or not starting
with `-`, are its targets. Each resolves to a
physical absolute path: `~` and `~/` against the home directory, a relative one against `cwd`; `~user`
and a brace leave it unresolvable. The deepest existing directory of a path resolves through its
symlinks. A component starting with a dot and holding a glob character (`*?[`) leaves the target
unresolvable, as bash without `globskipdots` matches `..` with `.*`; so does a `..` component, which
the kernel takes after any symlink before it, where a lexical clean would not. A target passes when it lies strictly below the project or `/tmp`: the directory itself is
never a target. The project is `$CLAUDE_PROJECT_DIR`, and none where it is unset, `/` or the home
directory. A target also passes when it is, or lies below, an extra root
(`shell_guard_extra_roots`, read by `pluginconfig.ExtraRoots`) of an entry whose project resolves
to the same physical path as the project; each root has `~` expanded and resolves through
`physical` as the project does, so a link out of it leads outside, and one resolving to `/` or the
home directory is dropped. Any target that fails, or no target, takes `shell_guard_outside_project`'s verdict
naming each kind; a non-literal target denies under `shell_guard_expansion`, else takes that verdict
as `cannot place`.

## Everything else

Any other command is walked call by call. A removal (or `git ... rm`) denies under
`shell_guard_lone_removal`, else takes the outside-project verdict as `cannot place: <targets> (not
alone)`. A move (`mv`, or `git ... mv`) is judged as a lone removal is, over its sources only
(`moveSources`): every operand but the last, or every operand where `-t`, `--target-directory` or a
short group ending in `t` names the target; `-S` and `--suffix` take a value and `--` ends the
options. The sources go to the same path reading as a lone removal's targets, behind a `--` so a
source named like an option still counts, from the directory the walk places the call in
(`shellwalk.Call.Dir`); `git -C`, `--git-dir` and `--work-tree` before `mv` leave that place unknown,
so a relative source is unresolvable. `cp` is not judged: shell-guard guards no reads.
`mktemp` denies under `shell_guard_mktemp`, its reason naming `.tmp/` by whether the project
carries one. `find` with `-delete` takes `shell_guard_find_delete`'s verdict. A launcher (`sudo`,
`env`, `xargs`, `timeout`, `find`, ...) has every judged command among its arguments judged as if it
stood alone. zsh's precommand modifiers (`noglob`, `nocorrect`, `-`) and `repeat N` are launchers
too, since Claude Code's Bash tool runs a command in zsh where that is the login shell, and a
command name opening with `=` (zsh's path expansion, `=rm`) is read without it. `xcrun` (past
`--sdk` and `--toolchain`; `-f` and `--find` only look a tool up), `sandbox-exec` (past `-p`, `-f`,
`-n` and `-D`), `npx` (past `-p`, `--package`, `-w`, `--workspace`) and `screen` (which runs its
command as words, never through a shell) are launchers by name; `uv run`, `pnpm exec` and
`launchctl submit` only through that subcommand, found as the first word past the program's own
options, with `uv --directory` and `pnpm -C` moving what they run elsewhere. Short options grouped
in one word (`screen -dmS name`) take the next word as a value where the last of them does. `make`
is no launcher: its operands are targets, never commands. Every alternative of the command alternatives table that launches a command joins the
launchers from that table (`alternatives.Launchers`): one opening with options, as `gtimeout`, by
name with its entry's value options; one with a subcommand, as `agentic-helpers bound`, only where
that subcommand follows as fixed text, the words after it then read as after a launcher by name;
one opening with fixed words, the perl alarm form, only where
a call spells them exactly (`alternatives.PrefixLaunch`), and its code is then no inline code. The
word after the prefix must not start with `-`, which perl reads as one more option (`-e` code among
them), and must be fixed text holding no whitespace and none of `` $&*(){}[]'";\|?<>~` `` or a
newline, whatever follows it, since perl's `exec` of one such word runs it through `/bin/sh`. Every
word after it must stay exactly one word (`shellwalk.OneKnownWord`: fixed text with no glob or
brace, or wholly double-quoted with no `@`, `*` or `!` expansion), since a word that expands to
nothing (`$empty`, `"$@"`, a glob matching nothing under `nullglob`) would leave the first one
alone at run time. Any other perl call stays inline code. A call whose name has no literal value (`$CMD`, `"$tool"`, `$(...)`, a `$'...'` holding
an escape outside the decoded set) could be any command, a removal among them: passed at least one
argument, it takes `shell_guard_outside_project`'s verdict, reason `cannot judge: the command name
<word> is not fixed text`, the word as the command spells it. Passed none, it runs only where the
name stays one word (`shellwalk.OneKnownWord`, the test a launcher's option value takes below:
wholly double-quoted with no `@`, `*` or `!` expansion, as `"$tool"`); a name that may split (`$CMD`,
`$(...)`, `{rm,-rf,/}`, a glob) takes the verdict with no argument, since `CMD='rm -rf /'; $CMD`
runs a removal with no argument written. Under a launcher
only the word `shellwalk` marks as the command it certainly runs counts, past the launcher's
options, their values, assignments and counts, since any other word may be an option's value (`sudo
-u "$user"`, `find -name "$x"`). A known value option's value counts as its value only when it is
fixed text with no glob or brace, or wholly double-quoted with no `@`, `*` or `!` expansion; any
other (`sudo -u $user`, `nice -n $n`, `sudo -u {root,rm}`) may split into the value and a command of
its own, so the walk takes it for the command and it takes this verdict. A name holding a glob or
brace counts as not fixed text too, since it expands into other words; a `[` counts as a glob only
with a `]` after it in the same word, so the test command `[ -f x ]` is fixed text. The walk that finds calls, launchers, nested code and pipe feeds lives in
`internal/shellwalk`, which the grounding gate walks commands through too.

## Command alternatives

`internal/alternatives` holds the table, `alternatives.Table`, one Go record per command: its name,
its kind (`Missing` or `Dialect`), an ordered list of alternatives a rewrite offers, the ones only
the walk reads (`Walked`: for `timeout`, `timeout` itself, `gtimeout`, `uu-timeout` and the perl
alarm form), and for a launcher its value options and whether it only bounds time. An alternative
is a program, a subcommand where it has one, whether the plugin bundles it (`Bundled`, offered
without a `PATH` lookup, since a hook's `PATH` need not hold the plugin's `bin/`), and an argument
template with named slots: `{opts...}` the call's options, `{dur}` its duration, `{cmd...}` the command it runs
with every word after it. A slot standing as a whole word takes the call's own word; `{dur}` inside
fixed text, as in perl's `'alarm {dur}; exec @ARGV'`, takes the duration in whole seconds. The
position of `{cmd...}` drives both the rewrite and the walk, so every alternative launching a
command is walked by construction. The table lives in code, since `config.yaml` is agent-writable.

`judgeAlternatives` (under `shell_guard_alternatives`) parses the command again and visits every
`CallExpr` with `syntax.Walk`, judging each word `shellwalk.Certain` marks as a command the shell
certainly runs (the first, and each a launcher before it runs), named by fixed text. A command
named by a path is not judged: only a binary the `PATH` finds by its bare name is read for its
flavour, never a file the proposed command picks. `alternatives.Machine` answers for the
machine: `Find` looks a bare name up on the `PATH` the hook was handed, as `exec.LookPath` would on
the process's own (an executable regular file), counting only absolute entries: an empty or
relative one names a directory under the working directory, which the hook does not share with the
shell and where a project's own file could sit. `Flavour` reads only an absolute path, as `Find`
returns.

- **Missing.** The name not found, the first installed alternative stands in. `fill` builds its
  words from the call: options up to the duration (a value option taking its value), the duration,
  then the command. It is not exact where nothing follows the duration, where the template has no
  slot for options the call carries, or where a duration set into fixed text is not a literal
  number with an optional `s`, `m`, `h` or `d`; the reason then names the alternative's form with
  its slots (`generic`). No alternative installed, the call runs.
- **Dialect.** The name found, its arguments are matched against the entry's usages, read up to the
  first operand as BSD's getopt reads them; a word built at run time ends the reading. A usage
  matching, the binary's flavour decides: `Machine.Flavour` resolves the path's symlinks and reads
  at most the first 8 MiB of the regular file for the entry's markers (`Entry.Markers`), GNU's
  first: `GNU sed` and `GNU coreutils`, the package names GNU's binaries carry; for BSD, the Apple
  project tag macOS's binaries carry (`PROGRAM:sed  PROJECT:text_cmds`,
  `PROGRAM:date  PROJECT:shell_cmds`) and BSD's usage lines (`[-i extension]`,
  `usage: date [-jnRu]`). A binary carrying neither, a busybox or one whose marker lies past the
  limit, has no flavour, and nothing is refused. It never runs the binary: running whatever the
  `PATH` holds to learn its flavour would run it before any call is approved. The answer, an empty
  one included, is kept in `<cache root>/command-flavours.json`, keyed by the resolved path,
  modification time and size. A
  sed script is read far enough to tell its expressions (addresses, `s`'s pattern) from replacement
  text, `y` strings, labels, file names and appended text, outside bracket expressions, an escaped
  delimiter being the delimiter's own character; an unknown command ends the reading.

Each finding replaces its call's words from the command on, the deepest launched command first,
and the reason names the first finding: `<lead>; use: <the whole command printed>` with every exact
rewrite applied, or, where the first finding has none, `<lead>; use: <form>` for a missing command
and `<lead>; instead <form>` for a dialect. Every finding denies. Code handed to another shell is
not rewritten: its text is a word of the outer command, and rewriting inside it would change
quoting the agent chose.

`agentic-helpers check`, as the SessionStart hook, asks the same `Machine` for `Line`: each missing
command with the alternative standing in for it, then the names of the commands found in their BSD
flavour, joined with `; ` after `this machine: `, and nothing where both are empty or where
shell-guard or `shell_guard_alternatives` is off. `cmd/agentic-helpers` hands it the `PATH` and
the cache root.

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
`-c`-carrying option, the body of a here-document or here-string a shell, `at` or `batch` reads,
zsh's `sched` command past its `-o` and its time (run as a string, as `eval` runs one), `npx -c`'s
command, the command `pnpm exec` runs in shell mode (`-c`, `--shell-mode`), tmux's own `-c`, the
command past the options of tmux's `new-session`, `new-window`, `split-window`, `respawn-pane`,
`respawn-window`, `display-popup` and `run-shell` (by name, alias, or any prefix of the name, in
every command of a `;` chain), and the value assigned to an element of zsh's `functions`,
`aliases`, `galiases` or `saliases` (`functions[ls]='...'` defines `ls`), is joined and parsed
again, and walked with removals and `mktemp` only noted. Arithmetic (`(( ))`, `$(( ))`, `let`, a
C-style `for`, and `[[ ]]`'s `-eq`, `-ne`, `-lt`, `-le`, `-gt`, `-ge`) evaluates a variable's text
as an expression in turn, and a subscript there runs its command substitutions:
`x='a[$(rm -rf ~/x)]'; (( x ))` runs `rm`. So every variable arithmetic names that the command
assigns, anywhere, is read: text other than a number is handed on as the code `(( text ))`, under
the runner `arithmetic on <name>`, and the names it holds are read in turn; a value naming another
variable (`$y`, `"$y"`) leads to it; an arithmetic expansion is a number. A value known only at
run time (`n=$(wc -l < f)`, any other expansion, what `read`, `mapfile`, `readarray` or
`printf -v` store, a `for` variable over the arguments or over an expanded or patterned word) is
reported unseen (`shellwalk.Visitor.Unseen`): under `shell_guard_nested_code` it asks, reason
`arithmetic on <name> reads a value shell-guard cannot see`, rather than denying, since most such
values are numbers; with the guard off it takes the outside-project verdict as `cannot place:
arithmetic on <name> (nested code)`. Text that is known is judged by what it holds, a removal in
it denying as nested code. A name the command never assigns is not read. Code handed to `sched`,
tmux or `at` that is no literal stays unreadable code, as `eval`'s is, and denies. Under
`shell_guard_nested_code` a noted one denies, and so does code that is no literal or does not parse;
with it off, a noted removal and unreadable code take the outside-project verdict as `cannot place:
code handed to <runner> (nested code)`, and a noted `mktemp` passes. Inline code for another
interpreter cannot be parsed: an argument whose text names `rm`, `rmdir`, `unlink` or `mktemp` as a
word denies under the nested-code guard, and with it off a removal takes the same `cannot place`
verdict. A shell at the end of a pipe, through any launchers before it, that is named no `-c` code and no
script, or is told `-s`, reads its code from the pipe: code it cannot read. A compound command at
the end of a pipe (`{ sh; }`, `if ...; then sh; fi`, a loop) hands the pipe on to every shell inside
it unless that statement redirects its input only to here-documents or here-strings, whose code is
judged in turn. `at` and `batch` at the end of a pipe read their job from it unless an option
before the first operand says otherwise: `-f` names a job file, and `-l`, `-c`, `-r` and `-d` list,
print or remove jobs. A file never replaces the pipe, since any name may be a link to `/dev/stdin`, made
in the same command or before it; a duplicated descriptor (`0<&3`, or `0>&3`, any operator on a
descriptor numbered 0, `00` included) may be the pipe; a named descriptor (`{fd}<<< x`) is a new
one and leaves the input on the pipe; and every
redirection counts, not only the last, since zsh's MULTIOS feeds all of them to the command. Its first operand past
its options is the script, unless it names the input itself (`-`, `/dev/stdin`, a descriptor under
`/dev/fd/` or `/proc/<pid>/fd/`, read cleaned, so `//dev/./stdin` counts), an option taking a value (`-o`, `-O`, fish's `-C`, grouped as `-eo`, and
every long option but the shells' known flags such as `--norc`) taking the next word with it. Only
`-c` or `--command` replaces the input as the code source; fish's `-C` init code runs before it. A shell's code is the word after an option carrying `c`,
or fish's `C`, or after `--command` or `--init-command`. A here-document or here-string fed to another interpreter is judged as its
inline code. Any other guard's verdict found inside nested code counts as found outside it.

## Scripts

`shellwalk.WalkWith` reads the shell scripts a command runs through a `shellwalk.Files`
(`shellwalk.OSFiles` in the hook, a reader confined to the test tree in tests), starting in the
payload's `cwd`; the plain `Walk` reads none. A script is the operand of `sh`, `bash`, `zsh`,
`dash`, `ksh` or `mksh` given no code (`scriptOperand`, reading options as `namesScript` does, and
taking the word after `--`), the file `source` or `.` reads, or a command named by a literal path
holding `/` whose base name is no shell, launcher or interpreter. Only a call the walk is certain
runs counts. A script a shell only parses (`parsesOnly`: `-n`, alone or grouped, and a literal
operand) goes unread, since none of it runs, and still counts as run rather than written, so
`bash -n x.sh && bash x.sh` reads it for the second call. Every option that may take `-n` back
(`+n`, `-i`, `-o`, `-O`, any long option) keeps the read: a list of the harmless ones is short, a
list of the harmful ones spans six shells. A script `fish` runs, or one run by its path whose `#!` line names fish, goes out as
`Visitor.Script` with `fish` as its interpreter: its syntax is no shell's the parser reads, so
shell-guard judges its text by the removal and `mktemp` names `Inline` looks for, as it judges
`python3` code. Asking on every fish script was rejected: it costs a confirmation for harmless ones
with no gain over the name check.

The walk keeps the directory the shell is in (`walker.dir`). A `cd` moves it only where what
follows runs only once the `cd` succeeded (`followedMoves`), with a fixed operand (none means home,
`-` and zsh's two operands mean unknown) naming a directory that exists: a statement of the
command's own, or the opening of an `&&` chain of such cds, moves every later statement; so does
the `cd` of a `cd <dir> || exit [n]` or `|| return [n]` statement (`guardedMove`; `exit` given two
operands fails and ends nothing, so is none). A `cd` behind another command in a top-level `&&`
chain moves the rest of that chain, and the statement after the chain (`directoryMoves.lost`)
starts with no place, since the chain may have stopped before the `cd`. `cd <dir> || <anything
else>` is none: on failure what follows runs in the old directory. Every other directory move a call
makes (`shellwalk.MovesDirectory`) loses the place for the rest of the command; a launcher's chdir option loses it only for what that call launches. A
sourced script that cannot be read or holds a directory move of its own loses it too, since it runs
in the shell itself. Each call carries its place as `Call.Dir`, and code another program runs
carries one to its `Code` event: the call's for `eval`, a shell's `-c`, `npx -c`, `pnpm exec -c` and
a here-document fed to a shell; none for `trap`, `sched`, tmux, zsh's code arrays and arithmetic,
which run elsewhere or later.

### Fixed values

Before a call is offered, `walker.call` writes out every variable the walked text fixes
(`fixedValues`, built once per walk by `fixedValuesOf`), so every visitor, shell-guard and the
grounding gate alike, and the walk's own `cd`, script and write tracking see the call as if its
author had typed the value. A plain `$X` or `${X}` resolves at a use only where every `Assign` to
`X` anywhere in the text is one `topLevelAssignments` accepts (an assignment-only statement of the
file's own, or `export` or `readonly` given no option, alone or the leading statement of an `&&` or
`||` chain, no redirection, no `+=`, index or array), the use lies in no `FuncDecl` body, and the
last such assignment before the use, by offset, holds fixed text or fixed text joined with
variables resolving at its own offset. Reading the whole text rather than only what precedes the
use is deliberate: a loop body runs again after a later assignment in it, and a function body runs
where it is called, so a text-order rule would read a value the shell no longer holds. A value
holding `~` or a backslash is left unresolved rather than imitating tilde expansion and quote
removal. `fixedValues.opaque` leaves every variable unresolved where the text may set one the
parse does not see: `valueSetters` (`eval`, `source`, `trap`, `read`, `unset`, `declare` and the
rest), `printf -v` and `print -v`, any `DeclClause` but plain `export` or `readonly`, arithmetic in
any form, a subscript or slice other than a plain index, `{name}>` redirections, `coproc`, and a
command name that is not fixed text. `shellManaged` names the variables the shell changes itself.
Written out unquoted, a value must match `unquotedSafe` and the text may assign `IFS` nowhere
(`splitChanged`); inside double quotes it goes in as plain text, which `Literal` reads back
unchanged since no resolved value holds a backslash. A word
holding anything else stays as written, so shell-guard judges it as today.

The script's own folder is written out only in a followed `cd`'s operands (`folderOperands`).
`Scripts.Program` carries the script's `$0`, its path as the call spelled it, set by
`walker.script` for a script a shell runs by name or one run by its path, and `Scripts.Bash`
whether bash runs it (the shell's name, or the `#!` line). A sourced script, a command line and
code handed to a shell (`Scripts.At` clears both) have none. `folderWord` reads `$(dirname "$0")`,
`$(dirname "${BASH_SOURCE[0]}")` (bash only, and only where nothing may assign `BASH_SOURCE`),
`${0%/*}` and `$(cd "$(dirname "$0")" && pwd)`, then fixed text, and requires the folder to be a
directory, since `cd x.sh/..` fails and leaves the shell where it was. The `cd` inside the last
form moves only its subshell, so `followedMoves` marks it `directoryMoves.inert`, and the walk keeps
its place past it.

Each statement's writes are noted before its call is offered (`walker.records`): a redirection
writing a file, and `tee`'s operands. `cat` with no operand, or `tee`, fed a here-document or
here-string writes its text, read as bash does: verbatim under a quoted delimiter, with `\\`, `\$`,
`` \` `` and line continuations resolved under a bare one, and unknown where it holds an expansion;
an append adds to what the file held. Any other write leaves the file's text unknown, and a write to
a path that is not fixed text, or cannot be placed, leaves every later script unknown. Once a call
is offered, every fixed argument it names is noted as written with its text unknown
(`walker.names`), and the part after `=` of one holding it (`of=x.sh`, `--output=x.sh`) too:
anything but a reader may write what it names, and a list of writers (`cp`, `sed -i`, `curl -o`,
`dd of=`) misses whichever it does not know, so the reader list fails safe where a writer list
would not. A call is exempt when the command it certainly runs is a reader (`shellwalk.readers`:
`cat`, `head`, `tail`, `less`, `more`, `wc`, `grep`, `rg`, `file`, `stat`, `ls`, `test`, `[`,
`diff`, `cmp`, `shasum`, `md5`, `od`, `xxd`, `chmod`, `chown`) or `tee`, whose writes `records`
notes; the script the call runs itself (`walker.runs` gives its index) is never a write. A fixed
word the walk cannot place (relative, with no known directory) may name any script, so it leaves
every later script unknown.

`walker.textOf` gives a script's text, or why not, which the walk reports as `Visitor.Unread`
with one of the `shellwalk.Unread...` reasons: a path that is not fixed text, no place to resolve a
relative path from, a write before it with no known text, more than `shellwalk.MaxDepth` scripts
deep, missing, unopenable, or over `shellwalk.MaxScript` (1 MiB). A file with a NUL byte in its
first 8000 bytes is no text and no script. A script run by its path is read by its `#!` line,
through `env` and its options and assignments: a shell's or none goes out as `Visitor.Script` with
no interpreter, another interpreter's as `Visitor.Script` naming it, any other as nothing. A shell
or `source` reads the file as shell whatever its first line says. The grounding gate walks shell
scripts only; another interpreter's text is as opaque to it as inline code is.

shell-guard walks a script's text with an inspector in script mode (`inspector.inScript`), in the
directory and depth the event carries; another interpreter's text goes to that inspector's `Inline`.
Two rules differ from a typed command's. A removal there (`rm`, `rmdir`, `unlink`, `git rm`), lone
or not, is judged by its targets alone through the lone removal's path reading, from the directory
its call runs in: the lone-removal rule is set aside, since a script holds commands beside its
removals by nature. And whatever the script's text yields, a deny included (an expansion in a
removal target, nested code, `mktemp`, a command name that is not fixed text), is added as an ask
with `in the script <name>: ` before its reason and no rewrite count: the person can read the
script, and rewriting the command cannot change it. Removals inside code the script hands to a
shell are still noted as nested code, which asks. A command typed in the call keeps every rule. Text that does not parse, and every
unread script, asks under `shell_guard_nested_code`, reason `cannot read the script <name> (<why>)`,
the why `it does not parse as bash` for the former; with the guard off it takes the outside-project
verdict as `cannot place: script <name> (nested code)`. The command alternatives and credential
guards judge the command's own text only.

Every guard adds a verdict; `deny` outranks `ask`, which outranks letting the call run, and the first
reason given for the strongest stands.

## Tool output

`agentic-shell-guard output` (`shellguard.Output`) answers `PostToolUse` for every tool, wired in
the `hooks.json` group without a matcher. It reads `cwd`, `tool_name`, `tool_input` and
`tool_response`, and returns nothing, which leaves the output as the tool returned it, unless it
withholds a line: then `hookSpecificOutput.updatedToolOutput`, which Claude Code shows the agent in
place of the tool's output, and `additionalContext` with a note only for a scan that could not
finish, never for an Edit, Write or NotebookEdit result. It returns before reading stdin
when the piece is off or `shell_guard_secret_output` is `off`, and before scanning when the tool
matches `shell_guard_secret_output_skip` or its output counts as the project's, so neither costs a
scan.

**Outside the project.** The project is `$CLAUDE_PROJECT_DIR` through its symlinks, none where it is
unset, `/` or the home directory, as for removals. A path is inside when it resolves, against `cwd`
for a relative one and through every symlink it passes (a file symlink included), to the project or
below it. A path that does not resolve counts as outside: `~user`, a brace, a `..` step, a dot glob,
a dangling link, a file that does not exist. Read judges `file_path`, NotebookRead `notebook_path`,
Grep `path` or `cwd`, and a tool input that is no object counts as outside. Edit, Write and
NotebookEdit always count as outside, under `all` and `files` alike, whatever their path: Edit's
response carries `originalFile`, the whole file it edited, and the path could only be judged after
the write, when a link swapped in between would steer where it resolves. Inside the project the
cost is a withheld line in the file they echo. Bash is
judged by `ReadsOnlyProject`: `cwd` must be inside the project, and the command is parsed, walked
with `syntax.Walk` for statements (only simple commands, pipelines and lists, subshells and blocks;
no assignment; every redirection a literal project path, `/dev/null`, a descriptor, or a
here-document or here-string with no expansion) and with `shellwalk.Walk` for every call, those a
launcher or a substitution runs included. Each call's words must be literal and unpatterned and its
name a key of `readerTable`. No reader changes directory or runs code, so `cd`, `pushd`, a
launcher's chdir option, a shell, `eval` and every interpreter fail on their name, before any nested
code they carry is reached.

**Reader options** (`readerargs.go`). Each reader has a closed `readerOptions` list, and
`parseReader` reads its arguments as getopt does: a group of one-letter options letter by letter,
where a flag letter goes on and a letter taking a value takes the rest of the group, or the next
word, as a text value, a file or the program; a long option matched exactly against its list (a flag
refusing `=value`, an optional one taking a value only through `=`, a pair of words where a reader
takes one). An option off the list, an abbreviation getopt_long would accept among
them, counts as outside rather than being guessed at: a wrong outside costs only a withheld line. The
first operand is the program where the reader takes one and no option gave it (grep's pattern, sed's
script), tr's operands are sets, and every other operand is a file. A word starting
with `-` after the first operand counts as outside: GNU's getopt reads it as an option and BSD's as
a file, and a parse that guessed either way would misplace the other. Every file, an option's file
value included, must be inside the project; `-` is dropped only as a plain operand of a reader whose
`stdinDash` says it reads standard input there, never as an option's file. A sed script, every
`-e` and the first operand where no `-e` is, must pass `sedScriptReadsNothing`, a parser of the
commands that touch no file; sed's `-l` is off the list, as GNU reads it as a line length and BSD as
line buffering. jq is no reader: its filter language reads the environment and loads modules in
spellings no pattern list can close, so its output is scanned. Under `files` every
tool but Read, Grep, NotebookRead, Edit, Write, NotebookEdit and Bash is left alone; under `all` it
is outside.


**The redaction.** The response is decoded with numbers kept as written, and every string value
(`mapStrings`), at any depth, is scanned by `secrets.Scan` with no file name. Each finding covers
the lines from `Line` to `LastLine`; each covered line becomes
`[agentic-shell-guard: credential withheld (<rule ids>)]`, the distinct rules on it sorted, and the
string is joined again, so every other line, key, number and boolean keeps its place and the output
keeps the tool's shape. A finding of rule `scan-incomplete`, or an ended context, withholds every
non-empty string of the response whole, with that rule, and the note says the scan could not
finish. The count in the note is the lines withheld.

**Failures.** The tool has already run, so the hook cannot block anything: a payload it cannot read,
a panic and an answer it cannot write say so on stderr and exit 1, a non-blocking error Claude Code
shows the user, and the output passes as returned. The launcher, finding no binary, does the same.

## Options

`internal/pluginconfig` reads every `shell_guard_*` option in the layers every other piece's
options take, with `config.yaml` held to `TightenOnly`: a value there looser than the layer below
is dropped and named, and `shell_guard_secret_output_skip` and `shell_guard_extra_roots`, in
`NeverConfig`, are never read from it or the environment. `Resolve` adds each extra-roots entry
`ExtraRoots` ignores to its problems, which `agentic-helpers check` prints. `shell_guard_ask_timeout` is a span read by `internal/span`, a bare number counting seconds; above `1h` it reads as `1h`, under the hook's own
`timeout` in `hooks.json` (3660 seconds), past which Claude Code would stop the hook before it
denies. `shell_guard_secret_output` takes `all`, `files` or `off`, empty
reading as `all`; `shell_guard_secret_output_skip` is kept as written and split on commas by the
hook. A mode other than those a guard takes, or a file it cannot read,
leaves every option at its default, so a typo never switches a guard off.

## Permission modes

A hook's `ask` forces a prompt in every mode and Claude Code gives it no time limit, so a session
nobody watches stalls on it. The hook input's `permission_mode` decides how an ask is put; a
missing one reads as `default`.

- `default`, `acceptEdits`, `plan`: the `PreToolUse` hook answers `ask`, and the prompt waits for
  the person.
- `dontAsk`: the `PreToolUse` hook answers `deny` with the ask's reason. Claude Code would turn the
  prompt into a deny the agent cannot act on.
- Any mode, with `AGENTIC_HELPERS_UNATTENDED=1` in the hook's environment (`UnattendedVar`,
  inherited from the session's process): as `dontAsk`. The `PermissionRequest` hook never runs then,
  since no prompt is put.
- `auto`, `bypassPermissions`: the `PreToolUse` hook answers `ask`. When the prompt is about to
  show, Claude Code runs the `PermissionRequest` hook, `agentic-shell-guard permission`. It judges
  the call again with the same options; for a call shell-guard asks about, it waits
  for `shell_guard_ask_timeout` and answers `deny`, message
  `agentic-shell-guard: nobody answered within <span>; <reason>`. Whichever of the person and the hook
  answers first decides, and a hook stopped by a signal before its countdown ends answers nothing. Claude Code's own prompt for a critical-path removal takes the same shape in
  these modes. With the option at `0`, for a call shell-guard did not ask about, or in any other
  mode, the hook answers nothing and the prompt is the person's.

A countdown that ran out is noted in the slot of its session in
`$XDG_CACHE_HOME/agentic-helpers/shell-guard/sessions.slots` (`~/.cache` where the variable is
unset), a slot file keyed by session id ([memory architecture](../memory/architecture.md#file-formats));
`agentic-helpers dump` prints it as JSON lines. Every write first clears the slots of sessions no
write touched for a week, so the file never grows past the sessions of the last one. Its format 2
takes in the `<session>.json` files an earlier release kept there, then removes them; a state of a
format past 2 is neither read nor written. The session's last prompt is the newest typed prompt in its transcript (`transcript_path`):
with three countdowns run out since it, the hook denies at once, message
`agentic-shell-guard: 3 countdowns ran out unanswered since the last prompt; <reason>`, until the
person prompts again. A note it cannot read or write counts nothing, which costs only the
deny-at-once.

## Asks a rewrite fixes

Two asks name something the agent can fix by rewriting the command: `cannot judge: the command name
<word> is not fixed text` (the name spelled out, a launcher's option value double-quoted) and `the
command holds what looks like a credential (...)` (the value referenced as `"$NAME"`). A guard's
verdict carries the reason's count key (`Verdict.Rewrite`: `not_fixed`, `credential`); the
`PreToolUse` hook, finding the call's verdict an ask with a key, counts the attempt under `attempts` of the session's
slot, the one the countdowns are noted in. Before the third attempt it
answers `deny`, reason `<reason>; rewrite the command (attempt <n> of 3)`; from the third on it
answers the ask, put as the permission mode says, so a person is prompted only for a command the
agent could not make judgeable. Each key counts apart and never resets within a session. The
`PermissionRequest` hook judges without counting: it runs only for a call the `PreToolUse` hook
asked about. Only the deciding verdict counts: where a stronger or earlier guard decides, a
rewritable finding behind it counts nothing.

A setting of `deny` denies every attempt and `off` passes; neither counts. An outside-project
removal, `find -delete` and the cut-short credential scan prompt at once: no rewording makes them
judgeable.

A call without a usable session id (missing, or not a file name) or without a state directory keeps
no count and is on its first attempt every time, so it is refused every time. Claude Code sends a
session id with every call, and a refusal of one of these reasons always leaves the agent a rewrite;
asking at once instead would put in front of the person the very prompt the rule spares them. A
slot it cannot write leaves the count where it was; a state too large for its slot gives up its
oldest countdowns. A hook reads the slot and writes it back whole, so two calls of one session
landing at once may lose one attempt or countdown note; the slot file itself never tears.
