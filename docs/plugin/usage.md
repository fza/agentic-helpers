# The plugin: usage

## How a project opts in

**No hook but shell-guard does anything until the project carries what it operates on.** Nothing is created,
nothing is asked, and a project carrying none of it never hears from any hook.

| Hook | Runs when the project carries | Otherwise |
|---|---|---|
| Memory | a seat claimed with `agentic-memory claim <seat>`; where a project has memory but this session holds no seat, one line names the seats | exits 0, silently |
| Grounding gate | a `.sdd/` decision graph, at the project root or above it, without `enabled: false` in its `grounding.yaml` | exits 0, silently |
| Scratch sweep | a `.tmp/` directory | exits 0, silently |
| Tool library | a git work tree, on a machine where the first `agentic-tools add` created a library in some store of the home | exits 0, silently |
| Shell-guard | nothing: it judges every Bash call of every project | its `shell_guard_*` options turn each guard off |

So opting a project in is one command, `mkdir .tmp`, `agentic-memory claim <seat>` or a first
`agentic-tools add`, and opting out is removing the directory or releasing the seat. A project using none needs no configuration, no
allowlist entry and no marker file.

## The home

Every component keeps its per-project data in one agent-neutral home, one store per project, which
every agent and every worktree of the project shares:

| Path | Holds |
|---|---|
| `$XDG_DATA_HOME/agentic-helpers` (`~/.local/share/agentic-helpers` where unset; `AGENTIC_HELPERS_HOME` overrides it) | the data root, `<data>` |
| `$XDG_CACHE_HOME/agentic-helpers` (`~/.cache/agentic-helpers` where unset) | the cache root, `<cache>`: only what is rebuilt at will |
| `<data>/format.json` | the home's layout version; a release meeting a newer one refuses every call and its hooks stay silent |
| `<data>/stores/<id>/<component>/`, `<cache>/stores/<id>/<component>/` | a store's data and cache per component (`memory`, `tools`) |
| `<data>/stores/<id>/identity.json` | the store's admitted checkouts and the identifiers a checkout is matched by |
| `<data>/index.json` | a cache of every identity, rebuilt whenever it is missing or disagrees with one |
| `<data>/unclaimed/<id>/` | data whose checkout cannot be found, kept under the key an earlier release used |
| `<data>/sessions/claude/<session>/` | what one session leaves, as its context reading |
| `<git common dir>/agentic-store` | the id of the checkout's store |

Each component keeps what changes often in binary files: what a session or seat keeps between hook
calls in a slot file (`sessions.slots`), its append-only records in journals (`.jnl`, memory's log
tail and agentic-tools' histories), and a size ledger (`.ledger`) the space figures read instead of
walking a store. `agentic-helpers dump <file>` prints any of them, and memory's log segments, as JSON
lines:

    agentic-helpers dump <data>/stores/<id>/memory/log/tail.jnl

A slot prints `{"key", "generation", "written", "value"}` (`value_base64` for a value that is no
JSON), a log event `{"record": "event", "event", "entry", "time", "seat", "target", "payload"}`, a
ledger file `{"record": "ledger-file", "name", "length", "crc32c", "modified", "counted", "held",
"bytes"}`, and any other record `{"record", "schema", "body"}`. A journal cut short prints its
records up to the cut, then fails naming it; a file of no binary kind is refused. The JSON files
(`format.json`, `index.json`, a claim) are read as they are.

A store id is a UUID v7 and a suffix fixed at creation: the last path part of the sdd `repo_id`, else
of the origin, else the checkout directory's name. A checkout is matched, in this order, by its
pointer, the volume UUID and inode of its git common dir, its path, the graph repository a split
layout names, its `repo_id`, then its origin (with any user, password, query or fragment dropped);
only the first four teach a store anything, and one identifier belongs to one store. A checkout
matching nothing gets a store of its own, silently, at its first write; one whose legacy key (its
`repo_id`, else `local/` and a hash of its git common dir) names an unclaimed store gets that store,
silently. Where two checkouts compete for a store, the agent asks the user, and `agentic-memory bind`
or `agentic-memory merge` answers ([memory usage](../memory/usage.md#which-store-a-checkout-uses)).

A code repository whose graph lives in another repository, named by `project_dir` in
`.sdd/config.yaml` (the sdd fork) or by a stock sdd `graph_dir` leaving the checkout, shares the
graph repository's store, the pointer written into both git common dirs, once that store admits
it. That config is committed, so a clone brings it along: where the graph store does not list the
checkout yet, the agent asks the user once (``its `.sdd/` names the graph repository <folder>,
whose store does not admit it``), and `agentic-memory bind <graph store>` or `bind --new` answers. Until
then the checkout reads nothing of that store.

An unclaimed store nobody promotes is deleted once `orphan_store_grace` (`config.yaml` only, `30d`
unless set) has passed since it became unclaimed, unless one of the checkouts it last knew exists
again. A store with a living checkout is never deleted, however long unused.

### Moving from `~/.claude`

Releases before the home kept memory under `~/.claude/agentic-memory/<key>/`, tool libraries under
`~/.claude/agentic-tools/<key>/` and writers lists under `~/.claude/agentic-helpers/<key>/`, `<key>`
being the project's `repo_id` or `local/` and a hash of its git common dir. The first call of a
release with the home, any hook, command or MCP server, finds that layout with no
`<data>/format.json` and moves all of it in one run, asking and printing nothing:

| Old key | Becomes |
|---|---|
| its writers list names a checkout that exists | a store admitting every checkout listed, the pointer written into each that exists |
| its checkout lies on a volume that is not mounted | a store admitting that checkout by path; its first visit writes the pointer |
| any other | an unclaimed store, stamped with the time of the move |

A key's memory store (seats, shelves, seat files and `backup/` included) and its tool library join
in one store; the session tree moves to `<data>/sessions/claude/`, each context reading
`<session>.ctx` becoming `<session>/context.json`, and the attachments to `<data>/attachments/`. Every writers list, `seats.json`, `shelves.json` and `format.json` is copied
to `<data>/backup/legacy-<UTC stamp>/` first. A moved memory store keeps its format and is migrated
by its first call, as ever.

Every other call waits for the run. Should it fail, every tree goes back, no pointer stays, and each
session hears once:

```
agentic-helpers: migrating to <data> failed: <cause>; nothing moved, and the next call tries again
```

A run killed half way, as a hook reaching its timeout is, leaves its journal,
`<data>/.migration.journal`: the next call undoes what it recorded and then migrates afresh.

A session started before the update keeps running the older release and is fenced off from the
old trees: each old memory store is left holding only a `format.json` of format 999, and
`~/.claude/agentic-tools` and `~/.claude/agentic-helpers` become files naming the home. Its hooks
then say nothing, and every memory and tools write is refused with the older release's own line;
under git that is `writers.json cannot be read: <path> is a link or no folder; no call lands until
<path> is mended` for memory and tools trust, and `creating the store: ... not a directory` for a
new tool. Nothing it filed earlier is lost: a restart picks it up from the home.

Once the process table shows no `agentic-*` binary of another release, the sweep removes those
stubs and files, the context readings older sessions kept writing below
`~/.claude/agentic-memory/sessions/`, and the old caches `~/.cache/agentic-memory` and
`~/.cache/agentic-tools`, leaving anything else. A `memory` or `tools` MCP server runs it after two quiet minutes, and the
SessionEnd hook `agentic-helpers end` starts `agentic-helpers sweep` in a process of its own; both
are silent, and `agentic-helpers sweep` can be run by hand.

## sdd

The grounding gate and `agentic-capture` work with stock sdd
([github.com/networkteam/sdd](https://github.com/networkteam/sdd)) and use what the fork
[github.com/fza/sdd](https://github.com/fza/sdd) adds wherever the installed `sdd` carries it. What a
binary carries is read from its own `sdd --help`, `sdd new --help`, `sdd init --help` and `sdd info --help`, never from
its version, and kept in `<cache>/sdd-caps.json` per binary path, modification time and size.

| Fork surface | Without it |
|---|---|
| `sdd new --preflight-verified` | `agentic-capture` runs one `sdd new`, which checks and writes |
| `sdd new --title`, `--actor-kind` | a draft carrying `title` or `actor_kind` is refused, naming the fork |
| `sdd --local-config` | `--preflight-route` is refused |
| `sdd migrate` | a graph on schema 2 is refused; a schema refusal names no `sdd migrate` |
| `sdd init --project-dir` | the gate's engine refusal skips a project naming `project_dir` |
| `sdd info --format json` | `agentic-capture` reads a titled draft's graph schema from `.sdd/meta.json` instead of asking sdd |
| `sdd export` (no stock release) | memory flags a graph link by whether its entry file exists |

## Commands

Claude Code's Bash tool finds `agentic-capture`, `agentic-helpers`, `agentic-memory` and `agentic-tools` through the plugin's `bin/`,
which hands each call to the launcher, so `agentic-capture --help` is answered by the real binary.
With no binary installed and none fetchable, a command from `bin/` or the MCP server exits 1 and
says why on stderr, where a hook stays silent. A script outside Claude Code has no plugin on its
PATH and builds the binaries itself: `go install -C source/hooks ./cmd/...` from a checkout.

## Options

Every option is set once per machine; no project overrides one. Each is read in layers, lowest
first:

1. the built-in default;
2. `config.yaml` under `$XDG_CONFIG_HOME/agentic-helpers/` (`~/.config/agentic-helpers/` unless
   set), one flat `key: value` map every agent on the machine shares;
3. the plugin's configuration dialog (`/plugin`, or `pluginConfigs` in the user settings), which a
   hook receives as `CLAUDE_PLUGIN_OPTION_<KEY>`;
4. Claude Code's managed settings, last, so a machine lock always wins.

An empty value counts as unset at every layer. The dialog declares no default: Claude Code would hand
a declared default to every hook as an answer and hide `config.yaml`, so each description names its
default instead.

**Keys deciding when stored data every agent shares is deleted live in `config.yaml` only**:
`retired_body_days`, `temporary_seat_lifetime`, `tools_total_mb` and `orphan_store_grace`. The dialog lacks them and a value
in the user settings or the hook environment is ignored, since two agents set apart would leave the
lowest value deciding for both. Managed settings still set them. A key deleting only what one session
made for itself, `capture_remove_draft`, stays in the dialog. At session start `agentic-helpers check`
copies a value the user settings still hold for a `config.yaml`-only key into `config.yaml`, where the
file names none; it writes no settings file.

**`config.yaml` only tightens a guard.** Any agent can write the file in one Bash call, where Claude
Code's settings sit behind its permission prompts, so the file never lifts a guard. Against the
built-in default, the layer below it:

| Keys | `config.yaml` takes |
|---|---|
| `shell_guard_enabled`, `grounding_gate_enabled`, `shell_guard_lone_removal`, `shell_guard_expansion`, `shell_guard_nested_code`, `shell_guard_mktemp`, `shell_guard_alternatives` | only `true` |
| `shell_guard_outside_project`, `shell_guard_find_delete`, `shell_guard_secrets` | only as strict or stricter: `off` < `ask` < `deny` |
| `shell_guard_secret_output` | only as strict or stricter: `off` < `files` < `all` |
| `shell_guard_ask_timeout` | only as short or shorter; `0` waits without limit, the loosest |
| `embedding_endpoint`, `embedding_api_key_env`, `shell_guard_secret_output_skip` | nothing: never read from it |

The embedder's endpoint and key variable together could send any environment variable to a server
the writer picks; the skip list can only loosen. A looser value, or any value for a key the file never
sets, is ignored, the layer below applies, and `agentic-helpers check` names it at session start as
`agentic-helpers: config.yaml may only tighten <key>; <value> ignored` or
`agentic-helpers: config.yaml never sets <key>; <value> ignored`. Loosening a guard and pointing the
embedder elsewhere stay possible in Claude Code's settings, and managed settings still lock every key.
The keys `config.yaml` never sets are never taken from `CLAUDE_PLUGIN_OPTION_<KEY>` either: an agent
can prefix its own command with that variable, and none of them is a `sensitive` option, so Claude
Code keeps them in the settings files read anyway. `CLAUDE_CONFIG_DIR` is still honoured, as Claude
Code's own contract; redirecting it takes a settings file written there, a visible step, which is
the accepted cost.

    agentic-helpers config                       # every key: value and the layer it comes from
    agentic-helpers config get <key>             # one key's line
    agentic-helpers config set <key> <value>     # write it into config.yaml

`config set` checks the value as the reader does, replaces the key's line or appends one, keeps every
other line and comment, and writes through a temporary file renamed into place. An unknown key is
refused naming the nearest. A guard value looser than its default is refused, writing nothing:
`agentic-helpers: refused: config.yaml may only tighten <key>; loosen it in Claude Code's settings`,
and so is every key the file never sets:
`agentic-helpers: refused: config.yaml never sets <key>; set it in Claude Code's settings`. The
listing marks those keys `config.yaml only tightens` and `never config.yaml`. A layer above
`config.yaml` that sets the key too is named:
`<layer> also sets <key> and wins over config.yaml: <value> applies`. An invalid value in
`config.yaml` (a word for a number, an unknown key, a file that is no YAML map) is skipped, the layer
below applies, and `agentic-helpers check` names it at session start as
`agentic-helpers: <path>: <key>: <why>; the layer below applies`; `config` lists it as `problem:`.
An invalid value in a settings file still sets every option to its default, as before.

[`../memory/usage.md`](../memory/usage.md), [`../tools/usage.md`](../tools/usage.md),
[`../capture/usage.md`](../capture/usage.md) and [`../shell-guard/usage.md`](../shell-guard/usage.md)
list each option.

## Switching a piece off

Each piece has a boolean plugin option switching it on or off for the machine, on unless set:
`memory_enabled`, `tools_enabled`, `scratch_enabled`, `grounding_gate_enabled`, `capture_enabled` and
`shell_guard_enabled`. Off, its hooks say nothing, its commands exit 1 with
`<binary>: off on this machine (plugin option <key>)`, its MCP server lists no tools and its
instructions name the option, and its skill stays listed. Each binary checks its own switch. A
feature resting on an off piece goes off with it, and the rest of its piece runs; the table is in
[`README.md`](../../README.md#dependencies). At each session start `agentic-helpers check` prints one
line per such feature, `agentic-helpers: <piece> is off, so <feature> is off; turn <key> on to restore it`,
and, where the machine needs a command substitution shell-guard enforces, one naming them, such as
`agentic-helpers: this machine: timeout -> gtimeout; sed and date are BSD` (see
[shell-guard's command alternatives](../shell-guard/usage.md#command-alternatives)); run as a command
it lists every piece on or off, with its features off under it.

Every binary reads `config.yaml`, then `settings.json` under `CLAUDE_CONFIG_DIR` (else `~/.claude`),
then, in a hook, the options Claude Code hands it as `CLAUDE_PLUGIN_OPTION_<KEY>`, then the managed
settings and their `managed-settings.d/` drop-ins in name order, as Claude Code merges them. The
managed settings come last, so no environment overrides what a machine locks.

## The skills

A hook fires and an agent that has never met it has to work out what just happened. Each hook has a
skill carrying what it cannot say in a one-line refusal.

| Skill | Carries |
|---|---|
| `agentic-helpers:agent-memory` | what a seat is, how to claim one, how to file, find, retire and supersede, what belongs in memory against what belongs in the graph (and that a project without a graph files its decisions and defects in memory), and what comes back after a compaction |
| `agentic-helpers:agent-grounding` | the sdd reads that open the gate, how deep to read an entry and why, which reference kind is the sharp one, and what each refusal means |
| `agentic-helpers:agent-capture` | the draft's shape, what runs before an sdd entry is written, and what each refusal and exit code means |
| `agentic-helpers:agent-tools` | looking for a tool before writing one, taking and registering tools, what each duplicate refusal means, and what each `tool library:` line asks |
| `agentic-helpers:agent-scratch` | where throwaway goes, what the sweep removes, and how to keep something from it |
| `agentic-helpers:agent-shell-guard` | what passes a Bash call, what each shell-guard refusal and question means, and what to do about it |

Each skill is the whole of its rules. A global instruction file names the skill and says to load it;
it never restates what the skill holds, because two copies drift and a reader obeys whichever they
opened. Each says in its own description when it applies, so an agent in an unrelated project has no
reason to open any of them. A plugin update brings every skill's new text with it.

## Working on a checkout

Add the checkout as the marketplace, so the plugin loads from it in place and an edit to a skill or
to `plugin/` reaches the next session or `/reload-plugins`:

```bash
claude plugin marketplace add /path/to/agentic-helpers
claude plugin install agentic-helpers@agentic-helpers
```

The binaries the checkout builds reach the hooks through `AGENTIC_HELPERS_BIN_DIR`, set to the
directory `go install` writes to in the `env` block of the checkout's own
`.claude/settings.local.json`:

```json
{ "env": { "AGENTIC_HELPERS_BIN_DIR": "/Users/you/go/bin" } }
```

A change to a binary then reaches the hooks on the next event after
`go install -C source/hooks ./cmd/...`. Where the variable is set, no release is fetched or run.
Set in the user settings instead, it would hold every project on the machine to the last
`go install` build: a release would reach no session until installed, and an install from a tree
holding unfinished work would run it everywhere.
