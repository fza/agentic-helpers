# Reference

## Settings

Launchers (`sudo`, `env`, `xargs`, `xcrun`, `sandbox-exec`, `npx`, `screen`, `uv run`, `pnpm exec`,
`launchctl submit`, ...) change nothing: what they run is judged. Only the user turns shell-guard
off (`shell_guard_*` plugin options, in Claude Code's settings). The shared `config.yaml` only
tightens a guard: a looser value there is ignored
(`agentic-helpers: config.yaml may only tighten <key>; <value> ignored`), and
`agentic-helpers config set` refuses it
(`refused: config.yaml may only tighten <key>; loosen it in Claude Code's settings`). Never route
around that; ask the user.

## Extra roots

`shell_guard_extra_roots` (`<project>=<root>[;<root>]`, comma-separated, paths absolute or under
`~`) lets the user open directories outside one project to its sessions: a removal or move source
there, or the root itself, passes as inside the project; every other project still gets
`outside project`. A link out of a root leads outside it. Only the user sets it, in Claude Code's
settings: `config.yaml` never does (`agentic-helpers: config.yaml never sets shell_guard_extra_roots; <value> ignored`),
nor a `CLAUDE_PLUGIN_OPTION_SHELL_GUARD_EXTRA_ROOTS` prefix. A malformed entry is ignored and named
at session start: `agentic-helpers: shell_guard_extra_roots: <entry> ignored; <why>`. Cleaning up in
another project often, with an ask each time: tell the user the option exists; never set it.

## Literal targets

Literal = plain text, quotes around plain text, or `$'...'` with no escape but `\n` `\t` `\r` `\\`
`\'` `\"` and two-digit `\x` (not `\x00`): `$'rm' -rf /` is `rm -rf /`. Any other escape = an
expansion.

## Fixed values

A variable the text itself fixes is judged as its value written out, in a removal target, a move's
source, a `cd` and a script's path:

```bash
OPENGREP_HOME=.tools/opengrep/v1.33.1          # fixed text, or fixed text joined with such variables
rm -f "$OPENGREP_HOME/opengrep.download"       # judged as .tools/opengrep/v1.33.1/opengrep.download
```

It counts only where every assignment to the name anywhere in the text is a statement of its own
(`X=a`, `export X=a`, `readonly X=a`, alone or first in an `&&` / `||` chain), the use is in no
function, and the value holds no `$(...)`, arithmetic, `~` or backslash. Not fixed, with the verdict
an unfixed variable gets: an assignment in `if`, a loop, a function, a subshell or pipeline, behind
`cmd &&`, `X=a cmd`, `+=`; `${X:-y}`; a variable never assigned (the environment's too); and every
variable once the text holds `eval`, `source`, `trap`, `read`, `unset`, `printf -v`, `declare`,
`local`, arithmetic or a command name built at run time. Unquoted (`$X`), the value must hold no
space or glob character. On a command line `rm` still runs alone; fixed values serve moves, `cd`
and scripts.

## Moves

Each source of `mv` / `git mv` (every operand but the target; all of them after `-t <dir>`) is
judged as a lone removal's target, wherever the move stands: `mv ~/Downloads/x ./` asks. The target
is never judged. `cp` is not.

## Time limits

`timeout`, `gtimeout`, `uu-timeout`, `agentic-helpers bound` (options and duration fixed text: no
`$x`, `"$(...)"`, glob or brace, quoted or not) or exactly `perl -e 'alarm N; exec @ARGV'` with the
command as separate words after it keep a removal lone: the program a plain word (no `-`, no
spaces, no shell characters), every word after it fixed text or `"$var"` (never `$var`, `"$@"` or a
glob). Anything else is inline perl code. They bound only how long it runs.

`agentic-helpers bound` runs the command in a process group of its own; when the duration runs out
it sends the group `SIGTERM`, then `SIGKILL` 5 seconds later, and exits 124, as GNU `timeout`
does. Never bound a run with `perl -e 'alarm N; exec @ARGV'`: a Go program (`go test`, any
`agentic-*` binary) ignores the alarm and runs on.

## Arithmetic

`arithmetic on <name> reads a value shell-guard cannot see` (an ask): arithmetic reads a variable
set at run time (`$(...)`, `read`, `mapfile`, `printf -v`, a loop over `$(...)`, the arguments or a
glob or brace): its text could run a command. Run the substitution and the comparison as two calls,
the number typed literally.

## Cannot judge

`cannot judge: the command name <word> is not fixed text`: a command named by an expansion
(`$CMD x`, `"$tool" build`, `$'\162m'`) and passed an argument, or a name that may split into words
of its own even with none (`$CMD`, `{rm,-rf,/}`, a glob): it could be a removal. Spell the name out
(`/usr/local/bin/tool build`); a name wholly in double quotes passing nothing (`"$tool"`) runs. A
launcher's assignment, option or option value holding an expansion, glob or brace
(`env FOO=$x make`, `timeout $secs make`, `sudo -u $user make`, `sudo -u {a,b} make`) asks too: the
walk cannot tell which word runs. Double-quote an option value: `sudo -u "$user" make`.

## Command alternatives

Only the usages the skill lists are refused: `sed -E`, `sed -i ''`, `\+` in a replacement,
`readlink -f` and `grep -P` run. Code handed to `bash -c` and a command named by a path
(`/usr/bin/sed`) are not checked. The user turns this off with `shell_guard_alternatives`.
