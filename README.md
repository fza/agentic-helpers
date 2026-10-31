# agentic-helpers

A coding agent forgets. When its context fills up, the session is summarised and the next one
starts from scratch. It answers questions about a project from whatever it read last, and it leaves
probes and half-finished drafts lying around.

agentic-helpers is a Claude Code plugin that builds better habits into every session. It adds hooks
that run on their own, commands the agent can call, and one skill per piece that explains the piece
to the agent. Memory and the tool library are also available as MCP servers, `memory` and `tools`.

| Piece | Command | What it does |
|---|---|---|
| memory | `agentic-memory` | Keeps notes the agent reads again after a compaction or in a new session |
| tool library | `agentic-tools` | Shares helper scripts between agents, so none is written twice |
| scratch sweep | `agentic-scratch` | Gives each seat or session one place for throwaway files, and empties it when the work ends |
| grounding gate | `agentic-grounding-gate` | Makes the agent read the decision graph before it asks, drafts or captures |
| capture | `agentic-capture` | Writes new graph entries through one checked path, free of credentials |
| shell-guard | `agentic-shell-guard` | Keeps deletions inside the project, and credentials out of commands and output |

## Memory and the decision graph

The grounding gate and capture exist for [sdd](https://github.com/networkteam/sdd), which keeps a
project's decision graph under `.sdd/`.

- **sdd is long-term memory.** It holds decisions, the signals behind them, and why, for everyone on
  the project. It lives in the repository, is reviewed like code, and never changes an entry once
  written.
- **agentic-memory is short-term memory.** It holds what one job needs to pick up where it left off:
  where work stands, a judgment call made without asking, a trap that already cost time. It lives on
  the machine, outside the repository, and fades at a rate set by each entry's kind. A memory entry
  points at a graph entry by id and never copies a decision.

## Good to know

- **One install covers the whole machine.** Each piece stays quiet in a project where it has nothing
  to do. Shell-guard is the exception: it checks every Bash call in every project, unless its
  options turn it off.
- **The hooks and commands are Go binaries** built from `source/hooks`. Each release publishes them,
  and the plugin downloads them.
- **Claude Code only.** The hooks read Claude Code's payloads, settings and transcripts. No other
  agent is tested or supported.

## Requirements

- macOS or Linux, on amd64 or arm64.
- Claude Code. The one-step `/plugin install` with `--marketplace` needs 2.1.275 or later.
- git. Memory works only inside a git work tree, unless `memory_require_git` is off.
- [sdd](https://github.com/networkteam/sdd), only for projects with a graph under `.sdd/`.

## Installing

Needs Claude Code v2.1.271 or later: the plugin's option pickers need it, and an older Claude Code
refuses to load the plugin.

```bash
claude plugin marketplace add fza/agentic-helpers
claude plugin install agentic-helpers@agentic-helpers
```

- **From inside a session**, one step does both:
  `/plugin install agentic-helpers --marketplace fza/agentic-helpers`.
- **To pin a release**, add the marketplace as `fza/agentic-helpers#v<version>`.
- **Installed at user scope**, the plugin runs in every project on the machine.
- **Turn auto-update on**, so every release arrives by itself. Claude Code leaves it off for this
  marketplace; add this to `~/.claude/settings.json` (or turn it on under `/plugin`, Marketplaces):

  ```json
  {"extraKnownMarketplaces": {"agentic-helpers": {"source": {"source": "github", "repo": "fza/agentic-helpers"}, "autoUpdate": true}}}
  ```

- **To update by hand**, run `claude plugin update agentic-helpers@agentic-helpers`. The update takes
  effect in the next session, or after `/reload-plugins`. While auto-update is off, a seat claim
  says once a day that a newer release is out; `update_notice: false` silences it.

### sdd, for projects with a graph

```bash
brew install networkteam/tap/sdd
```

Without Homebrew:

```bash
curl -sL https://github.com/networkteam/sdd/releases/latest/download/install.sh | sh
```

**Optional: the fork [github.com/fza/sdd](https://github.com/fza/sdd)** (`brew install fza/tap/sdd`).
agentic-helpers works with stock sdd, and uses the fork's extras wherever the installed `sdd`
offers them. [`docs/capture/usage.md`](docs/capture/usage.md) lists them. One caution: once
`sdd migrate` moves a graph to the fork's schema, stock sdd can no longer write to it, so everyone
writing to that graph needs the fork.

## Options

Options are set once per machine and apply to every project.

**Every agent:** `~/.config/agentic-helpers/config.yaml` (under `$XDG_CONFIG_HOME` where it is set)
holds options every agent on the machine shares, one `key: value` per line:

```yaml
memory_language: "de"
retired_body_lifetime: 120d
```

`agentic-helpers config` lists every option, its value and where the value comes from;
`agentic-helpers config set <key> <value>` writes one into the file, checked first.

**Claude Code alone:** run `/plugin`, select agentic-helpers, then Configure options, or add them
under `pluginConfigs` in your user settings, `~/.claude/settings.json` (or the one under
`CLAUDE_CONFIG_DIR`):

```json
{
  "pluginConfigs": {
    "agentic-helpers@agentic-helpers": {
      "options": { "shell_guard_find_delete": "deny" }
    }
  }
}
```

An answer given there wins over `config.yaml`. Managed settings (`managed-settings.json` and its
`managed-settings.d/` drop-ins) take the same block and win over both, so an administrator can lock
an option. An empty value counts as unset, and so does an answer equal to the built-in default:
Claude Code's dialog, saved untouched, stores every switch and picker at its default, which would
otherwise hide `config.yaml`. Setting a key back to its default over a different `config.yaml` takes
editing `config.yaml`. A project's `.claude/settings.json` and
`.claude/settings.local.json` are never read for plugin options: Claude Code itself ignores them
there, and one embedder and one set of guards serve every project on the machine.

**`config.yaml` only:** `retired_body_lifetime`, `tools_version_lifetime`, `temporary_seat_lifetime`, `tools_total_mb` and `orphan_store_grace` decide when
stored data every agent shares is deleted, so Claude Code's dialog lacks them and an answer there is
ignored; two agents set apart would otherwise leave the lowest value deciding for both. A key deleting
only what one session made for itself, such as `capture_remove_draft`, stays in the dialog.

**`config.yaml` only tightens a guard.** Any agent can write the file in one Bash call, so it never
lifts what shell-guard and the grounding gate hold. A guard switch (`shell_guard_enabled`,
`grounding_gate_enabled`, `shell_guard_lone_removal`, `shell_guard_expansion`,
`shell_guard_nested_code`, `shell_guard_mktemp`, `shell_guard_alternatives`) applies there only as
`true`; a verdict (`shell_guard_outside_project`, `shell_guard_find_delete`, `shell_guard_secrets`)
only stricter, `off` < `ask` < `deny`; `shell_guard_secret_output` only stricter, `off` < `files` <
`all`; `shell_guard_ask_timeout` only shorter.

**Time options take a span:** a whole count of seconds, minutes, hours, days or weeks (`45s`, `2m`,
`12h`, `90d`, `2w`), a Go duration as sdd prints one (`1m0s`), or `0`. `retired_body_days`, a number
of days, is still read as `retired_body_lifetime` for one release, and `agentic-helpers check` names it. A looser value there is ignored, and
`agentic-helpers config set` refuses it. `embedding_endpoint`, `embedding_path`,
`embedding_api_key_env` and `shell_guard_secret_output_skip` are never read from `config.yaml`: the
endpoint and key variable together could send any environment variable to a server the writer
picks, and the path to a route there the writer picks. Loosen a guard or point the embedder elsewhere
in Claude Code's settings, behind its permission prompts.

### Turning pieces off

| Option | Default | What it controls |
|---|---|---|
| `memory_enabled` | `true` | agentic-memory. Off, its commands refuse, its hooks stay quiet, its MCP server offers no tools |
| `tools_enabled` | `true` | agentic-tools, the same way |
| `scratch_enabled` | `true` | The scratch sweep |
| `scratch_sweep` | `release` | When a seat's scratch folder goes: `release` at `agentic-memory release`, `session` when its session ends, `idle` only after a week unused |
| `grounding_gate_enabled` | `true` | The grounding gate |
| `capture_enabled` | `true` | `agentic-capture` |
| `shell_guard_enabled` | `true` | Shell-guard |

### Search by meaning

Memory and the tool library can find entries and tools by what they mean, not only by the words they
contain. That needs an embedding model, which turns text into vectors that can be compared. With
one set up:

- `agentic-memory search --query` and `agentic-tools find --query` rank results by meaning.
- Each prompt brings up the memory entries and tools related to it.
- The tool library refuses a new tool that does the same as one it already holds.

Without one, searching by meaning fails with a message naming the cause. Keyword search
(`--term`) and everything else still work. The embedder can run locally (Ollama, LM Studio, any
OpenAI-compatible server) or as a hosted service.

| Option | Default | What it controls |
|---|---|---|
| `embedding_provider` | `auto` | Where the embedder comes from. `auto`: sdd's embedding configuration, where sdd sets one. `openai` for any OpenAI-compatible API, or `ollama`: the options below. `off`: no embedder, and sdd is never run for one. Empty reads as `auto` |
| `embedding_source` | `auto` | No longer in the plugin's dialog, still read where `config.yaml` or Claude Code's settings set it, and named at session start. `sdd`: always sdd's configuration, the options below ignored. `own`: never sdd's, so `embedding_provider` `auto` means no embedder. `auto` changes nothing. An `embedding_provider` of `off` wins over it |
| `embedding_model` | none | The model the endpoint serves, such as `embeddinggemma-300m` |
| `embedding_endpoint` | the provider's | Claude Code's settings only. The server's base URL. Empty means `https://api.openai.com` or `http://localhost:11434` |
| `embedding_path` | `/v1/embeddings` | Claude Code's settings only. The OpenAI-compatible route joined to the endpoint, for a gateway serving it elsewhere (`/v3/compat/embeddings`). An endpoint ending in `/v1` with no path set keeps `/v1/embeddings`. Ollama keeps `/api/embed` |
| `embedding_dimensions` | `0` | Vector size to ask the provider for. `0` keeps the model's own |
| `embedding_api_key_env` | none | Claude Code's settings only. Name of the environment variable holding a hosted service's API key. The key itself is never stored |
| `embedding_timeout` | `2m` | How long one embedding request may take, a span; a fill sending several requests gives each its own, as sdd's `embedding.timeout` does. Hooks keep a shorter limit of their own (20s), never longer than this |
| `embedding_query_template` | none | Wraps a search query or a prompt before it is embedded, `{text}` marking where it goes, as sdd's `embedding.query_template` does (`task: search result \| query: {text}`) |
| `embedding_document_template` | none | Wraps a memory entry or a tool description before it is embedded (`title: none \| text: {text}`) |

**sdd's embedder.** Under sdd's configuration, agentic-helpers runs `sdd config get embedding` once
per process in the project and takes sdd's provider, model, endpoint, path, dimensions, timeout and
both templates together, never mixed with its own. No API key is sent then: sdd prints its keys masked,
and its configuration is a file any agent can write, so a key sent to an endpoint read from it could
go anywhere. sdd's embedder therefore serves a server needing no key, such as a local one; a hosted one
is set in the options above. sdd's endpoint is taken only from its global configuration or its
built-in default: one from the repository's `.sdd/config.yaml` or `.sdd/config.local.yaml`, either of
which a clone can carry, is refused, since the clone would choose where every memory entry and prompt
goes. sdd's `path` is taken from the same layers, for the same reason. The session start says so,
and search by meaning stays off until `sdd config set` sets the endpoint or path globally. Without sdd, or with sdd setting no embedder, search by meaning is off.

**`off`.** `embedding_provider: off` turns every use of an embedder off: search by meaning
(`agentic-memory search --query`), the memory entries each prompt brings up, the tools offered at a
prompt, `agentic-tools find --query`, and the near-duplicate description check on `agentic-tools add`.
Keyword search, filing entries and tools, and the byte-identical duplicate check keep working.

Changing the provider, the model, the dimensions or a template starts a fresh vector index, since vectors from
two models cannot be compared. Memory embeds its entries again on its next search by meaning;
`agentic-tools reindex` does the same for the tool library.

**Input sizing.** An embedder past its input limit drops the rest of a text without an error, so
every text is sized to fit. The first search on an endpoint measures its limit with a few probe
requests, as sdd does, and keeps the result in the cache per model, endpoint and path. Entries and
tool descriptions are embedded in chunks that fit; a search phrase or prompt past the limit is
embedded by its head. A provider that reports no token count gets 2048 characters per input. A new
measurement, after an upgrade or a change of endpoint, embeds each index again once, by itself.

### Memory

| Option | Default | What it controls |
|---|---|---|
| `entry_max_words` | `500` | Suggested word limit for an entry's body |
| `state_stale_after_writes` | `5` | File writes after which the agent is asked once, at the end of a turn, to record where work stands |
| `retired_body_lifetime` | `90d` | `config.yaml` only. How long a retired entry keeps its body (at least `14d`) |
| `entry_id_length` | `6` | Characters after `m-` in a new entry's id (6 to 16) |
| `brief_note_limit` | `10` | Notes the brief shows, most used first. `agentic-memory brief --kind note` lists them all |
| `attachment_max_mb` | `10` | Size limit for one attached file, in MB |
| `attachments_total_mb` | `2048` | Size limit for every attached file on the machine together, across every project, in MB, each file counted once; never below `attachment_max_mb` |
| `memory_language` | `en` | Language entries are written in, unless `.sdd/config.yaml` names one |
| `writing_style` | `terse` | How agents write memory entries, the state and tool library notes: `terse` (no articles or filler) or `plain`. Graph entries stay plain prose |
| `memory_require_git` | `true` | Memory works only inside a git work tree |
| `memory_turns_off_auto` | `true` | Claiming a seat turns Claude Code's built-in auto memory off for the project, from the next session, through `.claude/settings.local.json` |
| `update_notice` | `true` | A seat claim says once a day that a newer release is out while auto-update is off; `false` also stops the daily check with GitHub |
| `temporary_seat_lifetime` | `1d` | `config.yaml` only. How long a closed temporary shelf keeps its entries before they are deleted |
| `orphan_store_grace` | `30d` | `config.yaml` only. How long a store whose checkout cannot be found waits for one to claim it before it is deleted |

### Tool library

| Option | Default | What it controls |
|---|---|---|
| `tools_query_floor` | `0.45` | Similarity (cosine) from which a tool counts as a match for a search or a prompt |
| `tools_duplicate_floor` | `0.85` | Similarity from which a new tool is refused as a duplicate of another |
| `tools_total_mb` | `500` | `config.yaml` only. Size limit for every stored version of every tool library on the machine, in MB. Past it, versions that are neither current nor a fork's origin are removed oldest first; never below the fixed 1 MB a tool may hold |
| `tools_version_lifetime` | `90d` | `config.yaml` only. How long a tool version stays once a newer one replaced it, whatever space is left (at least `14d`); a current version or a fork's origin never goes |

### Capture

| Option | Default | What it controls |
|---|---|---|
| `capture_remove_draft` | `true` | `agentic-capture` deletes a draft once its entry is captured, unless git tracks it or it lies outside the work tree and `/tmp` |

### Shell-guard

An option whose default is `ask` also takes `deny` or `off`. `config.yaml` sets each only stricter
than its default.

| Option | Default | What it controls |
|---|---|---|
| `shell_guard_outside_project` | `ask` | A deletion outside the project and `/tmp`, or a command whose name is only known at run time (`$CMD -rf /`) |
| `shell_guard_find_delete` | `ask` | `find ... -delete` |
| `shell_guard_secrets` | `ask` | A Bash command that seems to hold a credential |
| `shell_guard_lone_removal` | `true` | Refuses a deletion that shares its Bash call with other commands |
| `shell_guard_expansion` | `true` | Refuses a deletion whose target comes from a variable or a command substitution |
| `shell_guard_nested_code` | `true` | Refuses a deletion or `mktemp` inside `eval`, a shell's `-c`, or another language's inline code |
| `shell_guard_mktemp` | `true` | Refuses `mktemp` in any Bash command |
| `shell_guard_alternatives` | `true` | Refuses a command missing here (`timeout`) or a usage BSD `sed` or `date` gets wrong, naming the command that works, and names at session start what the machine needs |
| `shell_guard_secret_output` | `all` | Hides output lines from outside the project that look like credentials. `files` checks only Read, Grep, NotebookRead, Edit, Write, NotebookEdit and Bash; `off` checks nothing |
| `shell_guard_secret_output_skip` | none | Claude Code's settings only. Tools whose output is never hidden, comma-separated. Patterns such as `mcp__sdd__*` work |
| `shell_guard_ask_timeout` | `2m` | How long an ask waits in `auto` and `bypassPermissions` mode before it denies (at most `1h`; `0` waits forever; a bare number counts seconds) |

## Dependencies

Turning one piece off never breaks another. A feature that relies on the piece switches off with
it, and each session start names it:

```text
agentic-helpers: scratch is off, so shell-guard's mktemp ban is off; turn scratch_enabled on to restore it
```

`agentic-helpers check` lists every piece, on or off, with the features it lost.

| Turned off | Also off | Because |
|---|---|---|
| `scratch_enabled` | Shell-guard's `mktemp` ban, whatever `shell_guard_mktemp` says | The ban sends scratch files to `.tmp/`, which nothing would sweep |
| `grounding_gate_enabled` | `agentic-capture` | Its grounding check is the gate's hook; without it, a draft lands unchecked |
| `memory_enabled` | The grounding gate's `exempt_seats` | A seat is a memory claim, and a claim left over from before must not exempt anything |
| `memory_enabled` | The seat name agentic-tools stamps on what it files | The name comes from memory's seat registry |

## Contributing

The rules live in [`AGENTS.md`](AGENTS.md), plus [`source/AGENTS.md`](source/AGENTS.md) for Go
code. They apply to people and agents alike. In short:

- **Match the code around your change**: its naming, its comment density, its idiom. A comment says
  only what the code cannot.
- **Every change carries tests, and they pass before it lands.** Break each new guard once and watch
  its test fail.
- **Update the `docs/` pages and skills a change makes untrue, in the same commit.** A hook's output
  wording is part of its contract, because its skill quotes it.
- **One commit per change**, with a single Conventional Commits subject line of at most 100
  characters and nothing after it.

```bash
go install -C source/hooks ./cmd/...                       # build every binary
cd source/hooks && go vet ./... && go test -race ./...     # run every Go test
claude plugin validate .                                   # check the plugin and marketplace manifests
```

To run your checkout's binaries in your own sessions, point `AGENTIC_HELPERS_BIN_DIR` at the
`go install` directory; [`docs/plugin/usage.md`](docs/plugin/usage.md) explains how. Only the
`release` workflow cuts releases.

## Documentation

How to use each piece, and how each one works inside: [`docs/index.md`](docs/index.md).

## Licence

MIT. See [`LICENSE`](LICENSE).
