# agentic-helpers

A coding agent forgets. Its context fills, the session is summarised, and the next one starts from
nothing. Asked about a project's decisions, it answers from whatever it happened to read last. Its
probes and half-finished drafts pile up where nobody reviews them.

agentic-helpers gives Claude Code the habits that counter this, as hooks that run on every session
and a command the agent calls itself:

| Piece | Binary | What it keeps the agent doing |
|---|---|---|
| carry-forward | `agentic-carryforward` | writing down what the next session needs before the context runs out, and reading it back after |
| scratch sweep | `agentic-scratch` | keeping throwaway work in one place per session, and clearing it when the session ends |
| grounding gate | `agentic-grounding-gate` | reading the decision graph for real before asking a question or editing a draft |
| capture | `agentic-capture` | writing a new graph entry through one checked path, never by hand |

**The grounding gate and capture exist for [sdd](https://github.com/networkteam/sdd)**, the
Signal-Dialogue-Decision graph a project keeps under `.sdd/`. sdd motivates them and drives them:
the gate counts real `sdd search` and `sdd view` reads, and capture writes through `sdd new`.

Every piece installs once for the whole machine and stays silent in a project that does not carry
the directory it works on: `.memory/`, `.tmp/` or `.sdd/`. Each comes with a skill, a Markdown file
the agent loads to learn what the piece expects and what its refusals mean. The hooks and the
command are Go binaries built from `source/hooks` into `~/go/bin`.

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

### Capturing an entry

`agentic-capture <draft.md>` writes one sdd entry from a draft file: a YAML block carrying the
fields `sdd new` takes, and the body below it. The body reaches `sdd new` as one argument, never
through a shell, because a second shell runs backticks in a body as commands and every code
identifier vanishes from an entry that can never be edited.

Before it writes, the draft clears three gates in turn: its header names what the call needs (and a
topic carrying the project's `listing_prefix`, where one is set), the project's own lint reports
nothing on the body, and `sdd new --dry-run` reports nothing of high severity. The lint is whatever
the project names as `draft_lint` in `.sdd/grounding.yaml`, a script or `vale --output=line` alike,
and a project naming none gets no lint. The gate refuses a capture whose draft records a gap.

## The skills

A hook fires and an agent that has never met it has to work out what just happened. Each hook has a
skill carrying what it cannot say in a one-line refusal.

| Skill | Carries |
|---|---|
| `agent-carryforward` | what a seat is, how to claim one, what belongs in a carry-forward against what belongs in the graph, and the handoff procedure |
| `agent-grounding` | the three sdd reads that open the gate, how deep to read an entry and why, which reference kind is the sharp one, and what each refusal means |
| `agent-capture` | the draft's shape, what runs before an sdd entry is written, and what each refusal and exit code means |
| `agent-scratch` | where throwaway goes, what the sweep removes, and how to keep something from it |

Each skill is the whole of its rules. A global instruction file names the skill and says to load it;
it never restates what the skill holds, because two copies drift and a reader obeys whichever they
opened.

Each says in its own description that it applies only to a project carrying the directory its hook
needs, so an agent in an unrelated project has no reason to open any of them.

Skills are linked rather than copied, so an edit here reaches every project at once.

## How a project opts in

**No hook does anything until the project carries what it operates on.** Nothing is created,
nothing is asked, and a project carrying none of the directories never hears from any hook.

| Hook | Runs when the project carries | Otherwise |
|---|---|---|
| Carry-forward | a `.memory/` directory | exits 0, silently |
| Grounding gate | a `.sdd/` decision graph, at the project root or above it | exits 0, silently |
| Scratch sweep | a `.tmp/` directory | exits 0, silently |

So opting a project in is one command:

```bash
mkdir .memory          # start keeping a carry-forward here
```

and opting out is removing the directory. A project using none needs no configuration, no
allowlist entry and no marker file, and installing these hooks costs it nothing.

## Installing

```bash
git clone git@github.com:fza/agentic-helpers.git
cd agentic-helpers
go install -C source/hooks ./cmd/...  # build every binary into ~/go/bin
agentic-install               # wire the hooks in, link the skills
agentic-install --print       # write nothing, show what would land
agentic-install --remove      # take both back out
```

`agentic-install` runs from inside this checkout, which it finds by walking up from the working
directory, and wires the hook binaries sitting beside it.

It edits only the hook entries running these binaries and leaves every other key where it found it,
in the order it found it, which matters because that file also carries credentials. A timestamped
copy goes beside it before anything is written. Running it twice changes nothing the second time,
and binaries that moved leave nothing stale behind, because entries are matched by the name they
run rather than by the directory they name.

Claude Code reloads its settings while a session runs, so a running session picks up the new
wiring.

A Go hook runs from `~/go/bin` (or wherever `GOBIN` points), not from this checkout, so a pull
changes nothing until `go install -C source/hooks ./cmd/...` runs again. The installer warns about a
binary it wires that is not there yet.

A skill link naming a directory that moved is replaced. A directory somebody else put there under
one of these names is left alone and reported, rather than overwritten.

`CLAUDE_CONFIG_DIR` moves both the settings file it edits and the skills directory it links into,
for anyone whose Claude Code configuration does not sit at `~/.claude`.

## Working on this

```bash
cd source/hooks && go vet ./... && go test -race ./...   # every suite
```

The rules for this code live in [`source/AGENTS.md`](source/AGENTS.md). Each suite drives its
binary's `Main` against a tree built in a temporary directory, with processes and the settings
directory faked, never the real ones. Every guard is asserted from both
sides: the same call is driven once with the evidence present and once with it missing, and the two
outcomes have to differ.
A guard that cannot fail is worthless.

## Licence

MIT. See [`LICENSE`](LICENSE).
