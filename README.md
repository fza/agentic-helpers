# agentic-helpers

Your coding agent is smart, but it forgets. Once its context fills up, the work so far shrinks to a
summary, and a new session knows nothing at all. It writes the same helper script for the third
time, guesses about decisions your team already made, leaves probe files behind, and now and then
runs a command you would never have allowed.

agentic-helpers fixes that, quietly, for every Claude Code session on your machine. Install it once:
your agent remembers where work stands, reuses what other agents already built, checks your
project's decisions before it acts, cleans up after itself, and stays inside safe limits. There is
nothing to run by hand. Hooks do the work in the background, and a short skill per piece tells the
agent what it is doing. The goal is an agent you can leave alone for longer, and trust more when you
come back.

> **Your data stays on your machine.** agentic-helpers keeps memory, tools and state in local files
> and sends none of it anywhere. Text leaves the machine only for an embedding model you configure
> for search by meaning, and that model can run locally (Ollama, LM Studio). Beyond that, it
> downloads its own release from GitHub and, once a day while auto-update is off, asks GitHub
> whether a newer one is out (`update_notice: false` stops it). A graph capture runs sdd's
> pre-flight, which sends the draft to Claude through `claude -p`.
> [plugin/README.md](plugin/README.md#what-it-sends) lists every case.

| Piece | Command | What it does |
|---|---|---|
| memory | `agentic-memory` | Keeps notes the agent reads again after a compaction or in a new session |
| tool library | `agentic-tools` | Shares helper scripts between agents, so none is written twice |
| scratch sweep | `agentic-scratch` | Gives each seat or session one place for throwaway files, and empties it when the work ends |
| grounding gate | `agentic-grounding-gate` | Makes the agent read the decision graph before it asks, drafts or captures, and collapses sdd output the agent was already shown |
| capture | `agentic-capture` | Writes new graph entries through one checked path, free of credentials |
| shell-guard | `agentic-shell-guard` | Keeps deletions inside the project, and credentials out of commands and output |

Memory and the tool library are also available as MCP servers, `memory` and `tools`.

## Features

### Memory

- **Work survives a compaction.** The agent claims a seat, notes where work stands as it goes, and
  gets it back after a compaction or in the next session.
- **Notes fade on their own.** Each entry has a kind: a rule from you never fades, a trap that cost
  time lasts months, a todo days. Stale notes drop out without cleanup.
- **The right note at the right time.** Each prompt brings up the few entries related to it, by
  words or by meaning.
- **Edits to `CLAUDE.md` and `AGENTS.md` reach running sessions.** Claude Code reads `CLAUDE.md`,
  `CLAUDE.local.md`, their `@` imports (an `AGENTS.md`, say) and `~/.claude/CLAUDE.md` only at
  session start. Memory watches them, and every
  running session that loaded a changed one reads it again at its next prompt or tool call.
- **Short-term memory beside the decision graph.** [sdd](https://github.com/networkteam/sdd) keeps
  a project's decisions and their reasons under `.sdd/`, in the repository, reviewed like code.
  Memory keeps what one job needs to carry on, on the machine, and points at graph entries instead of
  copying them.

### Tool library

- **Helper scripts are shared.** A script one agent wrote is found by every other agent on the
  machine, in any project, by name or by meaning.
- **No tool twice.** A new tool that does what a stored one does is refused, naming the existing one.
- **Versions are kept.** An update keeps the old version for a while; a change to another project's
  tool becomes a fork.

### Scratch sweep

- **One place for throwaway files.** Each seat or session gets its own folder under `.tmp/`.
- **Emptied when the work ends**: at release, at session end, or after a week unused, as you choose.
  Nothing outside that folder is ever touched.

### Shell-guard

- **Deletions stay inside the project.** Removals and moves outside the project and `/tmp` ask
  first, and so does `find -delete`.
- **Credentials stay out.** A command holding what looks like a credential asks first, and output
  lines from outside the project that look like one are withheld.
- **Commands that work on this machine.** A command missing here (`timeout`) or a usage BSD `sed`
  or `date` gets wrong is refused, naming the one that works.

## Extended sdd features

For projects keeping a decision graph with [sdd](https://github.com/networkteam/sdd), in the
repository itself or in a separate one.

### Grounding gate

- **Decisions are read before they are discussed.** The agent must search and list the graph before
  it asks you a question, drafts an entry or captures one.
- **Repeated sdd output shrinks.** An entry the agent was already shown comes back as a short
  pointer instead of in full.

### Capture

- **One checked path into the graph.** Every new entry passes the project's lint, sdd's pre-flight
  and a credential scan before it is written.

## Measurements

Token use with agentic-helpers against the same work without it, measured on real Claude Code
transcripts. Output tokens are counted from the API's own `output_tokens`; output is priced at about
five times input.

| Work | Without agentic-helpers | With agentic-helpers | Saved |
|---|---|---|---|
| Getting a helper script the library already holds | write it: 1,307 output tokens (median), about 6,500 input-token equivalents | find and take it: about 4,100 input-token equivalents | about 37% per script; 40 of 49 stored tools cost more to write than to find |
| Reading sdd decision-graph entries again | 42% of 13.8 million characters of sdd output repeat what the agent already saw | entries already shown come back as a short pointer; a run of repeated reads fell from 9,426 to 7,714 characters | about 18% on that run |


## Good to know

- **One install covers the whole machine.** Each piece stays quiet in a project where it has nothing
  to do. Shell-guard is the exception: it checks every Bash call in every project, unless its
  options turn the guard feature off.
- **The hooks and commands are Go binaries** built from `source/hooks`. Each release publishes them,
  and the plugin downloads them. `agentic-memory`, `agentic-tools`, `agentic-capture` and
  `agentic-scratch` are one program, `agentic-helpers`, answering to each name.
- **Subagents get what their spawner names.** One line `agentic-helpers: <level>` in the Agent
  prompt picks `none` (the scratch folder and machine notes, the default), `rules`, `silent`
  (read-only memory), `shared` or `full` (a memory seat of its own). A subagent never gets more than
  its spawner.
- **Claude Code only.** The hooks read Claude Code's payloads, settings and transcripts. No other
  agent is tested or supported.

## Requirements

- macOS or Linux, on amd64 or arm64.
- Claude Code. The one-step `/plugin install` with `--marketplace` needs 2.1.275 or later.
- git. Memory works only inside a git work tree, unless `memory_require_git` is off.
- [sdd](https://github.com/networkteam/sdd), only for projects with a graph under `.sdd/`. Collapsing
  repeated sdd output needs the fza fork, `v0.18.0-alpha.2.fza.41` or later (`sessions.token_saving`);
  with any other sdd, answers pass through as sdd gives them. Collapsing a `view` answer needs
  `v0.18.0-alpha.2.fza.42` or later (`view_sections`).

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
  {
    "extraKnownMarketplaces": {
      "agentic-helpers": {
        "source": {
          "source": "github",
          "repo": "fza/agentic-helpers"
        },
        "autoUpdate": true
      }
    }
  }
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
offers them. [`docs/capture/usage.md`](docs/capture/usage.md) lists them.

## Options

Options are set once per machine and apply to every project. Two places set them:

- `~/.config/agentic-helpers/config.yaml`, shared by every agent. `agentic-helpers config` lists
  every option and where its value comes from; `agentic-helpers config set <key> <value>` writes one.
- Claude Code's `/plugin` dialog (agentic-helpers, Configure options), or `pluginConfigs` in
  `~/.claude/settings.json`. It wins over `config.yaml`; managed settings win over both.

```yaml
memory_language: "de"
retired_body_lifetime: 120d
```

```json
{
  "pluginConfigs": {
    "agentic-helpers@agentic-helpers": {
      "options": { "shell_guard_find_delete": "deny" }
    }
  }
}
```

Rules, in short ([docs/plugin/usage.md](docs/plugin/usage.md#options) has the detail):

- **`config.yaml` only tightens a guard**, since any agent can write it. Loosening one, or pointing
  the embedder elsewhere, takes Claude Code's settings.
- **Keys deleting shared data live in `config.yaml` only**, marked so in the tables below.
- **Times take a span**: `45s`, `2m`, `12h`, `90d`, `2w`, or `0`.
- A project's own `.claude/settings*.json` is never read for these options.

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

An embedding model lets memory and the tool library search by meaning, bring up related entries
and tools at each prompt, and refuse a duplicate tool. It runs locally (Ollama, LM Studio, any
OpenAI-compatible server) or hosted. Without one, keyword search (`--term`) and everything else
still work.

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

sdd's embedder is used without an API key, and only with an endpoint from sdd's global
configuration, never one a repository's `.sdd/` carries. Changing the model or provider re-embeds
everything once. [docs/memory/usage.md](docs/memory/usage.md) has the detail.

### Memory

| Option | Default | What it controls |
|---|---|---|
| `entry_target_words` | `500` | Words an entry's body aims at, told to agents; a longer body lands whole. The older `entry_max_words` is still read for one release |
| `landed_scan_interval` | `10m` | How often memory scans the checkout's history for commits that may settle a live todo or trap (at least `1m`); a git merge, commit, pull, rebase or cherry-pick scans at once |
| `retired_body_lifetime` | `90d` | `config.yaml` only. How long a retired entry keeps its body (at least `14d`) |
| `entry_id_length` | `6` | Characters after `m-` in a new entry's id (6 to 16) |
| `brief_note_limit` | `10` | Notes the brief shows, most used first. `agentic-memory brief --kind note` lists them all |
| `attachment_max_mb` | `10` | Size limit for one attached file, in MB |
| `attachments_total_mb` | `2048` | Size limit for every attached file on the machine together, across every project, in MB, each file counted once; never below `attachment_max_mb` |
| `memory_language` | `en` | Language entries are written in, unless `.sdd/config.yaml` names one |
| `writing_style` | `terse` | How agents write memory entries, state lines and tool library notes: `terse` (no articles or filler) or `plain`. Graph entries stay plain prose |
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
| `cache_retention` | `90d` | `config.yaml` only. How long a vector store of memory or the tool library survives once no binary has read or written it (at least `14d`); a daily background run deletes it after, and a changed embedder's store goes this way |

### Capture

| Option | Default | What it controls |
|---|---|---|
| `capture_remove_draft` | `true` | `agentic-capture` deletes a draft once its entry is captured, unless git tracks it or it lies outside the work tree, the graph repository's work tree and `/tmp` |

### Shell-guard

An `ask` option also takes `deny` or `off`.

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
| `shell_guard_extra_roots` | none | Claude Code's settings only. Directories outside a project its sessions may delete in and move from, as comma-separated `<project>=<root>[;<root>]` entries (`~/code/app=~/code/lib/.tmp`) |
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

- **Maintain existing coding style.** Match the code around your change: its naming, its comment
  density, its idiom. A comment says only what the code cannot.
- **Every change carries tests, and they pass before it lands.** Break each new guard once and watch
  its test fail.
- **Update the `docs/` pages and skills a change makes untrue, in the same commit.** A hook's output
  wording is part of its contract, because its skill quotes it.
- **One commit per change**, with a single Conventional Commits subject line of at most 100
  characters and nothing after it.

```bash
GOBIN="$PWD/.claude/bin" go install -C source/hooks ./cmd/...   # build every binary
cd source/hooks && go vet ./... && go test -race ./...           # run every Go test
claude plugin validate .                                         # check the plugin and marketplace manifests
```

Never install the binaries into `~/go/bin` or another directory on your PATH: every project's
shell would run them ahead of the plugin. To run your checkout's binaries in your own sessions,
point `AGENTIC_HELPERS_BIN_DIR` at the checkout's `.claude/bin`; [`docs/plugin/usage.md`](docs/plugin/usage.md) explains how. Only the
`release` workflow cuts releases.

## Testing

Development happens on macOS, and Linux support is tested in Linux containers. CI runs every Go
suite on ubuntu and macOS for each push; `scripts/linux-test` runs the same `go vet` and
`go test -race` locally, in a container of Apple's [`container`](https://github.com/apple/container)
tool, before a push.

`container` runs each Linux container as a lightweight virtual machine on Apple silicon. Its
service, `container-apiserver`, runs under launchd once started and stays up across shells. The
first start installs the Linux kernel the virtual machines boot. `scripts/linux-test` mounts the
checkout into a `golang` image, since tests read the plugin and docs beside the module, and runs
the suites as your own user, as CI does: as root, a test of a permission refusal would pass through
it. Go's module and build caches stay in `.tmp/linux-test/`, so a second run downloads and compiles
only what changed. Its arguments go to `go test` (`./...` when it gets none).

```bash
brew install container                          # once
container system start --enable-kernel-install  # once per boot; installs the kernel on the first start
container list                                  # answers while the service runs
scripts/linux-test                              # vet and race-test every package on Linux
scripts/linux-test ./internal/slots/            # one package
```

`container system status` can report the service as not running while it runs; `container list`
asks the service itself. Where macOS and Linux differ, as in what a sparse file costs on disk, a
change is measured on both.

## Documentation

How to use each piece, and how each one works inside: [`docs/index.md`](docs/index.md).

## Licence

MIT. See [`LICENSE`](LICENSE).
