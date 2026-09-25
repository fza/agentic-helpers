# agentic-helpers

Two hooks that make a coding agent pick up where the last one stopped, and keep it from writing
from memory, with a skill behind each one telling the agent what it is looking at. They install
once, for every project on the machine, and stay silent in a project that does not want them.

The hooks are Claude Code hooks, driven as a subprocess on an event: Go binaries built from
`source/hooks` and installed into `~/go/bin`.
The skills are plain Markdown, loaded by the agent when the work calls for them.

## What each one does

### The carry-forward

A session runs out of context and the next one starts knowing nothing. The carry-forward is the file
that survives that: one per seat, written by the session holding it and read by the session that
follows.

A **seat** is a named job at the work, held by one session at a time. Two sessions holding the same
seat would overwrite each other's file, and whichever wrote last would win in silence, so a seat is
claimed, the claim names the process holding it, and a claim against a live holder meets a refusal.

The hook watches how full the session's context is and how far its transcript has grown. As either
approaches the point where the session will be summarised, it says so, with what to write down and
where. It also keeps a rolling log of what the session did, in pieces small enough for the client to
inline, so the reminder arrives with the material rather than asking a session to remember it.

```
.memory/
├── MEMORY.md                        one pointer line per surviving file
├── carryforward/<seat>-memory.md    one per seat, written by its holder alone
├── roles/<seat>.json                who holds it, and which process
└── transcripts/<session>.md         what this session did, rolled forward
```

### The scratch sweep

An agent writes probes, one-off scripts and captured output. Sent to the system temporary directory
they are invisible to whoever reviews the work; left at the root of the project's own `.tmp/` they
pile up, and nothing tells a throwaway apart from a draft that still matters.

So a session writes its throwaway under a directory named for itself, and that directory goes when
the session ends. A directory abandoned by a session that crashed is reaped a week later.

**Only a directory named for a session is ever removed.** A name the sweep cannot parse as a session
identifier is somebody's deliberate keep, and survives whatever its age.

```
.tmp/
├── claude/<session-id>/   throwaway. Swept.
├── claude/<name>/         kept. Never swept.
├── drafts/                awaiting capture. Never swept.
└── <the project's own>    never touched
```

At session start it also reports, without blocking, a carry-forward that has grown past the size a
handover holds, and any `MEMORY.md` pointer that resolves to nothing.

### The grounding gate

An agent asked to reason about a decision graph will answer from the entry it already read, or from
what it believes the graph holds. Both are how a wrong answer gets written down with confidence.

The gate refuses a question or a draft edit until the turn carries real reads: a literal search and
a semantic one, because each misses what the other finds, and a listing, because an empty search
result is never evidence of absence. A project naming a topic prefix in `.sdd/grounding.yaml`
(`listing_prefix: area-`) owes a listing of one topic carrying it; every other project owes the view
of every topic, `sdd view --layout "active:as-counts"`. It also refuses a call that would discard the
graph tool's own output, since a swallowed refusal reads exactly like an empty graph, and a read
that stops at the entry itself, since a body is immutable and a later entry may have renamed every
surface it names.

## The skills

A hook fires and an agent that has never met it has to work out what just happened. Each hook has a
skill carrying what it cannot say in a one-line refusal.

| Skill | Carries |
|---|---|
| `agent-carryforward` | what a seat is, how to claim one, what belongs in a carry-forward against what belongs in the graph, and the handoff procedure |
| `agent-grounding` | the three reads that open the gate, how deep to read an entry and why, which reference kind is the sharp one, and what each refusal means |
| `agent-scratch` | where throwaway goes, what the sweep removes, and how to keep something from it |

Each skill is the whole of its rules. A global instruction file names the skill and says to load it;
it never restates what the skill holds, because two copies drift and a reader obeys whichever they
opened.

Each says in its own description that it applies only to a project carrying the directory its hook
needs, so an agent in an unrelated project has no reason to open either.

Skills are linked rather than copied, so an edit here reaches every project at once.

## How a project opts in

**Neither hook does anything until the project carries what it operates on.** Nothing is created,
nothing is asked, and a project that has neither directory never hears from either hook.

| Hook | Runs when the project carries | Otherwise |
|---|---|---|
| Carry-forward | a `.memory/` directory | exits 0, silently |
| Grounding gate | a `.sdd/` decision graph, at the project root or above it | exits 0, silently |
| Scratch sweep | a `.tmp/` directory | exits 0, silently |

So opting a project in is one command:

```bash
mkdir .memory          # start keeping a carry-forward here
```

and opting out is removing the directory. A project using neither needs no configuration, no
allowlist entry and no marker file, and installing these hooks costs it nothing.

## Installing

```bash
git clone git@github.com:fza/agentic-helpers.git
cd agentic-helpers
go install -C source/hooks ./cmd/...  # build the Go hooks into ~/go/bin
./install.py            # wire the hooks in, link the skills
./install.py --print    # write nothing, show what would land
./install.py --remove   # take both back out
```

The installer edits only the hook entries running these scripts and leaves every other key exactly
as it found it, which matters because that file also carries credentials. A timestamped copy goes
beside it before anything is written. Running it twice changes nothing the second time, and a
checkout that moved leaves nothing stale behind, because entries are matched by the script they run
rather than by the directory they name.

A session already running keeps the wiring it started with.

A Go hook runs from `~/go/bin` (or wherever `GOBIN` points), not from this checkout, so a pull
changes nothing until `go install -C source/hooks ./cmd/...` runs again. The installer warns about a
binary it wires that is not there yet.

A skill link naming a directory that moved is replaced. A directory somebody else put there under
one of these names is left alone and reported, rather than overwritten.

`CLAUDE_CONFIG_DIR` moves both the settings file it edits and the skills directory it links into,
for anyone whose Claude Code configuration does not sit at `~/.claude`.

## Working on this

```bash
./test.sh               # every suite
```

The installer's suite drives it as a subprocess against a settings directory of its own. The Go
hooks keep their rules in [`source/AGENTS.md`](source/AGENTS.md), and their suites drive each hook's
`Main` against a project tree built in a temporary directory. Every guard is asserted from both
sides: the same call is driven once with the evidence present and once with it missing, and the two
outcomes have to differ.
A guard that cannot fail is worthless.

## Licence

MIT. See [`LICENSE`](LICENSE).
