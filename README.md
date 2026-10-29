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
| scratch sweep | `agentic-scratch` | Gives each session one place for throwaway files, and empties it at the end |
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

```bash
claude plugin marketplace add fza/agentic-helpers
claude plugin install agentic-helpers@agentic-helpers
```

- **From inside a session**, one step does both:
  `/plugin install agentic-helpers --marketplace fza/agentic-helpers`.
- **To pin a release**, add the marketplace as `fza/agentic-helpers#v<version>`.
- **Installed at user scope**, the plugin runs in every project on the machine.
- **To update**, run `claude plugin update agentic-helpers@agentic-helpers`. The update takes effect
  in the next session, or after `/reload-plugins`. Auto-update is off by default; turn it on under
  `/plugin`, Marketplaces.

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
retired_body_days: 120
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
an option. An empty value counts as unset. A project's `.claude/settings.json` and
`.claude/settings.local.json` are never read for plugin options: Claude Code itself ignores them
there, and one embedder and one set of guards serve every project on the machine.

**`config.yaml` only:** `retired_body_days`, `temporary_seat_lifetime` and `tools_total_mb` decide when
stored data every agent shares is deleted, so Claude Code's dialog lacks them and an answer there is
ignored; two agents set apart would otherwise leave the lowest value deciding for both. A key deleting
only what one session made for itself, such as `capture_remove_draft`, stays in the dialog.

### Turning pieces off

| Option | Default | What it controls |
|---|---|---|
| `memory_enabled` | `true` | agentic-memory. Off, its commands refuse, its hooks stay quiet, its MCP server offers no tools |
| `tools_enabled` | `true` | agentic-tools, the same way |
| `scratch_enabled` | `true` | The scratch sweep |
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
| `embedding_provider` | none | `openai` for any OpenAI-compatible API, or `ollama`. Empty turns search by meaning off |
| `embedding_model` | none | The model the endpoint serves, such as `embeddinggemma-300m` |
| `embedding_endpoint` | the provider's | The server's base URL. Empty means `https://api.openai.com` or `http://localhost:11434` |
| `embedding_dimensions` | `0` | Vector size to ask an `openai` provider for. `0` keeps the model's own; `ollama` ignores it |
| `embedding_api_key_env` | none | Name of the environment variable holding a hosted service's API key. The key itself is never stored |

Changing the provider, the model or the dimensions starts a fresh vector index, since vectors from
two models cannot be compared. Memory embeds its entries again on its next search by meaning;
`agentic-tools reindex` does the same for the tool library.

### Memory

| Option | Default | What it controls |
|---|---|---|
| `entry_max_words` | `500` | Suggested word limit for an entry's body |
| `state_stale_after_writes` | `5` | File writes after which the agent is asked once, at the end of a turn, to record where work stands |
| `retired_body_days` | `90` | `config.yaml` only. Days a retired entry keeps its body (at least 14) |
| `entry_id_length` | `6` | Characters after `m-` in a new entry's id (6 to 16) |
| `brief_note_limit` | `10` | Notes the brief shows, most used first. `agentic-memory brief --kind note` lists them all |
| `attachment_max_mb` | `10` | Size limit for one attached file, in MB |
| `attachments_total_mb` | `2048` | Size limit for every attached file on the machine together, across every project, in MB, each file counted once; never below `attachment_max_mb` |
| `memory_language` | `en` | Language entries are written in, unless `.sdd/config.yaml` names one |
| `writing_style` | `terse` | How agents write memory entries, the state and tool library notes: `terse` (no articles or filler) or `plain`. Graph entries stay plain prose |
| `memory_require_git` | `true` | Memory works only inside a git work tree |
| `memory_turns_off_auto` | `true` | Claiming a seat turns Claude Code's built-in auto memory off for the project, from the next session, through `.claude/settings.local.json` |
| `temporary_seat_lifetime` | `1d` | `config.yaml` only. How long a closed temporary shelf keeps its entries before they are deleted |

### Tool library

| Option | Default | What it controls |
|---|---|---|
| `tools_query_floor` | `0.45` | Similarity (cosine) from which a tool counts as a match for a search or a prompt |
| `tools_duplicate_floor` | `0.85` | Similarity from which a new tool is refused as a duplicate of another |
| `tools_total_mb` | `500` | `config.yaml` only. Size limit for every stored version of every tool library on the machine, in MB. Past it, versions that are neither current nor a fork's origin are removed oldest first; never below the fixed 1 MB a tool may hold |

### Capture

| Option | Default | What it controls |
|---|---|---|
| `capture_remove_draft` | `true` | `agentic-capture` deletes a draft once its entry is captured, unless git tracks it or it lies outside the work tree and `/tmp` |

### Shell-guard

An option whose default is `ask` also takes `deny` or `off`.

| Option | Default | What it controls |
|---|---|---|
| `shell_guard_outside_project` | `ask` | A deletion outside the project and `/tmp`, or a command whose name is only known at run time (`$CMD -rf /`) |
| `shell_guard_find_delete` | `ask` | `find ... -delete` |
| `shell_guard_secrets` | `ask` | A Bash command that seems to hold a credential |
| `shell_guard_lone_removal` | `true` | Refuses a deletion that shares its Bash call with other commands |
| `shell_guard_expansion` | `true` | Refuses a deletion whose target comes from a variable or a command substitution |
| `shell_guard_nested_code` | `true` | Refuses a deletion or `mktemp` inside `eval`, a shell's `-c`, or another language's inline code |
| `shell_guard_mktemp` | `true` | Refuses `mktemp` in any Bash command |
| `shell_guard_secret_output` | `all` | Hides output lines from outside the project that look like credentials. `files` checks only Read, Grep, NotebookRead, Edit, Write, NotebookEdit and Bash; `off` checks nothing |
| `shell_guard_secret_output_skip` | none | Tools whose output is never hidden, comma-separated. Patterns such as `mcp__sdd__*` work |
| `shell_guard_ask_timeout` | `120` | Seconds an ask waits in `auto` and `bypassPermissions` mode before it denies (at most 3600; `0` waits forever) |

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
