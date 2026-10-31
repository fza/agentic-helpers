# Shell-guard: usage

A stray `rm -rf` outside the project is the one mistake an agent cannot undo, and a careless chain
such as `cd ~ && rm -rf x` slips past any rule that reads targets without parsing the command.
`agentic-shell-guard` runs before every Bash call, and every Monitor call, whose command runs in the
same shell, parses the command as bash, and holds an agent's
removals (`rm`, `rmdir`, `unlink`, `git rm`) and the sources of its moves (`mv`, `git mv`) to the
project and `/tmp`, asks before `find -delete`
and before a command holding what looks like a credential, and bans `mktemp`. A command missing on
the machine, or a usage its flavour of a command gets wrong, is refused with the command that works
(see [Command alternatives](#command-alternatives)). After every tool call it withholds each line of output from outside the project that looks like a credential (see [Credentials in tool output](#credentials-in-tool-output)). A removal named inside a quoted argument (a sed script, a grep pattern, a commit
message) is text and passes; code handed to `eval`, `trap`, a shell's `-c`, zsh's `sched`, `npx -c`,
`pnpm exec -c`, a tmux command running a shell command (`new-session`, `new-window`,
`split-window`, `respawn-pane`, `respawn-window`, `display-popup`, `run-shell`, tmux's own `-c`),
zsh's `functions[name]=` or `aliases[name]=`, or the standard input of a shell, `at` or `batch` by
a here-document or here-string is parsed again. A shell script the command runs (`bash x.sh`,
`source x.sh`, `./x`) is read and judged by the same guards, asking where they would refuse (see
[Scripts a command runs](#scripts-a-command-runs)). A variable arithmetic reads (`(( x ))`, `$(( x ))`,
`[[ x -eq 1 ]]`) whose assigned text is no number is parsed as the expression bash evaluates, since
a subscript in it runs its command substitutions; one holding a value known only at run time
(`n=$(wc -l < f); (( n > 1 ))`, `read n`, a loop over `$(...)`) is asked about:
`arithmetic on n reads a value shell-guard cannot see`. `xcrun`, `sandbox-exec`, `npx`,
`screen`, `uv run`, `pnpm exec` and `launchctl submit` are launchers: the command they run is
judged as if it stood alone; `make` runs targets, never a command.

**It judges every project.** Every other piece waits for a directory or a seat; shell-guard's work
is every Bash call, so it always has something to work on. A machine that wants it off turns its
options off.

## What passes

- A **lone removal**: the whole command is one `rm`, `rmdir`, `unlink` or `git rm` and nothing else
  (no other statement, pipe, redirection, subshell, wrapper or assignment), with literal targets,
  each strictly inside the project or `/tmp`. The project is `$CLAUDE_PROJECT_DIR`, the directory
  the session started in; where it is unset, `/` or `~` there is none, so every removal outside
  `/tmp` asks. A command name is read lower-cased, as a case-insensitive filesystem runs `RM` as
  `rm`. A literal is plain text, single or double quotes around plain text, or ANSI-C quoting
  (`$'rm'`, `$'\x72m'`) holding no escape but `\n`, `\t`, `\r`, `\\`, `\'`, `\"` and `\x` with
  two hex digits other than `00`, so `$'rm' -rf /` is judged as `rm -rf /`; one holding any other
  escape (octal, `\u`, `\U`, `\c`, `\e`, a one-digit `\x`, a NUL) is read as an expansion.
  Targets resolve through symlinks, so a link inside the project pointing elsewhere is elsewhere.
  A time limit in front of it keeps it lone: `timeout`, `gtimeout`, `uu-timeout` or
  `agentic-helpers bound` with options and duration all fixed text (no variable, substitution, glob
  or brace, quoted or not), or exactly `perl -e 'alarm N; exec @ARGV'`, which bound only how long
  the removal runs, never where or what (`agentic-helpers bound 30 rm -rf ./build` passes).
- Anything naming no removal and no `mktemp`.

## The guards and their options

Every guard is on by default and has its own plugin option (`/plugin`, or `pluginConfigs` in the
user or managed settings). The shared `config.yaml` only tightens one, since any agent can write it:
a switch only to `true`, a verdict only stricter (`off` < `ask` < `deny`), the timeout only shorter;
a looser value there is ignored and named at session start
([plugin usage](../plugin/usage.md#options)).

| Option | Values | Default | Judges |
|---|---|---|---|
| `shell_guard_outside_project` | `ask`, `deny`, `off` | `ask` | a lone removal whose target lies outside the project and `/tmp`, is the project or `/tmp` itself, cannot be resolved (`~user`, a brace, a `..` step, which a symlink before it would carry elsewhere, a glob that can match a name starting with a dot, `..` among them), or that names no target; and a command passed an argument whose name is not fixed text (`$CMD -rf /`, `"$tool" build`, `$'\162m' -rf /`), or passed none with a name that may split into words of its own (`$CMD`, `{rm,-rf,/}`), which could be a removal; and each source of `mv` or `git mv`, wherever it stands in the command, read as a lone removal's target (see [Moves](#moves)) |
| `shell_guard_find_delete` | `ask`, `deny`, `off` | `ask` | `find ... -delete` |
| `shell_guard_secrets` | `ask`, `deny`, `off` | `ask` | a command whose text holds what looks like a credential, found by the shared [credential scanner](../secrets/usage.md) |
| `shell_guard_lone_removal` | boolean | `true` | a removal that is not the whole command |
| `shell_guard_expansion` | boolean | `true` | a removal target built from a variable or command substitution |
| `shell_guard_nested_code` | boolean | `true` | a removal or `mktemp` inside `eval`, `trap`, a shell's `-c` or here-document, `at` or `batch` code, zsh's `sched` or `functions[name]=`, a tmux shell command, text arithmetic evaluates, or inline code for another interpreter (`perl -e`, `python -c`, `node -e`, `awk`, `ssh`, ..., naming `rm`, `unlink`, `rmtree`, `rmSync` and the like), and such code it cannot read or parse, a shell reading its code from a pipe among it; arithmetic on a variable set at run time, and a script the command runs that it cannot read, ask rather than deny |
| `shell_guard_mktemp` | boolean | `true` | `mktemp` anywhere in the command |
| `shell_guard_alternatives` | boolean | `true` | a command missing on the machine, or a usage its BSD flavour gets wrong (see [Command alternatives](#command-alternatives)); off, the session start line naming the machine's substitutions goes too |
| `shell_guard_ask_timeout` | a span (`45s`, `2m`), at most `1h`; a bare number counts seconds | `2m` | how long an ask waits in `auto` and `bypassPermissions` mode before it denies; `0` waits without limit |

A boolean guard denies when on. **A command that does not parse as bash is denied whatever the
options say**: the guard cannot judge it.

- **`ask` or `deny` for removals outside the project is the user's choice.** A hook's `deny` holds in
  every permission mode, `bypassPermissions` included; its `ask` forces a prompt even in auto mode,
  labelled `[plugin:agentic-helpers]`. `ask` keeps a cleanup outside the project possible with the
  user's approval; `deny` suits runs nobody watches, where the user runs such a removal as `! rm ...`.
- **An ask follows the session's permission mode**, for every guard set to `ask`:

  | Mode | The ask |
  |---|---|
  | `default`, `acceptEdits`, `plan`, or none given | a prompt with no time limit: a person approves each call |
  | `auto`, `bypassPermissions` | a prompt with a countdown of `shell_guard_ask_timeout`; nobody answering by then denies it |
  | `dontAsk` | a deny at once, with the guard's reason |

  The countdown is the `PermissionRequest` hook `agentic-shell-guard permission`, which judges the
  call again and answers only a call shell-guard asks about. A person answering first decides the
  call. After three countdowns ran out in a session with no prompt since, an ask denies at once
  until the next prompt: nobody is watching. With `shell_guard_ask_timeout` at `0` the prompt waits
  as in `default` mode. An unattended session in `default`, `acceptEdits` or `plan` mode still
  stalls on an ask.
- **An ask a rewrite fixes refuses twice first.** For a command name that is not fixed text (a
  launcher's option value that may split among them) and a command holding what looks like a
  credential, the first and second ask in a session for that reason come back as a deny,
  `<reason>; rewrite the command (attempt <n> of 3)`; the third is the ask above, put as the mode
  says. Each reason counts apart, per session, so the person is asked only about a command the agent
  could not make judgeable. A guard set to `deny` denies every time and one set to `off` passes;
  a removal outside the project and `find -delete` ask at once, since no rewording makes them
  judgeable.
- **A switched-off supporting guard falls to the outside-project verdict.** The lone-removal,
  expansion and nested-code guards are what let a removal's target be placed before it runs. With one
  of them off, a removal it no longer denies takes `shell_guard_outside_project`'s verdict, reason
  `cannot place: <target> (<why>)`; with that off too, it runs.

## Moves

A move takes a file away from where it was, so `mv ~/x /tmp/x` followed by `rm /tmp/x` would remove
a file outside the project in two calls that each pass. Each source of `mv` and `git mv` is judged
as a lone removal's target, wherever the move stands in the command: `mv ~/x ./` asks
`outside project: ~/x`, `mv "$f" ./` is refused as an expansion, a glob is judged as the pattern it
is. The target is never judged: moving into the project or `/tmp` removes nothing there, and
`mv ./build /tmp/old` passes.

- Every operand but the last is a source. `-t <dir>`, `--target-directory=<dir>` or a short group
  ending in `t` names the target, so every operand is a source; `-S` and `--suffix` take a value;
  `--` ends the options.
- A relative source resolves against the directory the move runs in: the session's working
  directory, moved by a `cd` the walk follows (see [Scripts a command runs](#scripts-a-command-runs)),
  and unresolvable after any other move or after `git -C`, `--git-dir` or `--work-tree`.
- `cp` is not judged: it reads its source and removes nothing, and shell-guard guards no reads.

Moving a file in from outside, a download from `~/Downloads` among them, costs the confirmation a
removal there costs.

## Scripts a command runs

A script the command writes and then runs would otherwise carry any command past every check.
shell-guard reads each script a call runs and judges its text by the guards it judges a command
with, two rules aside:

- **A removal there is judged by its targets alone**: inside the project or `/tmp` it passes,
  outside, unresolvable or with no project it asks, with the reasons a lone removal gets. The
  lone-removal rule does not apply, since a script holds commands beside its removals by nature.
- **Nothing found there is refused**: whatever would deny in a typed command (a removal target
  built from a variable, nested code, `mktemp`, a command name that is not fixed text) asks instead.
  The person can read the script; the agent cannot fix it by rewriting the command.

`bash x.sh` holding `ls` and then `rm -rf build` passes; holding `rm -rf ~/x` it asks
`in the script x.sh: outside project: ~/x`; holding `rm -rf $DIR` it asks
`in the script x.sh: refusing rm with a shell variable or command substitution`. A command typed in
the call keeps every rule: `mktemp && bash x.sh` is still refused.

- **What is read**: the operand of `sh`, `bash`, `zsh`, `dash`, `ksh` or `mksh` given no `-c`, `-s`
  or input path (`bash -e x.sh`, `bash -- x.sh`), the file `source` or `.` reads, and a command named
  by a path (`./x`, `bin/x`, `/opt/tool/run`) whose name is no shell, launcher or interpreter.
- **A file run by its path** is judged by its first line: a shell's `#!` line, or none, as a shell
  script; another interpreter's (`#!/usr/bin/env python3`) as that interpreter's inline code, which
  the nested-code guard judges by the removal calls it names, asking where it names one; any other
  `#!` line, and a file holding
  a NUL byte in its first 8000 bytes (a binary), not at all.
- **Where it is read from**: the session's working directory, moved by a `cd` with a fixed operand
  naming a directory that exists, in three forms, each one where what follows runs only where the
  `cd` took the shell. A `cd` that is a statement of its own or opens an `&&` chain of such cds
  (`cd sub && bash x.sh`) moves every later statement; `cd sub || exit 1` or `cd sub || return`
  does too, since a failed `cd` ends the shell or function there. A `cd` behind another command in a
  top-level `&&` chain (`make && cd sub && ./x.sh`) moves the rest of that chain only: a statement
  after the chain loses the place, since it runs whether or not the chain got as far as the `cd`.
  Any other move (a `cd` in a subshell, a function or a branch, one followed by `|| echo` or
  anything but `exit` or `return`, one to a variable, `pushd`, `popd`, a sourced script that moves)
  loses the place, and a launcher's chdir
  option (`env -C`, `sudo -D`) loses it for what it runs. Code handed to `bash -c`, `eval` or a
  shell's here-document runs where the command stands; code run later (`trap`, `tmux`, `sched`,
  zsh functions) has no place.
- **A here-document the command writes**: `cat > x.sh <<'EOF' ... EOF` or `tee x.sh <<'EOF'`
  before `bash x.sh` is judged from that here-document, never from the disk. A file the command
  writes any other way before running it (`echo ... > x.sh`, a redirection to a path built at run
  time) is not read.
- **A script an earlier command names**: a command before the run naming the script's path as an
  argument (an operand, an option's value as in `-o x.sh`, or `of=x.sh`) may have written it, so
  the script is not read (`cp new.sh x.sh && bash x.sh`, `sed -i s/a/b/ x.sh && ./x.sh`,
  `curl -o x.sh URL && sh x.sh`, `mytool x.sh && bash x.sh`). Exempt are the commands that only
  read or change metadata: `cat`, `head`, `tail`, `less`, `more`, `wc`, `grep`, `rg`, `file`,
  `stat`, `ls`, `test` and `[`, `diff`, `cmp`, `shasum`, `md5`, `od`, `xxd`, `chmod`, `chown`, and
  an earlier run of the same script, itself judged: `chmod +x x.sh && ./x.sh` is judged from the
  file on disk. A command missing from that list costs an ask, never a bypass. A command that
  changes the script without naming it (`git checkout other`, an archive extraction) is not seen.
- **What cannot be read is asked about, not refused**: `cannot read the script <name> (<why>)`, the
  why one of `no such file`, `cannot open it`, `its path is built at run time`,
  `the directory it runs in is not known`, `over 1 MiB`, `the command writes it before running it`,
  `scripts run scripts too deep` (a script running scripts more than 8 deep) and
  `it does not parse as bash`. `source "$venv/bin/activate"` costs one confirmation, not a dead end.
- **A script's own verdict** is an ask carrying `in the script <name>: ` before its reason. It
  prompts at once: rewriting the command cannot change the script. A script another script runs is
  judged the same way.

A script is judged as it is when the call is checked; one changed between the check and the run is
not judged again.

## Command alternatives

Agents write GNU habits. On macOS `timeout` is not installed, so a bounded command fails, and BSD
`sed` reads `sed -i 's/a/b/' f` as a backup suffix and a script named `f`, while `\+` or `\s` in a
basic expression match nothing without an error. Shell-guard carries a built-in table of such
commands, each with what to run instead, and refuses a call that would fail or silently do the
wrong thing, naming the command that works. The table lives in the binary, not in `config.yaml`:
that file is agent-writable, and a wrong launcher declared there would make shell-guard judge the
wrong word.

**Missing commands.** A command not on the `PATH` is stood in for by the first of its alternatives
that is. For `timeout`, missing on macOS unless GNU's gnubin or uutils' uubin comes first on the
`PATH`, that is always the plugin's own `agentic-helpers bound`, whatever else is installed, so the
rewrite is the same on every machine. The refusal names the whole command rewritten, quoting,
pipelines and every other call kept:

    agentic-shell-guard: timeout is not installed here; use: agentic-helpers bound 9 make test

`agentic-helpers bound <duration> <command> [argument...]` takes the duration as `timeout` does,
seconds with an optional fraction and `s`, `m`, `h` or `d`, so the rewrite copies it unchanged; `0`
bounds nothing. It runs the command in a process group of its own; when the duration runs out it
sends the group `SIGTERM`, then `SIGKILL` 5 seconds later, and exits 124. A `SIGINT`, `SIGTERM` or
`SIGHUP` it receives goes to the group. Otherwise it exits with the command's own status (128 plus
the signal where one ended it), 125 when its own arguments are wrong, 126 when the command cannot
run, 127 when it is not found. Options (`-s`, `-k`) have no `bound` form, so the refusal then names
it with its slots, `timeout is not installed here; use: agentic-helpers bound <duration> <command>`.

`gtimeout`, `uu-timeout` and `perl -e 'alarm N; exec @ARGV'` are offered no more. A perl alarm
bounds no Go program: the alarm survives `exec`, and Go's runtime catches `SIGALRM` and ignores it,
so `go test` or any `agentic-*` binary behind it runs on. `gtimeout` and `uu-timeout` bound well, but
differ by machine. All three stay launchers the walk understands, since an agent may still type
them.

Every alternative launching a command is a launcher for every other guard: the command behind
`agentic-helpers bound`, `gtimeout`, `uu-timeout` or the exact perl alarm form is judged as the
command behind `timeout`, so `agentic-helpers bound 9 rm -rf ~/x` asks as outside the project, and
`perl -e 'alarm 9; exec @ARGV' rm -rf ~/x` asks as outside the project rather than refusing as
inline perl code. The perl form counts only where the word after it does not start with `-` (perl
would read it as one more option, `-e` code among them), that word is fixed text with no
whitespace and no shell metacharacter, since perl's `exec` hands such a word to `/bin/sh` when it
stands alone (`perl -e 'alarm 9; exec @ARGV' 'rm -rf ~/x'`), and every word after it stays exactly
one word, so none can vanish and leave it alone (`'rm -rf ~/x' $empty`, `"$@"`, a glob matching
nothing). Any other perl call is inline code.

**Dialects.** A command present in another flavour than the GNU one agents write for has its
binary read, never run, for the strings its flavour carries: GNU sed names `GNU sed` and GNU date
`GNU coreutils`; macOS's carry their project tag (`PROGRAM:sed  PROJECT:text_cmds`) and BSD's usage
line. A binary carrying neither has no flavour and is never refused. The answer is kept per binary
under the cache root (`command-flavours.json`) and read again when the binary changes. On a BSD
flavour, a
usage verified to fail or mislead there is refused, naming the GNU tool where it is installed
(`gsed`, `gdate`) with the command rewritten to it, else BSD's own form:

| Command | Usage refused | BSD's form |
|---|---|---|
| `sed` | `-i` with no suffix word (`sed -i 's/a/b/' f`, `sed -i -e ...`) | `-i ''`, or `-i.bak` |
| `sed` | any long option (`--in-place`, `--expression=...`) | the short option |
| `sed` | `-z` | none: GNU sed, or work line by line |
| `sed` | `\+ \? \| \s \w \b \< \>` in a basic expression (no `-E` or `-r`) | `-E` with `+ ? \|`, `[[:space:]]`, `[[:alnum:]_]`, `[[:<:]]` `[[:>:]]` |
| `sed` | a `0,/re/` address | `1,/re/` where line 1 cannot match |
| `sed` | one-line `a`, `i` or `c` text (`/x/a text`) | `a\` and a newline before the text |
| `date` | `-d` | `date -j -f <format> <value>` |

Only a verified failure gets an entry: `readlink -f` and `grep -P` work on the macOS checked and
have none, and `sed -E 's/a+/X/' f`, `sed -i '' 's/a/b/' f` and a `\+` in replacement text run.
Options are read up to the first operand, as BSD's getopt reads them, and only an expression is
read for escapes: replacement text, `y` strings, labels, file names and appended text are not.

**What is judged.** Every call the command makes by a fixed name, those a launcher runs and those
in a substitution among them. Code handed to another shell (`bash -c '...'`) is not rewritten, and
a command named by a path (`/usr/local/bin/timeout`, `/usr/bin/sed`, `./sed`) is not judged: only
a binary the `PATH` finds by its bare name is read for its flavour, never a file the proposed
command picks. Only absolute `PATH` entries are searched; an empty or relative one (`.`, `bin`)
names a directory under the working directory, where a project's own `sed` could sit.

**At session start** `agentic-helpers check` prints one line naming the substitutions the machine
needs, and nothing where none is (Linux with GNU tools):

    agentic-helpers: this machine: timeout -> agentic-helpers bound; sed and date are BSD

`shell_guard_alternatives` switches the refusals and the line together; with shell-guard off,
neither appears.

## Refusals

Every reason starts `agentic-shell-guard: `:

| Reason | Guard |
|---|---|
| `mktemp is banned; write scratch files under the project's .tmp/ instead` | mktemp, in a project carrying `.tmp/` |
| `mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it` | mktemp, in a project without `.tmp/` |
| `rm runs alone: no command before or after it, no pipe, redirection, subshell or wrapper; run it as its own Bash call with literal targets` | lone removal |
| `refusing rm with a shell variable or command substitution` | expansion |
| `code handed to <runner> holds a removal or mktemp; refusing` | nested code (`eval`, `bash -c`, ...) |
| `inline code for <interpreter> names a removal or mktemp; refusing` | nested code (`perl`, `python3`, ...) |
| `arithmetic on <name> reads a value shell-guard cannot see` | nested code, an ask: arithmetic on a variable set at run time |
| `cannot read the code handed to <runner>; refusing`, `cannot parse the code handed to <runner>; refusing`, `cannot read the code piped to <shell>; refusing` | nested code |
| `cannot read the script <name> (<why>)` | nested code, an ask: a script the command runs that cannot be read (see [Scripts a command runs](#scripts-a-command-runs)) |
| `in the script <name>: <reason>` | an ask: whichever guard judged the script's text, its own reason after the script's name, a refusal's included |
| `outside project: <targets>`, `no project: <targets>`, `unresolvable: <targets>`, `no target` | outside project; several join with `; ` |
| `cannot place: <target> (not alone)`, `(expansion)`, `(nested code)` | outside project, for a supporting guard switched off; `script <name>` is the target for a script that cannot be read |
| `cannot judge: the command name <word> is not fixed text` | outside project, for a command passed an argument whose name the shell builds when it runs, or passed none with a name that may split into words of its own (`$CMD`, `{rm,-rf,/}`, a glob, `$(...)`), while a name wholly in double quotes passing none (`"$tool"`) runs; a launcher's assignment, option or option value holding an expansion asks too (`env FOO=$x make build`, `timeout $secs make test`, `nice -n$n make build`, `sudo -u $user make build`, `nice -n $n make build`, naming that word), and so does an option value or a command name holding a glob or brace (`sudo -u {root,rm} -rf /`, `nice -n ?? make build`), which expands into several words, since the walk cannot tell which word the launcher runs; an option value wholly in double quotes with no `@`, `*` or `!` expansion (`sudo -u "$user" make build`) stays one word and runs |
| `find -delete` | find -delete |
| `the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file` | secrets; `scan-incomplete` as the rule when the scan could not finish |
| `the hook was cut short before its credential scan finished; nothing says whether the command holds one` | secrets, when the hook's own context ended during the scan |
| `<reason>; rewrite the command (attempt <n> of 3)` | the first and second ask for a command name that is not fixed text or a credential, per session and reason |
| `timeout is not installed here; use: <rewritten command>` | command alternatives, a missing command |
| `<command> here is BSD <command>: <usage>; use: <rewritten command>`, `<command> here is BSD <command>: <usage>; instead <form>` | command alternatives, a dialect usage, with and without the GNU tool installed |
| `cannot parse the command; refusing` | always |
| `nobody answered within <span>; <reason>` | an ask in `auto` or `bypassPermissions` mode whose countdown ran out |
| `3 countdowns ran out unanswered since the last prompt; <reason>` | an ask in `auto` or `bypassPermissions` mode after three countdowns ran out |

In `dontAsk` mode an ask comes back as a deny carrying the ask's own reason.

A call the hook cannot read is blocked, exit 2, with `agentic-shell-guard: could not read the
command; refusing` on stderr. With no `agentic-shell-guard` binary installed, the plugin's launcher
refuses every Bash call the same way:
`agentic-helpers: no agentic-shell-guard binary is installed, so shell-guard refuses every Bash call; start a session with network so release v<version> can be fetched, or set AGENTIC_HELPERS_BIN_DIR`.

## Credentials in tool output

An agent reading a file outside the project, such as `~/.config/sdd/config.yaml` with provider keys
in its comments, takes every key into its context, where it can be quoted, filed or sent on. The
guard before a Bash call cannot see what a glob, a script or a variable reads, so a second hook,
`agentic-shell-guard output`, runs after every tool call and scans what the tool returned with the
shared [credential scanner](../secrets/usage.md). Each line of output from outside the project that
holds what looks like a credential reaches the agent as

    [agentic-shell-guard: credential withheld (<rule ids>)]

and the rest of the output as the tool returned it. The marker in place of each line says all a note
would, so a scan that finished adds none; the file or response itself is unchanged. A key over
several lines is withheld on every line it spans. Output with nothing to withhold is left
alone, the hook answering nothing.

Where output counts as outside the project:

| Tool | Outside |
|---|---|
| Read, NotebookRead | the file named lies outside the project once its symlinks are resolved |
| Edit, Write, NotebookEdit | always, under `all` and `files`, whatever the path: Edit's output carries the whole file it edited, and a path judged after the write can be steered by a link swapped in between; inside the project the cost is a withheld line in the echoed file |
| Grep | the path searched, or the working directory where it names none, lies outside the project |
| Bash | unless every command is a known reader (below), every path it names lies inside the project, and nothing else runs |
| every other tool (WebFetch, MCP tools, a subagent's answer) | always, under `shell_guard_secret_output` `all` |

A Bash call shows the project as is only when:

- every command is `cat`, `head`, `tail`, `grep`, `egrep`, `fgrep`, `sed`, `wc`, `sort`, `uniq`,
  `cut`, `tr` or `nl`, named bare (`/bin/cat` is not), with no assignment before it;
- every word is a literal with no glob or brace;
- each reader carries only the options on its own closed list, read as getopt reads them: a group
  of one-letter options letter by letter, a letter taking a value taking the rest of the group or
  the next word (`grep -fe x` reads patterns from the file `e`), and a long option only when
  spelled out in full, never abbreviated (`--files0` and `--deref` count as outside);
- no word after the first operand starts with `-` (`grep x notes.md -n`, `cat a -- b`): GNU reads
  it as an option, BSD as a file, so it counts as outside;
- every file it names, an operand or the value of an option taking a file (`grep -f`,
  `--exclude-from`, `sort -o`, `-T`, `--random-source`), resolves
  inside the project through every symlink; one that does not resolve, a dangling link or a missing
  file, counts as outside. `-` passes only as a plain operand of a reader that reads standard input
  there (every reader but `tr`), never as a file an option takes;
- it changes no directory, holds no nested code (`eval`, a shell's `-c`, a script, a substitution),
  and redirects only from or to project files, `/dev/null` or another descriptor;
- the reader's own program reads no other file and runs nothing: a sed script made only of
  addresses, `!`, braces, `s` and `y` without the `w` or `e` flag, and `p`, `d`, `D`, `n`, `N`, `g`,
  `G`, `h`, `H`, `x`, `l`, `P`, `z`, `=`, `q`, `Q`. Off every list: `grep -R`, `-S` and `-p` (they follow symlinks out of a named
  directory), `sed -i` and `-f`, `sed -l` and `--line-length` (GNU and BSD read `-l` differently),
  `--files0-from` and `--compress-program`. Every script
  sed is given, each `-e` and the first operand where no `-e` is, must pass.

`awk` and `jq` are no readers here: awk's program can read any file and run commands, and jq's
filter language reads the environment and loads modules in spellings no pattern list can close. Any other call, a script
or a test run included, has its output scanned; a project script printing a project credential is
redacted too.

| Option | Values | Default | Sets |
|---|---|---|---|
| `shell_guard_secret_output` | `all`, `files`, `off` | `all` | which output is scanned: every tool's, only Read, Grep, NotebookRead, Edit, Write, NotebookEdit and Bash, or none |
| `shell_guard_secret_output_skip` | comma-separated tool names or patterns | none | tools whose output is never scanned, as `path.Match` reads a pattern (`mcp__sdd__*`); a pattern that does not compile skips nothing |

`config.yaml` sets `shell_guard_secret_output` only stricter (`off` < `files` < `all`) and never
`shell_guard_secret_output_skip`, which can only loosen; both stay settable in Claude Code's settings.

- **A scan that cannot finish withholds everything**: every string of the output becomes the marker
  with rule `scan-incomplete`, and the note says
  `agentic-shell-guard: the credential scan of this <tool> output could not finish, so all of it was withheld (<n> line(s)); the source is unchanged`.
  An Edit, Write or NotebookEdit result carries the markers without the note, since its write
  already landed and running the call again would repeat it.
- **The tool has run**: only what it returns is withheld. A command that sends a file elsewhere
  without printing it is not covered, and a whole line is withheld, its other text with it.
- **A failure lets the output through and says so**, exit 1, which Claude Code shows the user:
  `agentic-shell-guard: could not read the tool output; it reached the agent unscanned`,
  `agentic-shell-guard: internal failure; the tool output reached the agent unscanned` or
  `agentic-shell-guard: could not answer; the tool output reached the agent unscanned`. With no
  binary installed the launcher says
  `agentic-helpers: no agentic-shell-guard binary is installed, so tool output reaches the agent unscanned; ...`,
  exit 1.

## What it does not stop

shell-guard holds a careless command, not an agent set on getting around it: a command named
through a double-quoted variable with no argument (`"$RM"`, `ls | xargs "$RM"`), `env -S`, a script file an
interpreter runs (`python3 x.py`), a script another command of the same call changes without naming
it (`git checkout other && ./x.sh`, an archive extraction) before running it, and inline code removing
through a call it does not name all run unjudged.

## Locking it on

shell-guard guards against mistakes, not against an agent set on getting around it: the plugin can
be disabled in user settings, and its binary lives in the plugin's user-writable data directory. A
machine that wants it on regardless force-enables the plugin and pins the options in managed
settings, which no user or project setting overrides (`/Library/Application Support/ClaudeCode/managed-settings.json`
on macOS, `/etc/claude-code/managed-settings.json` on Linux):

```json
{
  "enabledPlugins": { "agentic-helpers@agentic-helpers": true },
  "pluginConfigs": {
    "agentic-helpers@agentic-helpers": {
      "options": {
        "shell_guard_outside_project": "deny",
        "shell_guard_find_delete": "ask",
        "shell_guard_secrets": "ask",
        "shell_guard_secret_output": "all",
        "shell_guard_lone_removal": true,
        "shell_guard_expansion": true,
        "shell_guard_nested_code": true,
        "shell_guard_mktemp": true,
        "shell_guard_alternatives": true,
        "shell_guard_ask_timeout": "2m"
      }
    }
  }
}
```

## Off on the machine

The plugin option `shell_guard_enabled` switches shell-guard on or off for the machine, on unless set. Off, every Bash call runs unjudged and no tool output is scanned. With `scratch_enabled` off, the `mktemp` ban is off whatever `shell_guard_mktemp` says, and every other guard still judges.
