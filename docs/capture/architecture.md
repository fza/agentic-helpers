# Capture: architecture

## The body as one argument

The body reaches `sdd new` as one argument of an argument vector, never through a shell word.
`sdd new "$(cat body.md)"` re-evaluates the string in a second shell, so backticks in a body run as
command substitution and every code identifier vanishes from an entry that can never be edited.

## The run

| Step | Stops the run when |
|---|---|
| header | a field the call needs is missing, a key reaches no `sdd new` flag, or no topic carries the project's `listing_prefix` |
| sdd fork | `sdd --version` carries no `fza` |
| lint | `draft_lint` reports on the body, or fails and says nothing; it runs from the repository root on `.<draft>.body.md` beside the draft, its findings shifted to the draft's lines |
| pre-flight | `sdd new --dry-run` refuses, or reports a high finding; a medium finding is shown and captures anyway, a low one is never shown |
| capture | `sdd new --preflight-verified`, run once under `.sdd/tmp/capture.lock` of the project's `.sdd/` (the graph repository's, where `project_dir` names one), an exclusive `flock` serialising captures that share a graph |

## The pre-flight's provider

Each try runs under its own time limit, and a cut-off try kills sdd's whole process group, so a
`claude -p` it started dies with it. A provider fault waits 10 seconds, then 30, and gives up after
the third try; the fallback routes then get one try each. The faults told apart from a refusal are
an empty answer, a rate limit, status 408, 409 or 5xx, and a timeout; a refusal is checked first, so
a high finding quoting "timed out" is still a refusal.

## The write

The write runs once. Its only model call is the summary, and sdd writes the entry without one when
that call fails, so a failure of the write itself comes from the file or git, after the entry may
already be on disk.
