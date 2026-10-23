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
| grounding gate | `agentic-grounding-gate` | reading the decision graph for real before asking a question, editing a draft or capturing |
| capture | `agentic-capture` | writing a new graph entry through one checked path, never by hand |

**The grounding gate and capture exist for [sdd](https://github.com/networkteam/sdd)**, the
Signal-Dialogue-Decision graph a project keeps under `.sdd/`. sdd motivates them and drives them:
the gate counts real `sdd search` and `sdd view` reads, and capture writes through `sdd new`.

Every piece installs once for the whole machine and stays silent in a project that does not carry
the directory it works on: `.memory/`, `.tmp/` or `.sdd/`. Each comes with a skill, a Markdown file
the agent loads to learn what the piece expects and what its refusals mean. Everything ships as one
Claude Code plugin; the hooks and the command are Go binaries built from `source/hooks`, published
with each release and fetched by the plugin.

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

The gate refuses a question, a draft edit or an `agentic-capture` call until the turn carries real
reads: a literal search and a semantic one, because each misses what the other finds, and a
listing, because an empty search result is never evidence of absence. A project naming a topic
prefix in `.sdd/grounding.yaml` (`listing_prefix: area-`) owes a listing of one topic carrying it;
every other project owes the view of every topic, `sdd view --layout "active:as-counts"`. It also
refuses a call that would discard the graph tool's own output, since a swallowed refusal reads
exactly like an empty graph, and a read that stops at the entry itself, since a body is immutable
and a later entry may have renamed every surface it names.

A show owes `--down 2 --up 1` unless `.sdd/grounding.yaml` sets another depth, and a project can
name seats that owe none. A seat is the carry-forward's: the gate matches the session against the
claims under `.memory/roles/`, so a session holding no claim owes the depth. The exemption lifts the
depth alone; every other refusal holds for every seat.

```yaml
show_depth:
  down: 3                    # a direction left out keeps its default; 0 owes nothing
  up: 1
  exempt_seats: [reviewer]
```

A project keeping a graph can still lift the gate. `enabled: false` switches it off for every
session, as if the project carried no graph; left out, the gate is on. Top-level `exempt_seats`
names seats the gate never refuses: a session holding one passes every check, the reads, the
discarded stream and the depth alike. A file that does not parse keeps the gate on and refusing.

```yaml
enabled: false               # left out or true: the gate runs
exempt_seats: [reviewer]     # seats the gate never refuses; show_depth's own lifts the depth alone
```

A project running its reads through a wrapper names it, and the refusals suggest reads the wrapper
accepts. `read_command` replaces `sdd` in every suggested read and counts as a read beside `sdd`
wherever the gate looks for one; `read_suffix` ends every suggested read and is never required. A
project naming neither gets bare `sdd`. Graph writes stay with `sdd new` and `agentic-capture`.

A project whose tooling runs every read in one call names it as `gather_command`, and the refusal
offers that call ahead of the separate reads. A successful call through it counts as a semantic
search when it carries `--query`, a literal one when it carries `--term`, and a listing for each
`--area <name>` carrying the listing prefix; naming no `--area`, it counts as a listing of every area
its `--entry` subject carries. A call missing a mode still owes it. A project naming no
`gather_command` gets nothing credited for a call shaped like one.

```yaml
read_command: python3 scripts/graph.py      # plain words: no quotes, variables or operators
gather_command: python3 scripts/gather.py   # plain words too; absent, no call counts as one
read_suffix: --entry <entry>                # one line, printed only
```

A capture also passes on a grounded draft: one a `Write` or `Edit` saved, as it stands now, in a
turn carrying every read, while the graph holds the same entries (their count and the newest). The
turn confirming a playback then need not repeat the reads the draft turn ran. A draft written
through the shell, changed since, or captured after the graph moved owes the reads in the capture
turn. A capture counts however the shell spells it: after a runner, in a subshell, inside
`bash -c`, under any path. A plain `sdd new` without `--dry-run` owes the reads too, and since it
names no draft, nothing grounded stands in for them. The gate keeps each session's reads and
grounded drafts under `.sdd/tmp/grounding-gate/`, removes a session's files when it ends, and at a
session start removes those a session left a week ago without ending cleanly.

### Capturing an entry

`agentic-capture <draft.md>` writes one sdd entry from a draft file: a YAML block carrying the
fields `sdd new` takes, and the body below it. The body reaches `sdd new` as one argument, never
through a shell, because a second shell runs backticks in a body as commands and every code
identifier vanishes from an entry that can never be edited.

Before it writes, the draft clears three gates in turn: its header names what the call needs (and a
topic carrying the project's `listing_prefix`, where one is set) and carries no key that reaches no
`sdd new` flag, since an entry written without a field never gains it; the project's own lint reports
nothing on the body, and `sdd new --dry-run` reports nothing of high severity. The lint is whatever
the project names as `draft_lint` in `.sdd/grounding.yaml`, a script or `vale --output=line` alike,
and a project naming none gets no lint. The gate refuses a capture whose turn carries neither the
reads nor a grounded draft.

The pre-flight is a model call, so a provider can fail it without the draft being at fault. A try
that answers nothing, is rate-limited, fails with status 408, 409 or 5xx, times out within sdd, or
runs past `--preflight-limit` (default `5m`, or `CAPTURE_PREFLIGHT_LIMIT` for a caller; the flag
wins) runs again after 10 seconds, then after 30, and gives up with exit 1 after the third. A cut-off
try kills sdd's whole process group, so a `claude -p` it started dies with it. A refusal of the
draft never retries: a high finding, a shape error, or any other status. The write itself runs once,
because the summary is its only model call and sdd writes the entry without one when it fails; a
failure there comes from the file or git, after the entry may already be on disk.

A caller with more than one provider names fallback routes for the pre-flight, each with
`--preflight-route <model>=<layer>`: the model sdd gets as `--model`, and a configuration layer it
gets as `--local-config`, as many as needed, tried in the order given. Once the caller's own
configuration has failed all three tries on a provider fault, each route gets one try, with no wait,
and the first one answering without a fault decides the pre-flight: its findings hold the draft back
or let it through just as the primary's would. A refusal never moves on to a route, and the write
itself always runs on the caller's own configuration. With routes named, stdout opens with
`AGENTIC_CAPTURE_ROUTE <model>` (or `primary`) for the route that answered; where none did, each
route's last error stands on its own line before the failure. A layer holds an API key, so one that
is missing, unreadable, owned by someone else, or readable by group or others is skipped with a line
saying so, and its contents are never read. A caller naming no route sees none of this, and
`--help` lists the flag for a caller detecting support.

A caller that already ran the pre-flight on a draft passes `--preflight-done <sha256>`: the sha256
of the draft file's bytes as read from disk, front matter included, as `sha256sum <draft.md>` prints
it. Where the draft hashes to that value, the header check and the lint still run, the pre-flight is
skipped with `pre-flight skipped: the caller ran it on these exact bytes`, and the entry is recorded
as verified, never as unchecked. Where it hashes to anything else, one line names both values and the
pre-flight runs as usual, so the caller learns its key differs. The flag is refused beside
`--skip-preflight`. `--help` prints the usage line and one line per flag on stdout, the
`--preflight-done` line naming the bytes the hash covers.

Exit 0 is captured, 2 is a high finding holding the draft back, 1 everything else.

## The skills

A hook fires and an agent that has never met it has to work out what just happened. Each hook has a
skill carrying what it cannot say in a one-line refusal.

| Skill | Carries |
|---|---|
| `agentic-helpers:agent-carryforward` | what a seat is, how to claim one, what belongs in a carry-forward against what belongs in the graph, and the handoff procedure |
| `agentic-helpers:agent-grounding` | the three sdd reads that open the gate, how deep to read an entry and why, which reference kind is the sharp one, and what each refusal means |
| `agentic-helpers:agent-capture` | the draft's shape, what runs before an sdd entry is written, and what each refusal and exit code means |
| `agentic-helpers:agent-scratch` | where throwaway goes, what the sweep removes, and how to keep something from it |

Each skill is the whole of its rules. A global instruction file names the skill and says to load it;
it never restates what the skill holds, because two copies drift and a reader obeys whichever they
opened.

Each says in its own description that it applies only to a project carrying the directory its hook
needs, so an agent in an unrelated project has no reason to open any of them.

The plugin carries the skills, so Claude Code names each after it (`agentic-helpers:agent-capture`), and
a plugin update brings every skill's new text with it.

## How a project opts in

**No hook does anything until the project carries what it operates on.** Nothing is created,
nothing is asked, and a project carrying none of the directories never hears from any hook.

| Hook | Runs when the project carries | Otherwise |
|---|---|---|
| Carry-forward | a `.memory/` directory | exits 0, silently |
| Grounding gate | a `.sdd/` decision graph, at the project root or above it, without `enabled: false` in its `grounding.yaml` | exits 0, silently |
| Scratch sweep | a `.tmp/` directory | exits 0, silently |

So opting a project in is one command:

```bash
mkdir .memory          # start keeping a carry-forward here
```

and opting out is removing the directory. A project using none needs no configuration, no
allowlist entry and no marker file, and installing these hooks costs it nothing.

## Installing

```bash
claude plugin marketplace add fza/agentic-helpers
claude plugin install agentic-helpers@agentic-helpers
```

Both work inside a session as `/plugin ...` too. Installed at user scope, the plugin is enabled in
every project on the machine, and Claude Code itself records that in its settings: no installer of
ours edits `~/.claude/settings.json`. `claude plugin update agentic-helpers@agentic-helpers` moves to
the latest release; it takes effect in the next session or after `/reload-plugins`. Auto-update is
off by default for a marketplace like this one, and can be turned on under `/plugin`, Marketplaces.

### Where the binaries come from

The plugin holds no binaries. Every hook runs through `hooks/run`, which runs the binary of the
release named by the plugin's own `version`, kept under the plugin's data directory
(`~/.claude/plugins/data/agentic-helpers-agentic-helpers/<version>/`):

1. `AGENTIC_HELPERS_BIN_DIR` set: the binary in that directory runs, and nothing is fetched.
2. The plugin's version is there: it runs.
3. At SessionStart, and only then, the release archive for this machine and the release's
   `checksums.txt` are downloaded; the archive is unpacked only where its sha256 matches.
4. Otherwise the newest version already there runs. A session that cannot write the data directory,
   such as a sandboxed job, runs what an earlier session fetched.
5. No binary at all: the grounding gate refuses every call it guards, with a message naming the fix,
   rather than letting work run ungrounded, and every other hook does nothing. The SessionStart that
   failed to fetch says why, once.

Claude Code's Bash tool finds `agentic-capture` and `agentic-carryforward` through the plugin's
`bin/`, which hands each call to the same launcher, so `agentic-capture --help` is answered by the
real binary. A script outside Claude Code has no plugin on its PATH and builds the binaries itself:
`go install -C source/hooks ./cmd/...` from a checkout.

### Coming from `agentic-install`

A machine wired by the former `agentic-install` runs every hook twice once the plugin is enabled.
Before installing the plugin, run `agentic-install --remove` from the checkout it was built in; it
takes its hook entries out of the settings file and its skill links out of the skills directory, and
leaves everything else as it was.

### Working on the plugin

Add this checkout as the marketplace, so the plugin loads from it in place and an edit to a skill or
to `hooks/` reaches the next session or `/reload-plugins`:

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

### Releasing

A release is cut by starting the `release` workflow by hand (`gh workflow run release.yml`, or the
Actions tab). It runs the tests, then semantic-release reads the commits since the last tag: `fix:`
cuts a patch, `feat:` a minor, a `!` after the type a major, and every other type, `sdd:` included,
cuts nothing. A releasable run writes the new version into `.claude-plugin/plugin.json`, commits it
to `main` as `chore(release): v<version> [skip ci]`, tags that commit, and GoReleaser publishes an
archive per platform (darwin and linux, arm64 and amd64) with `checksums.txt` and the generated
notes. A breaking change marked without `!` ships as a minor. Pull before pushing after a release:
`main` carries the release commit.

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
