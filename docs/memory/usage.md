# Memory: usage

A session runs out of context and the next one starts knowing nothing. With agentic-memory a session
files what it learns as it works, one entry per finding, and the tool stamps, indexes and replays
it. Nothing is ever rewritten: an entry is retired or superseded, and the tool chooses what returns
to a fresh context.

## Where it works

agentic-memory works only inside a git work tree, a fresh `git init` without commits included.
Outside one, every subcommand and every tool of the `memory` MCP server refuses with
`agentic-memory: <dir> is not inside a git work tree; memory works only in one`, every hook stays
silent, and nothing is read or created. `help` answers anywhere. The plugin option
`memory_require_git: false` lifts the limit for the whole machine: a directory outside git then
keeps a store keyed by its own path.

## Seats

A **seat** is a named job, held by one session at a time; claiming one is what turns memory on for
a session, by a prompt or an instruction line. Every entry carries the seat that filed it, and a
search covers the own seat unless it names others, so one seat sees what another holds and nothing
is done twice. A call finds its seat through the `claude` process that owns it, so a command run
from the Bash tool, a tool call to the plugin's MCP server and a hook all agree, also after `/clear`.

```bash
agentic-memory claim main
agentic-memory new --title 'Gate ignores MCP reads' --topic gate --retention normal <<'BODY'
reads are recorded only for Bash, so a search through MCP never counts
BODY
agentic-memory search --term mcp --query 'gate blind to tool calls'
agentic-memory supersede m-7q2k4f --by m-9xq0b1
```

A seat name is a word in any script: a letter, then up to 23 letters, combining marks, digits or
dashes, counted in characters (`main`, `haupt`, `главная`, `メイン`, `मुख्य`). Every name agentic-memory
reads (`claim`, `release`, `holder`, `--seat`, the MCP keys carrying a seat, `exempt_seats` in
`.sdd/grounding.yaml`) is normalised to Unicode NFC and lower-cased first, so `Café` typed
precomposed or decomposed names one seat with one file.

An agent asked for a seat in words that are not its exact name ("Haupt-Session") lists the seats
with `sweep`, maps the words onto the seat meaning the same, and asks when none or several fit.

A seat whose holder still runs is refused to every other session, always: nothing tells a stuck
holder from a busy one, and a busy one losing its seat would find every write refused without
noticing. The seat frees when its holder runs `release` or its session ends; a seat whose holder has
gone is free to claim, and `sweep` frees every such seat of the project.

## Entries

An entry has a 3-10 word title, a free body, topics the agent chooses (one namespace per project,
merged with `merge`), a retention level setting how long it stays in view (`standing`, `long`,
`normal`, `short`, `scratch`) and a source (`--from user|agent|tool`). The tool stamps time, seat,
session, branch and commit, and anchors the entry to every file it names by content hash: a name of
letters, digits and marks of any script holding a dot or a slash, where a file exists under it,
with a trailing `:<line>` dropped.

Words are counted for the body against `entry_max_words` and for the title against 3-10, and the
similar-entry hint compares entries by their words. In scripts written without spaces (Han,
Hiragana, Katakana, Thai, Lao, Khmer, Myanmar) every character counts as a word; elsewhere a word is
a run between spaces.

An entry is written in the project's language, never in the language the conversation happens to
run in: the graph's `language` from `.sdd/config.yaml`, else the plugin option `memory_language`,
else `en`. The brief's first line names it, as in
`agentic-memory: seat main, 14 live, 6 retired, entries in de`. A domain term (a word naming a thing
in the project's code, documents or business, such as `Lieferschein`) keeps the project's spelling
and is never translated, so `--term` still finds the word the code uses; a gloss in the entry's
language may follow it in parentheses.

An entry id is `m-` and `entry_id_length` characters of `a-z0-9`, 6 unless set and never fewer, at
most 16; an id filed at another length keeps working.

## Ending an entry

`retire <id>... [--because '<why>']` (MCP `retire`, key `because`) ends entries that are done or no
longer matter: they leave the brief and stay in search, flagged `retired <stamp>: <why>`, the reason
standing after the stamp where one was given. `supersede <id> --by <id>` replaces an entry and
`supersede <id> --because '<why>'` marks it wrong; `revive <id>` undoes the latest of either within
14 days. Only the filing seat retires its own entries; any seat may supersede or confirm.

## Search

Search is sdd's: `--term` is a case-insensitive regex, every term required, matched against the
title, the body and the reason an ended entry gives; `--query` searches by
meaning; both fuse by reciprocal rank. Every result carries `drifted`, `gone` or `other branch` as
computed against the tree right then, and `confirm` re-anchors a verified entry. A `show` and a
search's top three hits add heat, which orders the brief; `--no-heat` (MCP `"no_heat": true`) reads
without it, for a sweep or a script no person is behind.

`--status live|retired|superseded` (MCP `status`, an array), repeatable, keeps only entries in the
named statuses; `--status superseded` includes superseded entries as `--with-superseded` does. Given
a status, search runs without `--term` or `--query` and lists: the most recently ended first for
retired and superseded entries, the most recently filed first for live ones. A listing matches
nothing, so it warms nothing.

A search returns `--limit` results, 10 unless set, and `--skip <n>` (MCP `skip`) drops the first
`n` before the limit cuts the page. A page with results past it ends with the next page's flag:
`14 more: --skip 10` where the remainder is exact (a `--term` search, a listing), `more: --skip 10`
where a `--query` takes part, since every candidate carries a score. A hybrid search's arms each keep
`skip + limit` candidates before fusion, so a later page is the fused ranking's next slice. Only the
first page warms its top hits.

## What comes back

A brief at every claim and compaction (standing entries, the seat's live entries by heat, what other
seats changed and filed, the topics), the turns since the seat's newest entry, and up to three
entries a prompt touches that stand out from the rest. Near compaction a nudge asks the seat to file
what the summary would lose; a session's last turns survive its end for the next claim. There is no
handoff to write.

## MCP tools

Every subcommand is also a tool of the plugin's `memory` MCP server,
`mcp__plugin_agentic-helpers_memory__<subcommand>`, and `agentic-memory help` lists them. A tool
takes exactly the keys its schema lists, spelled exactly (`all_seats`, not `ALL_SEATS`), and its
schema says so with `additionalProperties: false`; any other key refuses the call with
`agentic-memory: <tool> takes no key "<key>"; it takes <keys>`.

## Other tools

Other tools read seats through `agentic-memory holder <seat> --json` and
`agentic-memory holder --session <id> --json`: exit 0 for a held seat, 1 for a free one.

## Options

Set once per machine in the plugin's configuration dialog; no project overrides them.

| Option | Sets |
|---|---|
| `embedding_provider`, `embedding_model`, `embedding_endpoint`, `embedding_dimensions`, `embedding_api_key_env` | the embedder search by meaning and prompt recall run on |
| `entry_max_words` | the words a body should stay within, 500 unless set; longer bodies land with a note |
| `retired_body_days` | how long a retired entry keeps its body, 90 unless set, never under 14 |
| `entry_id_length` | the characters after `m-` in a new entry id |
| `memory_language` | the locale entries are written in where the project's `.sdd/config.yaml` names none; `en` unless set |
| `memory_require_git` | `true` unless set: memory works only inside a git work tree; `false` lets a directory outside git keep a store of its own |

Without an embedder, a search by meaning exits 1 naming the cause, a keyword search still answers,
and filing never waits: the next call that reaches the endpoint embeds what is pending.
