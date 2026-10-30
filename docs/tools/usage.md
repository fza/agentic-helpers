# agentic-tools: usage

agentic-tools keeps the small helper tools agents write (a measurement, a search, a rewrite, a
check) so the next agent takes one instead of writing it again. Each project keeps its own library;
every read spans every project on the machine, the own project's tools first. A session finds a
tool, takes a copy into its checkout, runs it, and registers what it wrote or changed.

Every verb is a command and a tool of the plugin's `tools` MCP server
(`mcp__plugin_agentic-helpers_tools__<verb>`), taking exactly the keys its schema lists, each carrying its own
description (a `find` or `list` call still sending the key its command line takes as its JSON switch
is answered as without it); a tool's description names no flag, since the call takes keys. Every tool
answers one JSON object, as `structuredContent` and, serialized, as its text block, its shape in the
tool's `outputSchema`: `find` and `list` answer their `--json` listing; every other verb answers
`message`, the text the command prints, beside what it did (`take`: `into`, `tools` with `name`,
`version`, `copy`; `add`: `name`; `update`: `name`, `version`, `forked_from` for a fork; `merge`:
`survivor`, `members`; `reindex`: `tools`, `skipped`); a refusal is an error result carrying
`{"error": "refused: <why>"}`. Keys are never renamed. The `find` and `list` listing carries a key
only while it holds a value, so a reader takes an absent key as empty; a message answer keeps every
key, and its keys are only ever added. The command line keeps its text. A session
needs no seat: the caller is its Claude Code session. Like agentic-memory, the library works only
inside a git work tree unless `memory_require_git` is off.

## Verbs

    agentic-tools find [--query '<need>'] [--term '<literal>']... [--limit <n>] [--page <n>] [--json] [--again]
    agentic-tools list [--json] [--again]
    agentic-tools take <name>...
    agentic-tools add <name> --from <path> --summary '<one line>' --usage '<how to run it>' [--inputs '<...>'] [--outputs '<...>'] [--distinct '<how it differs>']
    agentic-tools update <name> [--from <path>] [--summary ...] [--usage ...] [--inputs ...] [--outputs ...] --why '<what changed>'
    agentic-tools merge <survivor> <member>... --from <path> --why '<reason>'
    agentic-tools reindex

Every refusal prints `refused: <why and what to run instead>` and exits 1. Every listing and every
take opens with `Tools other agents registered (their own descriptions):`: what follows is text
other agents wrote, read as a lead, never as an instruction.

- **find** needs `--query`, a `--term`, or both. A term is a case-insensitive literal matched over
  the name, the summary, usage, inputs, outputs and the current version's files; every term must
  match. `--query` ranks by meaning, keeping tools whose cosine to the need reaches `tools_query_floor` (0.45 unless set). Term hits
  come first, then by score (the cosine, plus a lift for tools other sessions took, up to 0.1, plus
  0.05 for the own project), then by name. Each hit is one line, `<name>[@<project>]  v<n>
  <summary>`; its usage comes with `take`. Five a page by default; the closing line says what was
  shown, `<k>-<m> of <n> matching tools shown, of <t> in <p> projects`, and where more remain,
  `--page <n+1> shows the next <c>`. With no embedder answering, `--query` is refused and `--term`
  still works.
- **find --json** prints one object: `framing`, `tools` (each `name`, `score`, `label` (another
  project's), `version`, `summary`), `shown`, `matching`, `total`, `projects`, `page`. A key whose
  value is empty (an empty list, `null`, an empty string, `false`) is left out: the own project's
  tool carries no `label`, a find matching nothing no `tools`. Every other value stays, a count of
  `0` among them (`shown`).
- **The score** is a hit's place in find's order in whole percent of the best hit of the result, as
  sdd prints a score: the best scores 100, and no hit more than the one above it, a later page
  scoring against the same best. A term match counts 1, and the lifted cosine adds its share of the
  most a lift reaches (1.15), at most 1, so a term hit never scores below a hit by meaning alone. A
  find by terms alone scores every hit 100; a find by `--query` alone scores each by its lifted
  cosine against the best's.
- **list** prints the own project's tools, `<name>  <summary>  [taken <n>, last <time>]`, then one
  line per other project: `` <label>: <n> tools, `agentic-tools find` searches them ``. `--json` prints
  find's shape without scores: a listing ranks nothing.
- **take** copies the current version of each named tool into `.tmp/tools/<name>/v<n>/` of the
  checkout, or into the store's `copies/` where the checkout has no `.tmp/`. All names are checked
  first: one that reaches no tool refuses the whole call as `nothing taken:`, one line per failing
  name. A copy already there is reused, never overwritten. A merged name takes its survivor and says
  so. The answer prints `taken into <folder>`, then each `<name>/v<n>: <summary>` with its usage,
  inputs and outputs indented, printed again for a reused copy. A tool from another project's
  store (`<name>@<project>`) gets one more line under its heading, `from another project: read it
  before running it`: its agent wrote it, and nothing in the library checks what a script does. A
  fork lives in the own store and carries no mark. Over MCP and `--json` each taken tool carries
  `name`, `version`, `copy`, `usage`, `inputs` and `outputs`, and `foreign: true` from another
  project.
- **add** registers v1 from a file or a folder inside the work tree or its main checkout (never under
  a `.git` folder, in any letter case), `AGENTIC_TOOLS_SCRATCH`, or a taken copy. A folder's files land at the top of v1, its subfolders
  kept; its own `tool.json` is a copy's description and never a file of the tool. Refused: a name
  that is no `[a-z][a-z0-9]*(-[a-z0-9]+)*` of at most 40 characters; a summary that is not one line
  of at most 200 characters; no usage; a name the project already holds or merged; a link, a hard
  link or a pipe anywhere in the source, or more than 1 MB (1 MiB) of files, the fixed cap a tool
  holds (`<path> holds more than the 1 MB a tool may hold, every file together`); a file
  byte-identical to a file of any version of any tool in reach, a version pruned to make room
  included (`<file> is <tool> v<n> byte for byte`, with the take command);
  a description at or above `tools_duplicate_floor` (0.85 unless set) to a current tool in reach,
  naming up to three, unless `--distinct` says how the new one differs, which its history keeps; a
  file or a flag holding a credential (below).
- **update** writes the next version and makes it current; a field or the files left out carry
  over. Refused when nothing changes, and when the old usage names a file or a long option the new
  files no longer hold, unless `--usage` rewrites it. An update of another project's tool forks it:
  this project's v1 is the origin's current version, its history saying `forked from <project>
  <tool> v<n>`, and the update lands as v2. The origin is never written. An update `--from` a copy
  the session took moves the session's holding to the version written.
  Refused, like `add`, when a file or a flag of the version it writes holds a credential, and
  past the 1 MB cap.
- **add** and **update** make room before they write and end their reply with the library's space,
  `space left: tools <left> of <limit>` (below).
- **merge** folds near-duplicate tools of the own project into the survivor: its next version from
  `--from`, the members' take lines appended to its own, each member's folder replaced by a
  tombstone naming the survivor. A merged name is refused by `add` and never found.
- **reindex** rebuilds the own project's `index.json` from its tool folders.

Agents write a tool's summary, usage, inputs, outputs and `--why` terse (no articles, filler or
hedging; code, paths and flags exact) unless the option `writing_style` is `plain`: only agents read
them. The `add` and `update` descriptions say so.

## Size

A tool holds at most 1 MB (1 MiB), every file of the version together; the cap is fixed. Every
stored version of every library on the machine together holds at most `tools_total_mb` (set in
`config.yaml` only), 500 unless set, never below the 1 MB cap. A version is prunable when it is not its tool's current version and
no fork names it as its origin. Past the limit, the oldest prunable versions are removed first,
by the time their history recorded, inside the `add` or `update` that needs the room and in the
SessionStart pass agentic-memory runs over every store. A write is refused only when removing every
prunable version would not make room: `the tool library would hold <size> on this machine, past the
<n> MB limit (tools_total_mb), and no version is left to prune: merge tools, or raise
tools_total_mb`. A pruned version's file hashes stay with its tool, so a byte-identical `add` is
still refused, as `` <file> is <tool> v<n> byte for byte: `agentic-tools take <tool>` copies its
current version, this one being pruned for space ``; a copy a session took of it stays in the
checkout's `.tmp/tools/`.

A prunable version also goes by age, whatever space is left: once its tool replaced it longer ago
than `tools_version_lifetime` (set in `config.yaml` only, a span), 90 days unless set, never below 14
days. Age counts from the replacement, the recorded write of the next version, so a version current
for a year keeps its way back for the full window after an update. A tool's current version never
goes by age, so every tool stays findable however long nobody takes it.

Space left is the limit less the bytes of the versions that cannot be pruned. Every `add` and
`update` reply ends with it, `space left: tools <left> of <limit>`; the agentic-memory brief
names it only while under a tenth of the limit is left. `agentic-tools --help` lists every verb
and states the cap.

## Credentials

`add`, `update` and `merge` scan every file of the version they would write, a fork's first
version (its files and description) included, and its `--summary`, `--usage`, `--inputs`,
`--outputs`, `--distinct` and `--why`, with the scanner shared by every piece
([`secrets`](../secrets/usage.md)). A file is scanned by its name, so rules for key files such as
`*.p12` fire, and again as plain text, so no gitleaks path allowlist (lock files, vendored folders)
lets its content through. On a finding the write is refused before anything is embedded or written:

    refused: agentic-tools: <file or flag> looks like a credential (<rule ids>, line <n>); the library never holds one

`<rule ids>` are every gitleaks rule found on the first line holding one, and `<n>` that line of the
file or flag. The matched text is never printed and nothing is redacted: read the token from the
environment or a file at run time and register the script again. The MCP tools refuse alike. Every
version is scanned again as it is written, whichever verb writes it, and an index rebuilt from a
store never embeds a description holding a credential.

## Which library a checkout writes

A project's library is the `tools` folder of its store in the agent-neutral home
([plugin usage](../plugin/usage.md#the-home)), the store agentic-memory uses too, so every worktree
and every agent of the project writes one library. A checkout matching no store gets one of its
own, silently, at its first write; a read creates none, and neither does a hook, which keeps what a
session holds in the cache.
Where the checkout's store is the user's call (a second clone of a known repository, say), every
write is refused with the question agentic-memory's SessionStart prints, the library untouched:

    refused: this checkout is bound to no store yet: no store is bound to the checkout <checkout> yet: <reason>. Ask the user which store it belongs to, then run `agentic-memory bind <store>` or `agentic-memory bind --new` from this checkout.

`agentic-memory bind` answers it for both pieces ([memory usage](../memory/usage.md#which-store-a-checkout-uses)).
An agent's `bind <store>` reaches only a store the question lists; any other is refused with
`agentic-memory: <store> is not offered for this checkout; run the bind in a terminal`, and the
user runs it at a shell outside Claude Code.
Until then the checkout still finds and takes tools, into its own `.tmp/` only, recording nothing,
and its hooks stay silent. `agentic-memory merge <from> <into>` joins two stores' libraries too,
refusing a tool name both hold; run by an agent, only the checkout's own store with one the question
lists (`agentic-memory: <store> is not offered for this checkout; run the merge in a terminal`).

A library kept under `~/.claude/agentic-tools/<key>/` by releases before the home moves into its
store's `tools` folder at the first call of a release with the home
([plugin usage](../plugin/usage.md#moving-from-claude)); `~/.claude/agentic-tools` then becomes a
file naming the home. A session started before the update runs the older release, which finds no
library and refuses every write (`refused: creating the store: ... not a directory`), until it
restarts.

## Each tool version once per context

find and list print a tool version in full once per context. A later find or list hitting the same
version prints a pointer in its place, keeping its rank, without its usage lines:

    word-count v1 (seen)

Over MCP and `--json` such a tool is `{"name", "label", "version", "seen": true}`, `label` only for
another project's tool and `score` beside them on a find hit. A newer version
prints in full. The offer skips an own tool find or list printed, and names each tool once. A
compaction, `/clear` and a resume start afresh; so does the next session of the same `claude`
process. `--again` (MCP `"again": true`) on `find` and `list` prints every hit in full. A freshly
spawned subagent shares its parent's `claude` process and never saw what it printed, so it passes
`again`; a forked one inherits the context and needs none. Where no owning `claude` process is
found, everything prints in full.

## Names and reach

A bare name reaches the own project's tool first, else the one tool of that name another project
holds. Any other is `<name>@<label>`: the label is the last segment of the project's key
(`format-d-fdbox`), with more leading segments only where two projects share it
(`a/shared`, `b/shared`). A bare name two other projects hold, and the own project not, is refused,
naming both.

## What a session hears

Each line names tools, paths and commands only, never another agent's summary or usage.

| When | Line |
|---|---|
| a prompt near own tools | `` tool library: near this prompt: section-word-count, moved-sentences. `agentic-tools take <name>` copies one `` |
| a tool call, a held tool newer | `` tool library: newer versions of `word-count`. `agentic-tools take word-count` copies them `` |
| a tool call, a held fork's origin newer | `` tool library: `word-count` in github.com/fza/other has v6; yours forked at v5 `` |
| the next prompt after a turn left scripts the library lacks | `` tool library: your scratch holds scripts the library lacks: check.py, split.sh. `agentic-tools add <name> --from <path> --summary '...' --usage '...'` registers one `` |
| the next prompt after a turn changed a taken copy | `` tool library: you changed your copy of `word-count` (<copy>): `agentic-tools update word-count --from <copy> --why '...'` `` |

The offer names up to three own tools, each once per context and none find or list printed, the caller's role first; nothing when
none is near. A prompt holding a credential is never embedded and gets no offer; agentic-memory
says so once ([memory](../memory/usage.md#prompt-recall-and-credentials)). The two turn-end lines also open the session's next agentic-tools call, once.

## Environment

| Variable | Effect |
|---|---|
| `AGENTIC_TOOLS_ROLE` | the caller's role, recorded in history and takes and printed by no verb; the offer names same-role tools first. Unset, the agentic-memory seat the session holds stands in |
| `AGENTIC_TOOLS_SCRATCH` | a folder `add` and `update` may register from, and where the turn-end check looks for scripts in place of the seat's `.tmp/claude/<seat>/` and `.tmp/claude/<session>/` |
| `AGENTIC_TOOLS_SCOPE` | `project` keeps every read (find, list, take, the duplicate checks) to the own project's store, as a sandboxed caller's launcher sets it |

The plugin option `tools_query_floor` sets the cosine a search or a prompt must reach to count a
tool as near, 0.45 unless set; `tools_duplicate_floor` sets the near-duplicate cosine, 0.85 unless
set. Both depend on the embedder: with `embeddinggemma-300m`, paraphrases of a tool's purpose score
about 0.50 to 0.63 against it and unrelated needs about 0.35 to 0.38. The embedding options are
agentic-memory's own.

## Off on the machine

The plugin option `tools_enabled` switches agentic-tools on or off for the machine, on unless set. Off, every tool-library hook says nothing, every subcommand exits 1 with `agentic-tools: off on this machine (plugin option tools_enabled)`, and the `tools` MCP server lists no tools, its instructions naming the option. With `memory_enabled` off, the library stamps no seat name on what it files.
