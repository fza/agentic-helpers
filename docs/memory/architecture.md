# Memory: architecture

## Where it keeps what

```
~/.claude/agentic-memory/<project>/
├── log/tail.jsonl, log/<hash>.seg, log/manifest.json   the append-only log: the truth
├── seats/<seat>.json                                   who holds the seat, which process, which transcript
└── seats/<seat>.slice                                  the last turns of the seat's last session
~/.claude/agentic-memory/sessions/<session>.ctx         the context reading the plugin writes
~/.cache/agentic-memory/<project>/                      vectors, heat, per-session state: rebuilt at will
```

`<project>` is the `repo_id` the project's `.sdd/config.yaml` declares, else `local/` and a hash of
its git common directory, the key sdd gives its own search index, so every worktree of a repository
shares one store. A directory counts as inside git when `git rev-parse --is-inside-work-tree` says
`true`; a `.git` directory and a bare repository do not. Outside git there is no project key; with
`memory_require_git` set to `false`, the store is keyed `local/` and a hash of the directory's
absolute path.

## The log

Events append to `log/tail.jsonl`. The tail seals past 64 KiB into an immutable segment, named for
its content hash, and eight segments merge into one. A merge bounds what history it keeps: every
event older than 14 days, the revive window, folds into the shortest run of existing event kinds
that rebuilds its entry (one `new` row carrying the title, topics and retention as they stood, the
latest `confirm`, and the `retire` or `supersede` still standing), so the log grows with the entries
it holds rather than with every change ever made, and a binary of an earlier release still reads it.
Topic merges and every younger event stay as they are. The same merge drops the body of an entry
superseded more than 14 days earlier, or retired more than `retired_body_days` earlier; the entry
stays in search, flagged, and `revive` refuses it.

## Seats and liveness

A claim file names the holding session, the pid of its `claude` process, that process's start time
(`started`, RFC 3339 in UTC), when it was claimed, and the session's transcript. A call finds its own
seat by walking up the process tree to the nearest `claude` process.

A holder is alive while its pid runs `claude` and that process started at the recorded instant, so a
reused pid never passes. The start time comes from `ps -o lstart=` run under `LC_ALL=C`: `ps` prints
it in the caller's locale otherwise, and callers in two locales would read one process as two. A
claim written by an earlier release may hold `ps` text in another locale, which cannot be read back
into an instant; such a claim is judged by the process alone, and its holder's next claim rewrites
it.

## The context reading

The nudge reads how full the context is from `~/.claude/agentic-memory/sessions/<session>.ctx`,
`{"used_percentage": <n>, "size": <window>, "at": <unix seconds>}`. Claude Code hands that figure to
a status line and to a mod, never to a command hook, so the plugin carries a hooks module,
`plugin/hooks/register.ts`, which writes the file on `session.measure` after every turn wherever
`~/.claude/agentic-memory` exists. A Claude Code too old to load mods writes no reading, and the
nudge falls back to transcript growth.

## Hooks

Every hook exits at once in a project without a store.

| Event | What memory does |
|---|---|
| SessionStart (`session-start`) | for a seated session, points the claim at the session and transcript, and after a compaction or `/clear` prints the brief; for a seatless one, frees the seats whose holders are gone and names the project's seats |
| SessionStart (`session-log <n>`) | after a compaction, prints piece `<n>` of the turns since the seat's newest entry; one hook per piece, since Claude Code inlines a hook's output only up to 10,000 characters and swaps anything longer for a file path |
| UserPromptSubmit (`prompt`) | adds up to three related entries, and the nudge near compaction |
| Stop (`stop`) | blocks with the backstop once the nudge has gone unanswered twice and the context crossed the line |
| SessionEnd (`session-end`) | keeps the session's last turns as its seat's slice |

agentic-memory never reads or writes a project's `.memory/` directory.
