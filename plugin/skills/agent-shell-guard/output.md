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
