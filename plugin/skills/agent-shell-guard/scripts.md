# Scripts

`bash x.sh`, `sh x.sh`, `source x.sh`, `. x.sh`, and `./x` or `bin/x` with a shell's `#!` line or
none, are read and judged by the same guards as a typed command, relative to the working directory
a `cd sub && ...`, a lone `cd sub;` or `cd sub || exit 1;` moves (a `cd` behind another command,
`make && cd sub && ./x.sh`, moves the rest of its `&&` chain only).

- **The script's own folder**: inside a script run as one (not sourced), a `cd` into
  `"$(dirname "$0")"`, `"$(dirname "${BASH_SOURCE[0]}")"` (bash only), `"${0%/*}"` or
  `"$(cd "$(dirname "$0")" && pwd)"`, each optionally followed by fixed text such as `/..`, moves
  to that folder, relative to the path the call named the script by. A variable the script fixes
  counts as written out ([reference.md](reference.md#fixed-values)). On a command line `$0` names
  no folder.

- **Inside a script a removal is judged by its targets only**: `ls` then `rm -rf build` passes;
  `rm -rf ~/x` and `rm -rf $DIR` ask. Nothing is refused: what would deny in a typed command asks,
  as `in the script <name>: <reason>`, a refusal's reason included. Fix the script as that reason
  says (a target spelled out and inside the project, no `mktemp`), or run its commands as Bash
  calls. It prompts at once: rewriting the call changes nothing.
- **Another interpreter's `#!` line** (python or any other) is judged as that interpreter's inline
  code, asking too; so is a fish script (`fish x.fish`, or a fish `#!` line), asking
  `in the script x.fish: inline code for fish names a removal or mktemp; refusing` where its text
  names a removal or `mktemp`, a comment or string among it.
- **A script written by `cat > x.sh <<'EOF'` in the same call** is judged from the here-document.
- **A script an earlier command of the call names as an argument** (`cp new.sh x.sh`,
  `sed -i ... x.sh`, `curl -o x.sh`, any command it does not know) is not read and asks, unless
  that command only reads or changes metadata: `cat`, `head`, `tail`, `less`, `more`, `wc`, `grep`,
  `rg`, `file`, `stat`, `ls`, `test`, `[`, `diff`, `cmp`, `shasum`, `md5`, `od`, `xxd`, `chmod`,
  `chown`, or an earlier run of the same script.

## Unread

`cannot read the script <name> (<why>)`: a script the call runs could not be read. `<why>`:

- `no such file`, `cannot open it`, `over 1 MiB`, `scripts run scripts too deep`,
  `it does not parse as bash`: as they say.
- `its path is built at run time`: `source "$venv/bin/activate"`.
- `the directory it runs in is not known`: a `cd` in a subshell, followed by `|| echo`, or to a
  variable the text does not fix before it, or one behind another command in an earlier
  statement's `&&` chain.
- `the command writes it before running it`: written by anything but a here-document, or named by
  an earlier command that is no reader.

Spell the path out, run it from a plain `cd dir && ...`, or write it with `cat > x.sh <<'EOF'`;
else the user decides.
