# Capture: architecture

## The body as one argument

The body reaches `sdd new` as one argument of an argument vector, never through a shell word.
`sdd new "$(cat body.md)"` re-evaluates the string in a second shell, so backticks in a body run as
command substitution and every code identifier vanishes from an entry that can never be edited.

## The run

| Step | Stops the run when |
|---|---|
| header | a field the call needs is missing, a key reaches no `sdd new` flag, or no topic carries the project's `listing_prefix` |
| credentials | the body, a header value as its flag carries it, or a file the header attaches holds what looks like a credential, an attachment reads from standard input, or an attached file cannot be read (see Credentials) |
| sdd | no `sdd` answers `--help`; or the draft or the call asks for what only the fork carries (`title`, `actor_kind`, `--preflight-route`, `--preflight-done`, a schema-2 graph) and the probe finds it missing; or the draft carries `title` and `sdd info --format json` says writes to the graph take none, or cannot tell (a graph newer than sdd, a failed run); or, on an sdd without `info --format`, the draft carries `title` and `.sdd/meta.json` puts the graph on schema 1, which holds no titles |
| lint | `draft_lint` reports on the body, or fails and says nothing; it runs from the repository root on `.<draft>.body.md` beside the draft, its findings shifted to the draft's lines |
| pre-flight | `sdd new --dry-run` refuses, or reports a high finding; a medium finding is shown and captures anyway, a low one is never shown. Where sdd lacks `--preflight-verified`, no dry run: one `sdd new` under the lock checks and writes, a high finding writing nothing, a provider fault retried, a cut-off never retried |
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

## Removing the draft

Success is `sdd new` exiting 0; it writes and commits in one step, and nothing runs after it, so the
draft is deleted only then, and only where `capture_remove_draft` is on. Every symlink in the
draft's path is resolved first. `git rev-parse --show-toplevel`, asked in the working directory,
names the work tree; a draft outside it and outside `/tmp` stays with `draft kept: <path> is outside
the work tree`. `git ls-files -- <draft>`, asked in the draft's directory, then says whether git
tracks it: a tracked draft stays with `draft kept: <path> is tracked by git`, since `git rm` would
touch the index outside the capture's own commit and a plain deletion would be an uncommitted
change another commit can sweep in. `ls-files` fails outside any work tree, which leaves a draft
under `/tmp` untracked; inside the work tree a failure leaves the question open, and the draft stays
without a line. Git runs behind the `capture.Git` interface, so the tests answer it with a fake.

## Credentials

The grounding gate scans a capture before the Bash call runs: the command, the draft as it is on
disk, and the files its header attaches. That scan comes early, before anything runs, but it reads
files the call may change, a draft written by the same command or a file rewritten while the
pre-flight runs. So `agentic-capture` scans again what it hands `sdd new` itself
(`internal/capture/credentials.go`, the shared `internal/secrets` scanner): the body, counted in
the draft's own lines; every header value as its flag carries it, after YAML decoded any escape;
and the bytes of every attached file, read from the directory sdd runs in, by its name in the entry and, where that finds nothing, as nameless text (a path allowlist skips a file by name)
(`source:target` splits on the last colon, as sdd splits it). The scan runs after the header check,
before the lint and the pre-flight, since the pre-flight hands the draft to a model provider; again
right before the write; and, where one `sdd new` checks and writes, right before that call. A
finding exits 1 with `agentic-capture: refused. The capture holds what looks like a credential
(<rule ids>, <where>, line <n>); the graph is shared and immutable`, `<where>` the draft, a header
flag (`<draft>'s --title`) or an attached file. An attachment from standard input (`-:target`)
cannot be scanned, and nothing hands sdd one here: `agentic-capture: refused. The capture attaches
standard input as <name>, which cannot be scanned for a credential; attach a file instead`. A file
that cannot be read refuses too: `agentic-capture: refused. The capture names <file>, which cannot
be read to scan for a credential`. sdd reads an attached file itself once the scan passed, so a
file changed in that last instant escapes both scans. An interrupt arriving during a scan cuts it
short, and a cut-short scan reports a `scan-incomplete` finding; the interrupt wins, exiting 1 with
`agentic-capture: interrupted before sdd ran: <reason>; nothing was written`, never as a credential.
