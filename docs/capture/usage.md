# Capture: usage

`agentic-capture <draft.md>` writes one sdd entry from a draft file: a YAML block carrying the
fields `sdd new` takes, and the body below it. It runs only in a project carrying `.sdd/`, with stock
sdd or with the fork at github.com/fza/sdd, and uses what the fork adds wherever the installed `sdd`
lists it in its `--help`.

```bash
agentic-capture <draft.md>                                    # lint, pre-flight, capture
agentic-capture <draft.md> --force                            # capture despite a high finding: the graph owner's call
agentic-capture <draft.md> --skip-preflight                   # no pre-flight; the entry records that nothing checked it
agentic-capture <draft.md> --preflight-limit 8m               # give each pre-flight try's model call 8m
agentic-capture <draft.md> --preflight-route <model>=<layer>  # a fallback for the pre-flight, repeatable
agentic-capture <draft.md> --preflight-done <sha256>          # the caller ran the pre-flight on these bytes
agentic-capture --help                                        # usage and one line per flag
```

## Engine first

Where the project serves sdd's engine, an entry is captured through it, not through
`agentic-capture`: `mcp__sdd__start_procedure` with `canonical: capture`, driven by sdd's `sdd`
skill. The engine plays the exact entry back for the owner's confirmation, runs a writing-guide
review and checks the generated summary; `agentic-capture` does none of these. The grounding gate
refuses `agentic-capture` in a project whose `.mcp.json` declares `mcpServers.sdd`, naming the
engine's capture instead. A project that captures through `agentic-capture` on purpose beside an sdd
server, a pipeline or a scripted capture, sets `cli_capture: true` in `.sdd/grounding.yaml`. An sdd
server configured at user or local scope, outside `.mcp.json`, goes unseen.

## What a draft must clear

Before it writes, the draft clears four checks in turn: its header names what the call needs
(`type`, `layer`, `confidence`, and a topic carrying the project's `listing_prefix`, where one is
set) and carries no key that reaches no `sdd new` flag, since an entry written without a field never gains it; nothing it hands sdd, body,
header values and attached files alike, looks like a credential ([architecture](architecture.md#credentials)),
since an entry is never edited and every session reads it; the project's own lint reports
nothing on the body; and `sdd new --dry-run` reports nothing of high severity. The lint is whatever
the project names as `draft_lint` in `.sdd/grounding.yaml`, a script or `vale --output=line` alike,
and a project naming none gets no lint. In sdd's split layout, where the code repository's
`.sdd/config.yaml` names the graph repository as `project_dir`, `grounding.yaml` is the graph
repository's; sdd itself still runs from the code repository, which resolves its graph repository. The grounding gate refuses a capture whose turn carries
neither the reads nor a grounded draft.

A graph on an older schema than the installed sdd refuses every write, the pre-flight included. The
capture then says ``the graph needs `sdd migrate` before sdd writes to it; the draft did not cause
this``, prints sdd's reason and exits 1, since no edit to the draft helps; `sdd migrate` is the
graph owner's to run. Stock sdd has no `sdd migrate`: there the capture says ``sdd refuses to write
this graph; the draft did not cause this`` and prints sdd's reason.

A schema-1 graph holds no entry titles, and sdd refuses an explicit `--title` on one, its dry run
included, so a draft header carrying `title` is checked before anything runs. Where `sdd info --help`
lists `--format`, the capture asks `sdd info --format json`, whose `capabilities.titles` states what
writes to this graph take:

- `false` → refused: ``the graph is on schema 1, which has no entry titles; drop the draft's `title`
  key, or run `sdd migrate` to upgrade the graph`` (the number is the graph's schema);
- `null`, a graph on a schema newer than the installed sdd → refused: ``the graph is on a schema
  newer than this sdd writes, so whether it takes entry titles is unknown; install the newer sdd with
  `brew install fza/tap/sdd` ``;
- a run that fails or prints no JSON → refused: `whether the graph takes entry titles is unknown: `
  and what sdd printed.

An sdd without `info --format` takes the older path: a graph whose `.sdd/meta.json` holds
`graph_schema_version: 1` is refused with the schema-1 message above, and a graph whose `meta.json`
names no schema is on the installed sdd's own, so the check leaves it to sdd. A draft without `title`
runs no `sdd info`.

## On stock sdd

Stock sdd has no `--preflight-verified`, so no dry run comes first: one `sdd new`, under the capture
lock, runs the pre-flight and writes. A high finding writes nothing and exits 2; a provider fault is
retried as below, since a failed check wrote nothing; a call cut off by `--preflight-limit` is never
retried, since it may have written, and the capture says to check `git log`. `--force` past a high
finding writes the entry again with `--skip-preflight`, so it records that nothing checked it.

What only the fork writes is refused before anything runs, naming the fork: a draft header carrying
`title` or `actor_kind`, `--preflight-route` (the fork's `--local-config`), `--preflight-done` (the
fork's `--preflight-verified`), and a graph whose `.sdd/meta.json` holds `graph_schema_version: 2`.

## When the pre-flight's provider fails

The pre-flight is a model call, so a provider can fail it without the draft being at fault. A try
that answers nothing, is rate-limited, fails with status 408, 409 or 5xx, times out within sdd, or
runs past its cut-off runs again after 10 seconds, then after 30, and gives up with exit 1 after the
third. A refusal of the draft never retries: a high finding, a shape error, or any other status.
Giving up after timeouts adds `a long draft may need more than <limit>; run again with a longer
--preflight-limit`, since a long draft takes the model longer and the same limit fails the same way.

The limit is the model call's bound per try: `--preflight-limit`, else `CAPTURE_PREFLIGHT_LIMIT` (the
flag wins), else the larger of 5m and sdd's configured `llm.timeout` (`sdd config get llm.timeout`),
so an owner's higher setting is never cut down. It reaches sdd as `sdd new --preflight-timeout
<limit>` wherever `sdd new --help` lists the flag, stock and fork alike. That flag does not bound the
extraction sdd falls back to (`llm.extract_timeout`, 1m unless configured), so a try is cut off only
after the limit, the extraction's bound and 30 seconds more, and sdd's own bound fires first. An sdd
without the flag keeps its own `llm.timeout`, and the limit alone cuts the try off.

A capture through sdd's MCP server has no such flag: only `llm.timeout` reaches its model calls
(writing guide, pre-flight, summary). Where it fails with `claude -p timed out` on a long entry,
raising it (`sdd config set --local llm.timeout 5m`) is the owner's call.

A caller with more than one provider names fallback routes, each with
`--preflight-route <model>=<layer>`: the model sdd gets as `--model`, and a configuration layer it
gets as `--local-config`, tried in the order given. Once the caller's own configuration has failed
all three tries on a provider fault, each route gets one try, with no wait, and the first one
answering without a fault decides the pre-flight: its findings hold the draft back or let it through
just as the primary's would. A refusal never moves on to a route, and the write itself always runs on
the caller's own configuration. With routes named, stdout opens with `AGENTIC_CAPTURE_ROUTE <model>`
(or `primary`) for the route that answered; where none did, each route's last error stands on its own
line before the failure. A layer that is missing, unreadable, owned by someone else, or readable by
group or others is skipped with a line saying so.

## A pre-flight already run

A caller that already ran the pre-flight on a draft passes `--preflight-done <sha256>`: the sha256
of the draft file's bytes as read from disk, front matter included, as `sha256sum <draft.md>` prints
it. Where the draft hashes to that value, the header check and the lint still run, the pre-flight is
skipped with `pre-flight skipped: the caller ran it on these exact bytes`, and the entry is recorded
as verified, never as unchecked. Where it hashes to anything else, one line names both values and the
pre-flight runs as usual. The flag is refused beside `--skip-preflight`.

## Exit codes

Exit 0 is captured, 2 is a high finding holding the draft back, 1 everything else.

## The draft after a capture

Once `sdd new` exits 0, the entry is written and committed, and `agentic-capture` deletes the draft,
saying `draft removed: <path>` on stderr. A deleted draft cannot be recovered; a summary that drifts
is fixed with `sdd summarize <id> --text`, which never needs it. These drafts stay, with a line saying
why:

- `draft kept: <path> is tracked by git`: deleting it would leave a git change nobody asked for,
  which another commit could sweep in.
- `draft kept: <path> is outside the work tree`: it lies outside both the work tree the capture runs
  in and `/tmp`, the places shell-guard lets an agent remove files, so it may belong to something
  other than the capture.
- `draft kept: <path> is not a regular file`: the path is a symlink or no plain file, and the capture
  never deletes through a link; neither the link nor its target is touched.
- `draft kept: <path> changed while it was checked`: another file took the path while git was asked
  about it, so the file there now was never judged.

A capture that fails, at the lint, the pre-flight, `sdd new` or the time limit, keeps the draft and
says nothing about it: it is the copy to fix and run again. So does a captured draft where git cannot
say whether it tracks it, or whose deletion fails.

The plugin option `capture_remove_draft` switches the deletion for the machine, on unless set. Off,
every draft stays and no line prints.

## Off on the machine

The plugin option `capture_enabled` switches capture on or off for the machine, on unless set. Off, `agentic-capture` exits 1 with `agentic-capture: off on this machine (plugin option capture_enabled)`; with `grounding_gate_enabled` off it exits 1 with `agentic-capture: off while grounding gate is off (plugin option grounding_gate_enabled)`, since a draft would land ungrounded.
