# The plugin: usage

## How a project opts in

**No hook does anything until the project carries what it operates on.** Nothing is created,
nothing is asked, and a project carrying none of it never hears from any hook.

| Hook | Runs when the project carries | Otherwise |
|---|---|---|
| Memory | a seat claimed with `agentic-memory claim <seat>`; where a project has memory but this session holds no seat, one line names the seats | exits 0, silently |
| Grounding gate | a `.sdd/` decision graph, at the project root or above it, without `enabled: false` in its `grounding.yaml` | exits 0, silently |
| Scratch sweep | a `.tmp/` directory | exits 0, silently |
| Tool library | a git work tree, on a machine whose library `~/.claude/agentic-tools/` the first `agentic-tools add` created | exits 0, silently |

So opting a project in is one command, `mkdir .tmp`, `agentic-memory claim <seat>` or a first
`agentic-tools add`, and opting out is removing the directory or releasing the seat. A project using none needs no configuration, no
allowlist entry and no marker file.

## sdd

The grounding gate and `agentic-capture` work only with sdd built from the fork
[github.com/fza/sdd](https://github.com/fza/sdd). A build is the fork when `sdd --version` carries
`fza`. With any other sdd, `agentic-capture` refuses to run and says how to install the fork, and the
grounding gate says the same once as a session starts, then stays off for that session.

## Commands

Claude Code's Bash tool finds `agentic-capture`, `agentic-memory` and `agentic-tools` through the plugin's `bin/`,
which hands each call to the launcher, so `agentic-capture --help` is answered by the real binary.
With no binary installed and none fetchable, a command from `bin/` or the MCP server exits 1 and
says why on stderr, where a hook stays silent. A script outside Claude Code has no plugin on its
PATH and builds the binaries itself: `go install -C source/hooks ./cmd/...` from a checkout.

## Options

The plugin's configuration dialog (`/plugin`, or `pluginConfigs` in the user or managed settings)
carries agentic-memory's options and agentic-tools' `tools_duplicate_floor`; no project overrides
them. [`../memory/usage.md`](../memory/usage.md) and [`../tools/usage.md`](../tools/usage.md) list each.

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

The binaries the checkout builds reach the hooks through `AGENTIC_HELPERS_BIN_DIR`, set once in the
`env` block of the settings file to the directory `go install` writes to:

```json
{ "env": { "AGENTIC_HELPERS_BIN_DIR": "/Users/you/go/bin" } }
```

A change to a binary then reaches the hooks on the next event after
`go install -C source/hooks ./cmd/...`. While the variable is set, no release is fetched or run on
that machine.
