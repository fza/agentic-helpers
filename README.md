# agentic-helpers

A coding agent forgets. Its context fills, the session is summarised, and the next one starts from
nothing. Asked about a project's decisions, it answers from whatever it happened to read last. Its
probes and half-finished drafts pile up where nobody reviews them.

agentic-helpers gives Claude Code the habits that counter this, as hooks that run on every session
and commands the agent calls itself:

| Piece | Binary | What it keeps the agent doing |
|---|---|---|
| memory | `agentic-memory` | filing what it learns as it works, and getting back what a fresh context needs after a compaction or a new session |
| scratch sweep | `agentic-scratch` | keeping throwaway work in one place per session, and clearing it when the session ends |
| grounding gate | `agentic-grounding-gate` | reading the decision graph for real before asking a question, editing a draft or capturing |
| capture | `agentic-capture` | writing a new graph entry through one checked path, never by hand |

**The grounding gate and capture exist for [sdd](https://github.com/fza/sdd)**, the
Signal-Dialogue-Decision graph a project keeps under `.sdd/`.

Every piece installs once for the whole machine and stays silent where it has nothing to work on.
Everything ships as one Claude Code plugin; the hooks and commands are Go binaries built from
`source/hooks`, published with each release and fetched by the plugin.

**Every piece is built and tested with Claude Code only.** The hooks read Claude Code's payloads,
settings and transcripts, and answer in its hook protocol; no other agent harness is tested, and
none is supported.

## Installing

**agentic-helpers works only with sdd built from the fork [github.com/fza/sdd](https://github.com/fza/sdd)**,
not from networkteam/sdd: `agentic-capture` and the grounding gate follow the fork's `sdd`. Install
it first:

```bash
brew install fza/tap/sdd
sdd --version   # sdd version 0.18.0-alpha.2.fza.22, or any version carrying "fza"
```

Without Homebrew, the fork's install script from a pinned release does the same (every fork release
is a prerelease, so `releases/latest` finds none):

```bash
curl -sL https://github.com/fza/sdd/releases/download/v0.18.0-alpha.2.fza.22/install.sh | sh
```

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

## Documentation

How to use each piece and how each works inside: [`docs/index.md`](docs/index.md).

## Licence

MIT. See [`LICENSE`](LICENSE).
