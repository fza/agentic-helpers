# Shell-guard: usage

A stray `rm -rf` outside the project is the one mistake an agent cannot undo, and a careless chain
such as `cd ~ && rm -rf x` slips past any rule that reads targets without parsing the command.
`agentic-shell-guard` runs before every Bash call, parses the command as bash, and holds an agent's
removals (`rm`, `rmdir`, `unlink`, `git rm`) to the project and `/tmp`, asks before `find -delete`
and before a command holding what looks like a credential, and bans `mktemp`. A removal named inside a quoted argument (a sed script, a grep pattern, a commit
message) is text and passes; code handed to `eval`, `trap`, a shell's `-c`, or a shell's standard
input by a here-document or here-string is parsed again.

**It judges every project.** Every other piece waits for a directory or a seat; shell-guard's work
is every Bash call, so it always has something to work on. A machine that wants it off turns its
options off.

## What passes

- A **lone removal**: the whole command is one `rm`, `rmdir`, `unlink` or `git rm` and nothing else
  (no other statement, pipe, redirection, subshell, wrapper or assignment), with literal targets,
  each strictly inside the project or `/tmp`. The project is `$CLAUDE_PROJECT_DIR`, the directory
  the session started in; where it is unset, `/` or `~` there is none, so every removal outside
  `/tmp` asks. A command name is read lower-cased, as a case-insensitive filesystem runs `RM` as
  `rm`.
  Targets resolve through symlinks, so a link inside the project pointing elsewhere is elsewhere.
- Anything naming no removal and no `mktemp`.

## The guards and their options

Every guard is on by default and has its own plugin option (`/plugin`, or `pluginConfigs` in the
user or managed settings):

| Option | Values | Default | Judges |
|---|---|---|---|
| `shell_guard_outside_project` | `ask`, `deny`, `off` | `ask` | a lone removal whose target lies outside the project and `/tmp`, is the project or `/tmp` itself, cannot be resolved (`~user`, a brace, a `..` step, which a symlink before it would carry elsewhere, a glob that can match a name starting with a dot, `..` among them), or that names no target |
| `shell_guard_find_delete` | `ask`, `deny`, `off` | `ask` | `find ... -delete` |
| `shell_guard_secrets` | `ask`, `deny`, `off` | `ask` | a command whose text holds what looks like a credential, found by the shared [credential scanner](../secrets/usage.md) |
| `shell_guard_lone_removal` | boolean | `true` | a removal that is not the whole command |
| `shell_guard_expansion` | boolean | `true` | a removal target built from a variable or command substitution |
| `shell_guard_nested_code` | boolean | `true` | a removal or `mktemp` inside `eval`, `trap`, a shell's `-c` or here-document, or inline code for another interpreter (`perl -e`, `python -c`, `node -e`, `awk`, `ssh`, ..., naming `rm`, `unlink`, `rmtree`, `rmSync` and the like), and such code it cannot read or parse, a shell reading its code from a pipe among it |
| `shell_guard_mktemp` | boolean | `true` | `mktemp` anywhere in the command |
| `shell_guard_ask_timeout` | seconds, at most 3600 | `120` | how long an ask waits in `auto` and `bypassPermissions` mode before it denies; `0` waits without limit |

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
  | `auto`, `bypassPermissions` | a prompt with a countdown of `shell_guard_ask_timeout` seconds; nobody answering by then denies it |
  | `dontAsk` | a deny at once, with the guard's reason |

  The countdown is the `PermissionRequest` hook `agentic-shell-guard permission`, which judges the
  call again and answers only a call shell-guard asks about. A person answering first decides the
  call. After three countdowns ran out in a session with no prompt since, an ask denies at once
  until the next prompt: nobody is watching. With `shell_guard_ask_timeout` at `0` the prompt waits
  as in `default` mode. An unattended session in `default`, `acceptEdits` or `plan` mode still
  stalls on an ask.
- **A switched-off supporting guard falls to the outside-project verdict.** The lone-removal,
  expansion and nested-code guards are what let a removal's target be placed before it runs. With one
  of them off, a removal it no longer denies takes `shell_guard_outside_project`'s verdict, reason
  `cannot place: <target> (<why>)`; with that off too, it runs.

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
| `cannot read the code handed to <runner>; refusing`, `cannot parse the code handed to <runner>; refusing`, `cannot read the code piped to <shell>; refusing` | nested code |
| `outside project: <targets>`, `no project: <targets>`, `unresolvable: <targets>`, `no target` | outside project; several join with `; ` |
| `cannot place: <target> (not alone)`, `(expansion)`, `(nested code)` | outside project, for a supporting guard switched off |
| `find -delete` | find -delete |
| `the command holds what looks like a credential (<rule ids>, line <n>); history, logs and files keep it; reference it instead: "$NAME" or a file` | secrets; `scan-incomplete` as the rule when the scan could not finish |
| `cannot parse the command; refusing` | always |
| `nobody answered within <n>s; <reason>` | an ask in `auto` or `bypassPermissions` mode whose countdown ran out |
| `3 countdowns ran out unanswered since the last prompt; <reason>` | an ask in `auto` or `bypassPermissions` mode after three countdowns ran out |

In `dontAsk` mode an ask comes back as a deny carrying the ask's own reason.

A call the hook cannot read is blocked, exit 2, with `agentic-shell-guard: could not read the
command; refusing` on stderr. With no `agentic-shell-guard` binary installed, the plugin's launcher
refuses every Bash call the same way:
`agentic-helpers: no agentic-shell-guard binary is installed, so shell-guard refuses every Bash call; start a session with network so release v<version> can be fetched, or set AGENTIC_HELPERS_BIN_DIR`.

## What it does not stop

shell-guard holds a careless command, not an agent set on getting around it: a command named
through a variable (`$RM x`), `env -S`, a script file a shell or interpreter runs, and inline code
removing through a call it does not name all run unjudged.

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
        "shell_guard_lone_removal": true,
        "shell_guard_expansion": true,
        "shell_guard_nested_code": true,
        "shell_guard_mktemp": true,
        "shell_guard_ask_timeout": 120
      }
    }
  }
}
```

## Off on the machine

The plugin option `shell_guard_enabled` switches shell-guard on or off for the machine, on unless set. Off, every Bash call runs unjudged. With `scratch_enabled` off, the `mktemp` ban is off whatever `shell_guard_mktemp` says, and every other guard still judges.
