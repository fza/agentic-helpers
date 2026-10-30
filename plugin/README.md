# agentic-helpers

A Claude Code plugin that builds better habits into every session: memory that survives a
compaction, a shared library of helper scripts, one place for throwaway files, a gate that makes
the agent read a project's decision graph before it asks or drafts, a checked path for new graph
entries, and a guard on every Bash call. Each piece comes with a skill that explains it to the
agent. The full guide lives in the repository: <https://github.com/fza/agentic-helpers>.

## Install

```
/plugin install agentic-helpers --marketplace fza/agentic-helpers
```

Claude Code 2.1.275 or later takes this in one step; older releases run
`claude plugin marketplace add fza/agentic-helpers`, then
`claude plugin install agentic-helpers@agentic-helpers`. Claude Code keeps a third-party plugin on
the version first installed: turn auto-update on under `/plugin`, Marketplaces, or update with
`claude plugin update agentic-helpers@agentic-helpers`.

## Pieces

- **agentic-memory** keeps notes the agent reads again after a compaction or in a new session, and
  serves them over the `memory` MCP server.
- **agentic-tools** shares helper scripts between agents, over the `tools` MCP server.
- **agentic-scratch** gives each seat or session a `.tmp/claude/` folder and empties it when the
  work ends.
- **agentic-grounding-gate** holds questions, drafts and captures until the agent has read the
  project's [sdd](https://github.com/networkteam/sdd) decision graph.
- **agentic-capture** writes new graph entries through one checked path.
- **agentic-shell-guard** keeps deletions inside the project and credentials out of commands and
  output.

## Example prompts

- "main seat": the agent claims its memory seat and says where the work stands.
- "What did we decide about the release workflow?": the gate has the agent search the graph first.
- "Write a script that counts tokens per tool reply": the agent first looks for a tool another
  agent already wrote.

## What it runs

The hooks and both MCP servers run `hooks/run`, a shell script that starts the Go binaries of the
release named in `.claude-plugin/plugin.json`. On the first run of a release it downloads them from
`https://github.com/fza/agentic-helpers/releases`, checks each archive against the release's
`checksums.txt`, and keeps them under Claude Code's plugin data folder. The binaries are built
from the repository's `source/hooks` by its release workflow.

A session end starts one detached `agentic-helpers sweep` process that tidies the plugin's own
data. `agentic-memory wait` may wake an idle session when another session files an entry for it.

## What it sends

Nothing leaves the machine unless one of these applies:

- **An embedder is configured.** Memory and the tool library search by meaning through an
  embedding endpoint: the one sdd's own settings name (the default), OpenAI, or Ollama. Prompts,
  entry text and tool descriptions go to it, with the API key the options name; a prompt that looks like it holds a credential is never sent. With
  `embedding_provider: off`, nothing goes.
- **A graph entry is captured.** sdd's pre-flight check sends the draft to Claude through
  `claude -p`.
- **A release is first run.** The binaries are fetched from GitHub, as above.
- **A seat is claimed, at most once a day.** The claim asks GitHub's API for the latest release, to
  say when a newer one is out while auto-update is off. The request carries nothing but the
  plugin's name as its user agent. With `update_notice: false`, nothing goes.

## What it writes outside the project

- Its data and cache under `~/.local/share/agentic-helpers` and `~/.cache/agentic-helpers` (or
  their XDG locations): memory stores, the tool library, the tail of each session's transcript
  for the next claim, and recent prompts with their vectors.
- `~/.config/agentic-helpers/config.yaml`, when an option is set there.
- `.claude/settings.local.json` of the project's main checkout: claiming a seat sets
  `autoMemoryEnabled` to `false` there, so Claude Code's own memory does not compete. The option
  `memory_turns_off_auto: false` leaves the file alone.
- Git's global excludes file gains `.tmp/` and `**/.claude/settings.local.json`; a repository's
  `.git` gains an `agentic-store` file naming its memory store.

## What it decides

- **shell-guard** asks before, or refuses, a Bash call that deletes outside the project and `/tmp`,
  holds a credential, or uses a command missing on the machine. In auto mode an ask nobody answers
  is refused after a countdown. It never approves a call. After every tool call it replaces output
  lines from outside the project that look like a credential.
- **The grounding gate** refuses graph calls and captures until the agent has read the graph.
- **agentic-memory** can hold the end of a turn once, asking the agent to write where its work
  stands.

Every piece can be switched off in the plugin's options.

## Support

Issues and questions: <https://github.com/fza/agentic-helpers/issues>. Licensed under MIT.
