---
name: agent-shell-guard
description: 'agentic-shell-guard: the PreToolUse hook on every Bash call that holds removals (rm, rmdir, unlink, git rm) to the project and /tmp, asks before find -delete or a command holding a credential, and bans mktemp; in auto mode an ask nobody answers is denied after a countdown. Use when a Bash call is denied or asked about with a reason starting "agentic-shell-guard:", before writing a removal or a temporary file, and when a cleanup outside the project is needed. Judges every project.'
license: MIT
---

# Shell-guard

A stray `rm -rf` outside the project cannot be undone. Shell-guard parses every Bash command as bash
before it runs. Reason starts `agentic-shell-guard: ` → this hook.

**Every project, every Bash call.** No directory opts in or out. Only the user turns it off
(`shell_guard_*` plugin options).

## What passes

- **A lone removal**: the whole Bash call is one `rm` / `rmdir` / `unlink` / `git rm`, literal
  targets, each strictly inside the project or `/tmp`. Nothing before or after it. Literal = plain
  text, quotes around plain text, or `$'...'` with no escape but `\n` `\t` `\r` `\\` `\'` `\"` and
  two-digit `\x` (not `\x00`): `$'rm' -rf /` is `rm -rf /`. Any other escape = an expansion.
- **Text naming `rm`**: a sed script, a grep pattern, a commit message. Quoted text is no command.

```bash
rm -rf sub/build .tmp/claude/<session>/probe     # passes: one call, literal, inside
cd sub && rm x                                   # denied: not alone. Absolute path instead
rm -rf "$dir"                                    # denied: expansion. Spell the path
```

## Refusals → what to do

| Reason (after `agentic-shell-guard: `) | Do |
|---|---|
| `rm runs alone: no command before or after it, no pipe, redirection, subshell or wrapper; run it as its own Bash call with literal targets` | the removal as its own Bash call, absolute literal targets, no `cd` in front |
| `refusing rm with a shell variable or command substitution` | spell each target out |
| `mktemp is banned; write scratch files under the project's .tmp/ instead` | `.tmp/claude/<session-id>/` (agentic-helpers:agent-scratch) |
| `mktemp is banned; create the project's .tmp/ (gitignored) and write scratch files under it` | ask the user before creating `.tmp/` |
| `code handed to <runner> holds a removal or mktemp; refusing` | no removal inside `eval`, `trap`, `bash -c` or a heredoc fed to a shell; run it directly |
| `inline code for <interpreter> names a removal or mktemp; refusing` | no removal in `python -c`, `perl -e`, `awk`, `ssh` code; a lone `rm` instead |
| `cannot read the code handed to <runner>; refusing`, `cannot parse the code handed to <runner>; refusing`, `cannot read the code piped to <shell>; refusing` | literal code, or none; no code piped into a shell |
| `cannot parse the command; refusing` | fix the quoting |

## Questions → the user decides

`ask` puts the call to the user, labelled `[plugin:agentic-helpers]`. The session's permission mode
decides how:

| Mode | The ask |
|---|---|
| `default`, `acceptEdits`, `plan` | a prompt with no time limit |
| `auto`, `bypassPermissions` | a prompt with a countdown (`shell_guard_ask_timeout`, 120 s unless set); nobody answering → denied |
| `dontAsk` | denied at once, with the reason below |

Questions it asks:

| Reason | Means |
|---|---|
| `outside project: <targets>` | a target outside the project and `/tmp`, or the project or `/tmp` itself |
| `no project: <targets>` | the session started at `/` or `~`: no project, only `/tmp` passes |
| `unresolvable: <targets>` | `~user` or a brace: placed only once the shell expands it |
| `no target` | a removal naming nothing |
| `cannot place: <target> (not alone)`, `(expansion)`, `(nested code)` | a guard the user switched off no longer denies it; its target cannot be placed |
| `cannot judge: the command name <word> is not fixed text` | a command named by an expansion (`$CMD x`, `"$tool" build`, `$'\162m'`) and passed an argument: it could be a removal. Spell the name out (`/usr/local/bin/tool build`). A launcher's assignment or option holding an expansion (`env FOO=$x make`, `timeout $secs make`) asks too: the walk cannot tell which word runs |
| `find -delete` | `find ... -delete` anywhere |
| `the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file` | a token or key typed into the command, found by the shared scanner; rewrite it to read from a variable or a file rather than asking again (`scan-incomplete` = the scan could not finish) |
| `the hook was cut short before its credential scan finished; nothing says whether the command holds one` | the hook's own interrupt ended the scan; it names no credential. Follows `shell_guard_secrets` like a finding. Run the same call again |

The secrets guard follows `shell_guard_secrets`: `ask` unless the user set `deny` or `off`.

A countdown that runs out comes back as a deny:

| Reason (after `agentic-shell-guard: `) | Do |
|---|---|
| `nobody answered within <n>s; <reason>` | nobody was at the prompt; go on without the call, and report it |
| `3 countdowns ran out unanswered since the last prompt; <reason>` | every ask now denies at once until the user prompts again; stop retrying, report what waits on them |

The user may set these to `deny`. Then a removal outside the project is theirs: suggest they run
`! rm ...` themselves. Never route around a refusal (another interpreter, a wrapper, a script): the
guard exists for exactly that call.

## Fails closed

- **Unreadable call** → blocked: `agentic-shell-guard: could not read the command; refusing`. The
  same for `agentic-shell-guard: internal failure; refusing` and
  `agentic-shell-guard: could not answer; refusing`.
- **No binary installed** → every Bash call refused:
  `agentic-helpers: no agentic-shell-guard binary is installed, so shell-guard refuses every Bash call; ...`.
  Tell the user: a session with network fetches it, or `AGENTIC_HELPERS_BIN_DIR`.

## Off on the machine

**Off on the machine** (`shell_guard_enabled: false`) → every Bash call runs unjudged; removals stay yours to keep inside the project. With `scratch_enabled` off, `mktemp` passes.
