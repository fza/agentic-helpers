---
name: agent-shell-guard
description: 'agentic-shell-guard: the PreToolUse hook on every Bash call that holds removals (rm, rmdir, unlink, git rm) and the sources of moves (mv, git mv) to the project and /tmp, judges the shell scripts a command runs as its own code, asks before find -delete or a command holding a credential, refusing an ask a rewrite fixes twice first, bans mktemp, and refuses a command missing here (timeout) or a usage BSD sed or date gets wrong, naming the command that works; in auto mode an ask nobody answers is denied after a countdown. After every tool call it withholds each output line from outside the project that looks like a credential. Use when a Bash call is denied or asked about with a reason starting "agentic-shell-guard:", when output shows "[agentic-shell-guard: credential withheld", before writing a removal or a temporary file, and when a cleanup outside the project is needed. Judges every project.'
license: MIT
---

# Shell-guard

A stray `rm -rf` outside the project cannot be undone. Shell-guard parses every Bash command as bash
before it runs. Reason starts `agentic-shell-guard: ` → this hook.

**Every project, every Bash call**, and every Monitor command. No directory opts in or out.
Launchers (`sudo`, `env`, `xargs`, ...) change nothing: what they run is judged. Only the user turns
it off (`shell_guard_*` plugin options, in Claude Code's settings); `config.yaml` only tightens a
guard ([reference.md](reference.md#settings)). Never route around that; ask the user.

## What passes

- **A lone removal**: the whole Bash call is one `rm` / `rmdir` / `unlink` / `git rm`, literal
  targets, each strictly inside the project or `/tmp`. Nothing before or after it.
- **Text naming `rm`**: a sed script, a grep pattern, a commit message. Quoted text is no command.
- **A move whose sources lie inside the project or `/tmp`** (`mv`, `git mv`); the target is never
  judged.
- **A script whose removals stay inside** (`bash x.sh`, `source x.sh`, `./x`): read and judged by
  the same guards ([scripts.md](scripts.md)).
- **A time limit in front keeps it lone**: `timeout`, `gtimeout`, `uu-timeout`, `agentic-helpers
  bound`, with fixed options and duration.

```bash
rm -rf sub/build .tmp/seats/<session>/probe     # passes: one call, literal, inside
gtimeout 30 rm -rf sub/build                     # passes: a time limit keeps it lone
cd sub && rm x                                   # denied: not alone. Absolute path instead
rm -rf "$dir"                                    # denied: expansion. Spell the path
```

## Read when

| You meet | Read |
|---|---|
| `in the script`, `cannot read the script`; writing or running a script with a removal in it | [scripts.md](scripts.md) |
| `[agentic-shell-guard: credential withheld`, `the credential scan of this`; which tool output is scanned | [output.md](output.md) |
| what counts as literal, a move's sources, a time limit or launcher in front, `cannot judge`, `arithmetic on`, `config.yaml may only tighten`, `agentic-helpers bound`, which sed and date usages are checked | [reference.md](reference.md) |

## Refusals → what to do

| Reason (after `agentic-shell-guard: `) | Do |
|---|---|
| `rm runs alone: no command before or after it, no pipe, redirection, subshell or wrapper; run it as its own Bash call with literal targets` | the removal as its own Bash call, absolute literal targets, no `cd` in front |
| `refusing rm with a shell variable or command substitution` | spell each target out |
| `mktemp is banned; write scratch files under the project's .tmp/ instead` | `.tmp/seats/<seat>/`, else `.tmp/seats/<session-id>/` (agentic-helpers:agent-scratch) |
| `mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it` | ask the user before creating `.tmp/` |
| `code handed to <runner> holds a removal or mktemp; refusing` | no removal inside `eval`, `trap`, `bash -c`, a heredoc fed to a shell, `at` or `batch`, `sched`, `npx -c`, `pnpm exec -c`, a tmux command's shell command (`new-session`, `new-window`, `split-window`, `respawn-pane`, `run-shell`, ...), zsh's `functions[name]=` / `aliases[name]=`, or text arithmetic reads (runner `arithmetic on <name>`: `x='a[$(rm ...)]'; (( x ))`); run it directly |
| `inline code for <interpreter> names a removal or mktemp; refusing` | no removal in `python -c`, `perl -e`, `awk`, `ssh` code; a lone `rm` instead |
| `cannot read the code handed to <runner>; refusing`, `cannot parse the code handed to <runner>; refusing`, `cannot read the code piped to <shell>; refusing` | literal code, or none; no code piped into a shell, `at` or `batch` |
| `arithmetic on <name> reads a value shell-guard cannot see` (an ask) | run the substitution and the comparison as two calls, the number typed literally ([reference.md](reference.md#arithmetic)) |
| `in the script <name>: <reason>` (an ask) | the script's own text got `<reason>`: the user decides. Fix the script as that reason says, or run its commands as Bash calls. It prompts at once: rewriting the call changes nothing |
| `cannot parse the command; refusing` | fix the quoting |

## Command alternatives → run what it names

A call that would fail on this machine, or silently do the wrong thing, is denied with the command
that works. Session start already says what the machine needs:
`agentic-helpers: this machine: timeout -> agentic-helpers bound; sed and date are BSD`. Nothing appears where
nothing is needed (Linux with GNU tools).

| Reason (after `agentic-shell-guard: `) | Do |
|---|---|
| `timeout is not installed here; use: <rewritten command>` | run the rewritten command as given: `agentic-helpers bound <duration> <command>`, the plugin's own bound, whatever else is installed |
| `timeout is not installed here; use: agentic-helpers bound <duration> <command>` | options (`-s`, `-k`) have no `bound` form: write it yourself, without them |
| `<command> here is BSD <command>: <usage>; use: <rewritten command>` | the GNU tool (`gsed`, `gdate`) is installed: run the rewritten command |
| `<command> here is BSD <command>: <usage>; instead <form>` | no GNU tool installed: rewrite as the form says |

Never bound a run with `perl -e 'alarm N; exec @ARGV'`: a Go program (`go test`, any `agentic-*`
binary) ignores the alarm and runs on.

The usages, `<usage>` → `<form>`:

- `-i takes the next word as its backup suffix` → `write -i '' for no backup file, or -i.bak`
- `it takes no long option` → `use the short one`
- `it has no -z` → `install GNU sed (gsed), or work line by line`
- `\+ \? \| \s \w \b \< \> match nothing in a basic expression` →
  `use -E with + ? |, [[:space:]], [[:alnum:]_], [[:<:]] [[:>:]]`
- `a 0,/re/ address matches nothing` → `use 1,/re/ where line 1 cannot match, or install GNU sed (gsed)`
- `a, i and c take their text only after a backslash and a newline` → `write a\ and a newline before the text`
- `it has no -d` (date) → `use gdate -d, or date -j -f <format> <value>`

## Questions → the user decides

`ask` puts the call to the user, labelled `[plugin:agentic-helpers]`. The session's permission mode
decides how:

| Mode | The ask |
|---|---|
| `default`, `acceptEdits`, `plan` | a prompt with no time limit |
| `auto`, `bypassPermissions` | a prompt with a countdown (`shell_guard_ask_timeout`, 2m unless set); nobody answering → denied |
| `dontAsk` | denied at once, with the reason below |

Questions it asks:

| Reason | Means |
|---|---|
| `outside project: <targets>` | a removal target or a move's source outside the project, `/tmp` and the project's extra roots ([reference.md](reference.md#extra-roots)), or the project or `/tmp` itself |
| `no project: <targets>` | the session started at `/` or `~`: no project, only `/tmp` passes |
| `unresolvable: <targets>` | `~user` or a brace: placed only once the shell expands it |
| `no target` | a removal naming nothing |
| `cannot place: <target> (not alone)`, `(expansion)`, `(nested code)` | a guard the user switched off no longer denies it; its target cannot be placed |
| `cannot judge: the command name <word> is not fixed text` | a command named by an expansion (`$CMD x`) could be a removal. Spell the name out (`/usr/local/bin/tool build`); double-quote a launcher's option value: `sudo -u "$user" make` ([reference.md](reference.md#cannot-judge)) |
| `find -delete` | `find ... -delete` anywhere |
| `cannot read the script <name> (<why>)` | `<why>` is `no such file`, `cannot open it`, `its path is built at run time`, `the directory it runs in is not known`, `over 1 MiB`, `the command writes it before running it`, `scripts run scripts too deep` or `it does not parse as bash`. Spell the path out, run it from a plain `cd dir && ...`, or write it with `cat > x.sh <<'EOF'`; else the user decides ([scripts.md](scripts.md#unread)) |
| `the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file` | a token or key typed into the command, found by the shared scanner; rewrite it to read from a variable or a file rather than asking again (`scan-incomplete` = the scan could not finish) |
| `the hook was cut short before its credential scan finished; nothing says whether the command holds one` | the hook's own interrupt ended the scan; it names no credential. Follows `shell_guard_secrets` like a finding. Run the same call again |

The secrets guard follows `shell_guard_secrets`: `ask` unless the user set `deny` or `off`.

**Rewrite first.** The asks a rewrite fixes, `cannot judge: the command name <word> is not fixed text`
and `the command holds what looks like a credential (...)`, come back as a deny the first and second
time in a session for that reason:

    agentic-shell-guard: <reason>; rewrite the command (attempt <n> of 3)

Rewrite it as the reason says: the name spelled out, the credential as `"$NAME"`. The third attempt
asks the user, as the mode table says. Each reason counts apart, per session. `outside project`,
`find -delete` and every other ask prompt at once: no rewording makes them judgeable.

A countdown that runs out comes back as a deny:

| Reason (after `agentic-shell-guard: `) | Do |
|---|---|
| `nobody answered within <span>; <reason>` (`2m`, `45s`) | nobody was at the prompt; go on without the call, and report it |
| `3 countdowns ran out unanswered since the last prompt; <reason>` | every ask now denies at once until the user prompts again; stop retrying, report what waits on them |

The user may set these to `deny`. Then a removal outside the project is theirs: suggest they run
`! rm ...` themselves. Never route around a refusal (another interpreter, a wrapper, a script): the
guard exists for exactly that call.

## Withheld output

`agentic-shell-guard output` runs after every tool call. A line of output from outside the project
that looks like a credential reaches you as

    [agentic-shell-guard: credential withheld (<rule ids>)]

in its place. **Need the value?** Don't read it: use it by reference, `"$NAME"` or a file a command
reads at run time. Never route around the marker (another tool, an encoding, a copy into the
project). A credential in a project file you must fix → Read it by its project path; it shows whole.

- `agentic-shell-guard: the credential scan of this <tool> output could not finish, so all of it was withheld (<n> line(s)); the source is unchanged`
  → run the same call again.
- `agentic-shell-guard: could not read the tool output; it reached the agent unscanned`,
  `agentic-shell-guard: internal failure; the tool output reached the agent unscanned` and
  `agentic-shell-guard: could not answer; the tool output reached the agent unscanned` go to the
  user; the output passed as the tool returned it.

## Fails closed

- **Unreadable call** → blocked: `agentic-shell-guard: could not read the command; refusing`. The
  same for `agentic-shell-guard: internal failure; refusing` and
  `agentic-shell-guard: could not answer; refusing`.
- **No binary installed** → every Bash call refused:
  `agentic-helpers: no agentic-shell-guard binary is installed, so shell-guard refuses every Bash call; ...`.
  Tell the user: a session with network fetches it, or `AGENTIC_HELPERS_BIN_DIR`.

## Off on the machine

**Off on the machine** (`shell_guard_enabled: false`) → every Bash call runs unjudged and no output is withheld; removals stay yours to keep inside the project. With `scratch_enabled` off, `mktemp` passes.
