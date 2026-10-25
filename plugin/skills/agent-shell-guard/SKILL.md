---
name: agent-shell-guard
description: 'agentic-shell-guard: the PreToolUse hook on every Bash call that holds removals (rm, rmdir, unlink, git rm) to the project and /tmp, asks before find -delete and bans mktemp. Use when a Bash call is denied or asked about with a reason starting "agentic-shell-guard:", before writing a removal or a temporary file, and when a cleanup outside the project is needed. Judges every project.'
license: MIT
---

# Shell-guard

A stray `rm -rf` outside the project cannot be undone. Shell-guard parses every Bash command as bash
before it runs. Reason starts `agentic-shell-guard: ` → this hook.

**Every project, every Bash call.** No directory opts in or out. Only the user turns it off
(`shell_guard_*` plugin options).

## What passes

- **A lone removal**: the whole Bash call is one `rm` / `rmdir` / `unlink` / `git rm`, literal
  targets, each strictly inside the project or `/tmp`. Nothing before or after it.
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
| `code handed to <runner> holds a removal or mktemp; refusing` | no removal inside `eval` or `bash -c`; run it directly |
| `inline code for <interpreter> names a removal or mktemp; refusing` | no removal in `python -c`, `perl -e`, `awk`, `ssh` code; a lone `rm` instead |
| `cannot read the code handed to <runner>; refusing`, `cannot parse the code handed to <runner>; refusing` | literal code, or none |
| `cannot parse the command; refusing` | fix the quoting |

## Questions → the user decides

`ask` puts the call to the user, labelled `[plugin:agentic-helpers]`, in every permission mode:

| Reason | Means |
|---|---|
| `outside project: <targets>` | a target outside the project and `/tmp`, or the project or `/tmp` itself |
| `no project: <targets>` | the session started at `/` or `~`: no project, only `/tmp` passes |
| `unresolvable: <targets>` | `~user` or a brace: placed only once the shell expands it |
| `no target` | a removal naming nothing |
| `cannot place: <target> (not alone)`, `(expansion)`, `(nested code)` | a guard the user switched off no longer denies it; its target cannot be placed |
| `find -delete` | `find ... -delete` anywhere |

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
