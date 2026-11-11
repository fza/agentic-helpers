# Withheld output

`agentic-shell-guard output` runs after every tool call. A line of output from outside the project
that looks like a credential reaches you as `[agentic-shell-guard: credential withheld (<rule ids>)]`
in its place, with no note. Every other line is as the tool returned it. The file or response
itself is untouched.

| Output | Counts as |
|---|---|
| Read, Grep, NotebookRead | by the path named: inside the project (through symlinks) → shown whole |
| Edit, Write, NotebookEdit | always scanned, project files included: a credential line in the file they echo comes back withheld; the file itself is written as asked |
| Bash | shown whole only when every command is `cat`, `head`, `tail`, `grep`, `egrep`, `fgrep`, `sed`, `wc`, `sort`, `uniq`, `cut`, `tr` or `nl`, by bare name, only options on its list, all before the first operand (long ones spelled in full), every word literal, every file existing inside the project, no `cd`, no glob, no variable, no script, no nested code. Anything else, a test run included → scanned |
| every other tool (WebFetch, MCP tools, a subagent) | outside |

`the credential scan of this <tool> output could not finish` → every string replaced by the marker
with `scan-incomplete`. Run the same call again. An Edit, Write or NotebookEdit result gets the
markers without this note: its write already landed.

The user sets `shell_guard_secret_output` (`all` unless set, `files` for Read, Grep, NotebookRead,
Edit, Write, NotebookEdit and Bash only, `off`) and `shell_guard_secret_output_skip` (tool names or
patterns never scanned), the skip list in Claude Code's settings only: `config.yaml` never sets it.

## Searches and refused reads

A Grep line, or a Bash `grep`, `egrep` or `fgrep` line, whose `<file>:` prefix names a file holding
a credential on that line comes back as `[agentic-shell-guard: credential withheld (secret-key-name)]`
(or `private-key`, `age-secret-key`, `putty-private-key`,
`[agentic-shell-guard: credential withheld (credential-store)]`), inside the project too. A file
holding one:

| Holds | When |
|---|---|
| a secret-named key with a real value | a config file (`.yaml`, `.json`, `.toml`, `.ini`, `.env`, `.npmrc`, `credentials` and the like) and a key ending in `password`, `pass`, `secret`, `token`, `apikey`, `credentials`, `authorization`, `cookie`, `api key`, `access key`, `private key`, `client secret` and the like; `${NAME}`, `$NAME`, `{{ ... }}`, `<...>`, `***`, empty and `null` are no value |
| a private key | a PEM `PRIVATE KEY` block, an `AGE-SECRET-KEY-`, a PuTTY key, in any file |
| a credential store | `.netrc`, `.pgpass`, `.git-credentials`, `.htpasswd` |

A Read, NotebookRead or plain Bash reader of such a file is refused, the lines handed back with
`[withheld]` in place of each inside a credential. A Read range clear of every credential reads as
is. The user sets `shell_guard_secret_read` (`deny` unless set, `off`) and
`shell_guard_secret_read_skip` (`<project>=<glob>[;<glob>]` entries, globs relative to the project),
the skip list in Claude Code's settings only. An entry that does not parse is named at session start:
`agentic-helpers: shell_guard_secret_read_skip: <entry> ignored; <why>`.

**Work from the copy.** Tell the user the path and what it holds; never read the file another way,
nor around the withheld lines (a shifted range, another tool, a script). A credential you must
change → the user edits it, or skips the path in `shell_guard_secret_read_skip`.

- `<path> holds a credential (a private key store by its path); reading it is refused` → a `.p12`,
  `.pfx`, `.jks`, `.keystore` or GnuPG private key; no copy.
- `<path>: file too large to check; reading it is refused` → a config file over 8 MiB for a Bash
  reader; Read a range instead.

A Read call it cannot read is blocked: `agentic-shell-guard: could not read the call; refusing`.
