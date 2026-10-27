# agentic-helpers

A coding agent forgets. Its context fills, the session is summarised, and the next one starts from
nothing. Asked about a project's decisions, it answers from whatever it happened to read last. Its
probes and half-finished drafts pile up where nobody reviews them.

agentic-helpers gives Claude Code the habits that counter this, as hooks that run on every session
and commands the agent calls itself:

| Piece | Binary | What it keeps the agent doing |
|---|---|---|
| memory | `agentic-memory` | filing what it learns as it works, and getting back what a fresh context needs after a compaction or a new session |
| tool library | `agentic-tools` | taking a helper tool another agent already wrote instead of writing it again, and registering the ones it writes |
| scratch sweep | `agentic-scratch` | keeping throwaway work in one place per session, and clearing it when the session ends |
| grounding gate | `agentic-grounding-gate` | reading the decision graph for real before asking a question, editing a draft or capturing |
| capture | `agentic-capture` | writing a new graph entry through one checked path, never by hand |
| shell-guard | `agentic-shell-guard` | keeping every removal inside the project and `/tmp`, and scratch files out of `mktemp`, on every Bash call |

**The grounding gate and capture exist for [sdd](https://github.com/networkteam/sdd)**, the
Signal-Dialogue-Decision graph a project keeps under `.sdd/`.

**sdd is a project's long-term memory; agentic-memory is its short-term memory.** The graph holds
what should still hold in a month and for everyone working on the project: decisions, the signals
behind them, and why. It lives in the repository, is reviewed like code, and never changes an entry
once written. agentic-memory holds what one job needs to pick up where it left off: where work
stands, a judgment call made without confirmation, a trap that already cost time. It lives on the
machine outside the repository, fades by each entry's kind, and survives a compaction or a new
session. A memory entry points at a graph entry by id; it never copies a decision.

Every piece installs once for the whole machine and stays silent where it has nothing to work on.
Shell-guard's work is every Bash call, so it judges every project; its options turn it off.
Everything ships as one Claude Code plugin; the hooks and commands are Go binaries built from
`source/hooks`, published with each release and fetched by the plugin.

**Every piece is built and tested with Claude Code only.** The hooks read Claude Code's payloads,
settings and transcripts, and answer in its hook protocol; no other agent harness is tested, and
none is supported.

## Installing

Install sdd first:

```bash
brew install networkteam/tap/sdd
```

Without Homebrew, sdd's install script does the same:

```bash
curl -sL https://github.com/networkteam/sdd/releases/latest/download/install.sh | sh
```

**Optional: the fork [github.com/fza/sdd](https://github.com/fza/sdd).** agentic-helpers works with
stock sdd and uses what the fork adds wherever the installed `sdd` carries it, read from its own
`--help`: entry titles and `actor_kind` in a draft's header, a pre-flight checked once and recorded
as verified, fallback routes for the pre-flight, `sdd migrate`, and a code repository naming its
graph repository through `project_dir`.

```bash
brew install fza/tap/sdd
```

A graph `sdd migrate` has moved to the fork's schema takes only the fork for writes: stock sdd
refuses it, and so does `agentic-capture` through stock sdd. Every member writing to such a graph
installs the fork.

Then the plugin:

```bash
claude plugin marketplace add fza/agentic-helpers
claude plugin install agentic-helpers@agentic-helpers
```

Inside a session, `/plugin install agentic-helpers --marketplace fza/agentic-helpers` does both in
one step (Claude Code 2.1.275 or later), and `fza/agentic-helpers#v<version>` pins the marketplace to
one release. Installed at user scope, the plugin is enabled in every project on the machine.
`claude plugin update agentic-helpers@agentic-helpers` moves to the latest release; it takes effect
in the next session or after `/reload-plugins`. Auto-update is off by default for a marketplace like
this one, and can be turned on under `/plugin`, Marketplaces.

## Options

Set once per machine in the plugin's configuration dialog (`/plugin`), or as `pluginConfigs` in the
user or managed settings; no project overrides them.

| Option | Default | Sets |
|---|---|---|
| `memory_enabled` | `true` | agentic-memory on or off; off, its commands refuse, its hooks say nothing, its MCP server lists no tools |
| `tools_enabled` | `true` | agentic-tools on or off, as for memory |
| `scratch_enabled` | `true` | the scratch sweep on or off |
| `grounding_gate_enabled` | `true` | the grounding gate on or off |
| `capture_enabled` | `true` | `agentic-capture` on or off |
| `shell_guard_enabled` | `true` | shell-guard on or off |
| `embedding_provider`, `embedding_model`, `embedding_endpoint`, `embedding_dimensions`, `embedding_api_key_env` | none | the embedder memory's and the tool library's search by meaning runs on; the key itself stays in the environment variable named |
| `entry_max_words` | `500` | the words a memory entry's body should stay within |
| `retired_body_days` | `90` | how long a retired memory entry keeps its body, never under 14 |
| `entry_id_length` | `6` | the characters after `m-` in a new memory entry id, 6 to 16 |
| `brief_note_limit` | `10` | the notes a memory brief prints, hottest first; `agentic-memory brief --kind note` lists the rest |
| `attachment_max_mb` | `10` | the megabytes one file attached to a memory entry may hold |
| `attachment_budget_mb` | `500` | the megabytes a project's memory attachments may hold together, each file counted once |
| `memory_language` | `en` | the language memory entries are written in where `.sdd/config.yaml` names none |
| `memory_require_git` | `true` | memory only inside a git work tree |
| `memory_turns_off_auto` | `true` | a seat claim turns Claude Code's own auto memory off for the project, through its `.claude/settings.local.json`, from the next session |
| `temporary_seat_lifetime` | `1d` | how long a closed temporary shelf keeps its entries before they are purged |
| `tools_query_floor` | `0.45` | the cosine at or above which the tool library counts a tool as near a search or a prompt |
| `tools_duplicate_floor` | `0.85` | the cosine at or above which the tool library refuses a new tool as another's duplicate |
| `shell_guard_outside_project` | `ask` | `ask`, `deny` or `off` for a removal reaching outside the project and `/tmp` |
| `shell_guard_find_delete` | `ask` | `ask`, `deny` or `off` for `find ... -delete` |
| `shell_guard_lone_removal` | `true` | deny a removal that is not the whole Bash command |
| `shell_guard_expansion` | `true` | deny a removal target built from a variable or command substitution |
| `shell_guard_nested_code` | `true` | deny a removal or `mktemp` inside `eval`, a shell's `-c` or inline code for another interpreter |
| `shell_guard_mktemp` | `true` | deny `mktemp` anywhere in a Bash command |
| `shell_guard_secrets` | `ask` | `ask`, `deny` or `off` for a Bash command holding what looks like a credential |
| `shell_guard_secret_output` | `all` | `all`, `files` or `off`: withhold each line of a tool's output from outside the project that looks like a credential; `files` limits it to Read, Grep, NotebookRead, Edit, Write, NotebookEdit and Bash |
| `shell_guard_secret_output_skip` | none | comma-separated tool names or patterns (`mcp__sdd__*`) whose output is never withheld |
| `shell_guard_ask_timeout` | `120` | the seconds a shell-guard ask waits in `auto` and `bypassPermissions` mode before it denies, at most 3600; `0` waits without limit |

## Dependencies

No piece breaks when another is off; a feature resting on the other goes off with it, and the rest
of the piece runs. Each session start names every such feature off, as
`agentic-helpers: scratch is off, so shell-guard's mktemp ban is off; turn scratch_enabled on to restore it`,
and `agentic-helpers check` lists every piece on or off with its features off through a tie.

| Off | Feature that goes off | Why |
|---|---|---|
| `scratch_enabled` | shell-guard's `mktemp` ban, whatever `shell_guard_mktemp` says | its refusal sends scratch files to a `.tmp/` nothing sweeps |
| `grounding_gate_enabled` | `agentic-capture` | its grounding check is the gate's hook; without it a draft lands ungrounded |
| `memory_enabled` | the grounding gate's `exempt_seats` | a seat is an agentic-memory claim; a claim left from before the switch must not exempt anything |
| `memory_enabled` | the seat name agentic-tools stamps on what it files | it reads the name from agentic-memory's seat registry |

## Documentation

How to use each piece and how each works inside: [`docs/index.md`](docs/index.md).

## Licence

MIT. See [`LICENSE`](LICENSE).
