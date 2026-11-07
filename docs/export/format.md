# The `agent-memory-export` archive, version 1

The user owns what their agents filed. An export puts all of it into one file the user can read,
keep, and move to any tool they choose: every memory entry with its whole history, every seat and
shelf, every state ledger, the last turns of each seat, every attached file, and the tool library.
The format names no tool: any program may write it or read it, and the manifest says which program
wrote a given archive.

`agentic-helpers export <file>` writes one, and `agentic-helpers uninstall` writes one before it
removes anything. This page defines the archive alone.

## Layout

One `tar.gz` holds the whole machine: every store in the data root, the unclaimed ones included.

```
manifest.json                                  what the archive is, who wrote it, which stores it holds
stores/<store id>/identity.json                the store's checkouts and identifiers
stores/<store id>/seats.jsonl                  one line per seat
stores/<store id>/shelves.jsonl                one line per shelf
stores/<store id>/entries.jsonl                one line per memory entry, any status, with its events
stores/<store id>/names.jsonl                  names no entry of the archive carries any more
stores/<store id>/topic-merges.jsonl           one line per topic merge
stores/<store id>/state.jsonl                  one line per state ledger line, and each seat's old free-text state
stores/<store id>/slices/<seat id>.txt         the last turns of the seat's last session
stores/<store id>/tools/<tool>/meta.json       the tool's current version, or the tool it was merged into
stores/<store id>/tools/<tool>/history.jsonl   one line per version written
stores/<store id>/tools/<tool>/takes.jsonl     one line per copy taken
stores/<store id>/tools/<tool>/pruned.jsonl    one line per version pruned to make room
stores/<store id>/tools/<tool>/v<n>/tool.json  the version's description
stores/<store id>/tools/<tool>/v<n>/<path>     the version's files, byte for byte
attachments/<sha256>                           every attached file, once, uncompressed
```

| Choice | Why |
|---|---|
| One folder per store | Ids, names and seats are unique within a store only. Two stores may both hold a seat named `main`. |
| `slices/<seat id>.txt`, not JSON Lines | A slice is one text block the producer wrote for an agent to read, not a run of records. Splitting it would invent a structure it does not have. |
| `names.jsonl` | A name, once given, never goes to another entry, even after its entry is purged. The file keeps those names so an importer can keep that promise. |
| `topic-merges.jsonl` | A topic merge belongs to the store, not to an entry, and later topic names resolve through it. |
| The tool library under its store | Each project keeps its own library. A tool name is unique within a library only. |
| `meta.json` beside `v<n>/` | A version folder matches its stored layout exactly, `tool.json` included. The tool's own state needs a file outside every version. |
| `attachments/` at the root | One file attached in several projects is stored once on the machine and once in the archive. |

A file that would hold no line may be left out. A reader treats a missing file as empty. A store
without a memory component has no memory files, and a store without a tool library has no `tools/`.

## `manifest.json`

One JSON object.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `format` | string | yes | always `agent-memory-export` |
| `version` | integer | yes | `1` for this page |
| `exported` | string (time) | yes | when the archive was written |
| `producer` | string | yes | the program that wrote it and its version, as that program names itself |
| `stores` | array of objects | yes | one object per store in the archive, in archive order |
| `stores[].id` | string | yes | the store id, the name of its folder under `stores/` |
| `stores[].name` | string | yes | how the store reads to a person: its first `repo_id`, else its first origin, else the suffix of its id |
| `stores[].unclaimed` | boolean | no | `true` where the store has lost every checkout it knew |
| `stores[].formats` | object | no | the format each component of the archive's records was read at, by component (`memory`, `tools`) |
| `stores[].migrated_from` | object | no | by component, the older format the store held where the producer read a migrated copy of it; the store itself kept that format |
| `stores[].counts` | object | no | lines per file, by file stem (`entries`, `seats`, `shelves`, `state`, `names`, `topic-merges`), and `slices`, `tools` and `tool_versions` |
| `attachments` | object | no | `files`, the members under `attachments/`, and `bytes`, their total size |

```json
{"format":"agent-memory-export","version":1,"exported":"2026-11-07T14:02:11Z","producer":"agentic-helpers 2.28.0","stores":[{"id":"0192f3a4-5b6c-7d8e-9f01-23456789abcd-agentic-helpers","name":"agentic-helpers","formats":{"memory":11,"tools":3},"counts":{"entries":412,"seats":9,"shelves":7,"state":58,"slices":6,"tools":14,"tool_versions":31}}],"attachments":{"files":3,"bytes":48211}}
```

## `stores/<store id>/identity.json`

One JSON object: the store's `identity.json`, every field as stored.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | the store id: a UUID v7, then a suffix naming the project |
| `created` | string (time) | yes | when the store was created |
| `checkouts` | array of objects | yes | the checkouts admitted to the store |
| `checkouts[].path` | string | yes | the checkout's directory, absolute |
| `checkouts[].common_dir` | string | no | its git common directory |
| `checkouts[].volume` | string | no | the UUID of the volume holding it |
| `checkouts[].inode` | integer | no | the inode of its git common directory on that volume |
| `repo_ids` | array of strings | no | the sdd `repo_id`s that matched the store |
| `origins` | array of strings | no | the git remote URLs that matched the store |
| `legacy_keys` | array of strings | no | keys an earlier layout kept the store under |
| `kept_apart` | array of strings | no | git common directories of graph repositories the store stays apart from |
| `merged` | array of strings | no | ids of stores joined into this one |
| `unclaimed` | string (time) | no | when the store lost every checkout it knew |

```json
{"id":"0192f3a4-5b6c-7d8e-9f01-23456789abcd-agentic-helpers","created":"2026-03-02T09:14:55.120934Z","checkouts":[{"path":"/Users/ana/src/agentic-helpers","common_dir":"/Users/ana/src/agentic-helpers/.git","volume":"5C1E7A2B-0D44-4E0B-9C71-2B1F3E8A9D10","inode":40711552}],"repo_ids":["agentic-helpers"],"origins":["git@github.com:ana/agentic-helpers.git"]}
```

## `seats.jsonl`

One line per seat the store's registry holds, expired seats included.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | the seat id, `s-` and 6 characters of `0-9a-z`; every record of the store names a seat by it |
| `name` | string | yes | the seat's current name |
| `shelf` | string | no | the id of the shelf it files onto |
| `expired` | boolean | no | `true` where the seat was purged with its temporary shelf and is kept only because a surviving record names it; its name is free |
| `private` | boolean | no | `true` where the seat is listed by its own project alone |
| `last_claim` | object | no | the newest claim of the seat, open or closed |
| `last_claim.session_id` | string | no | the session that held it |
| `last_claim.claimed` | string (as stored) | no | when it was claimed; `until_hold.claimed` values compare against this string |
| `last_claim.closed` | string (time) | no | when it closed; absent while it is held |
| `last_claim.commit` | string | no | HEAD when it was claimed |

```json
{"id":"s-4f8k2p","name":"main","shelf":"h-9d2m7a","last_claim":{"session_id":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","claimed":"2026-11-07T08:01:12Z","closed":"2026-11-07T13:55:40Z","commit":"8ac7f98e"}}
```

## `shelves.jsonl`

One line per shelf.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | the shelf id, `h-` and 6 characters of `0-9a-z` |
| `name` | string | yes | the shelf's name |
| `follows` | array of strings | no | the topics its seats follow |
| `lifetime` | string | no | `temporary`, or a span such as `2d` or `0`; absent where the shelf is durable |

```json
{"id":"h-9d2m7a","name":"main","follows":["hooks","release"]}
```

## `entries.jsonl`

One line per memory entry the store holds, live, retired and superseded alike. Each line carries
the entry as it stands (the folded fields) and every event the store still records for it (the
`events` array). The two agree: replaying `events` in order gives the folded fields, except
`body`, which only the folded record carries, since no event changes it.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | the entry id, `m-` and 4 to 16 characters of `a-z0-9` |
| `name` | string | no | the name the store's name index gives it |
| `title` | string | yes | the current title |
| `body` | string | yes | the body as filed; empty where `dropped` |
| `dropped` | boolean | no | `true` where the store dropped the body after a retire or supersede, as its lifetime rules say |
| `kind` | string | yes | `rule`, `trap`, `todo` or `note`, as it stands now |
| `from` | string | no | who the entry comes from: `user`, `agent` or `tool` |
| `retention` | string | yes | the decay in force: `standing`, `long`, `normal`, `short` or `scratch` |
| `retention_override` | string | no | the decay a `retain` set, where it differs from the one the kind sets |
| `seat` | string | yes | the id of the seat that filed it |
| `shelf` | string | no | the id of that seat's shelf: the shelf the entry belongs to |
| `session` | string | no | the session that filed it |
| `commit` | string | no | HEAD at the latest confirm, else at filing |
| `branch` | string | no | the branch at the latest confirm, else at filing |
| `anchors` | array of objects | no | the files the entry is about, as the latest confirm or the filing left them |
| `anchors[].path` | string | yes | the path, relative to the work tree's root |
| `anchors[].hash` | string | yes | the file's content hash then: the first 16 hex digits of its SHA-256, empty where no file was there |
| `filed` | string (time) | yes | when it was filed |
| `changed` | string (time) | yes | its newest event, or `filed` where it has none |
| `confirmed` | string (time) | no | its latest confirm |
| `status` | string | yes | `live`, `retired` or `superseded` |
| `status_at` | string (time) | no | when the standing retire or supersede happened; absent while live |
| `status_seat` | string | no | the seat that made it |
| `status_reason` | string | no | its reason, as written |
| `superseded_by` | string | no | the id of the successor, where a supersede named one |
| `ended_by` | string | no | the declared end that retired it, where `status_reason` is `end reached: <condition>` |
| `revived` | string (time) | no | its latest revive |
| `topics` | array of strings | yes | its topics now, every name resolved through the store's topic merges |
| `for` | string or array of strings | no | who besides its filer it was filed for: `all`, or shelf ids; an `s-` id among them stands for that seat's shelf |
| `links` | array of objects | no | every link it holds now |
| `links[].target` | string | yes | a memory id (`m-...`), a todo item (`m-...#<item>`), a graph entry id, a connected graph's `<repo_id>:<id>`, `tool:<name>`, or `tool:<name>@<store id>` for another store's tool |
| `links[].kind` | string | yes | `memory`, `graph` or `tool`, by the target's shape |
| `links[].via` | string | yes | `text` where the title or body named it at filing, `explicit` where a filing flag or a `link` made it |
| `links[].seat` | string | no | the seat that made an explicit link |
| `links[].at` | string (time) | yes | when the link was made |
| `until` | array of strings | no | the conditions that end it, any one of them: `closed:<graph id>`, `merged:<branch>[@<into>]`, `changed:<path>`, `gone:<path>`, `claim`, `session`, `at:<time>`, `captured` |
| `until_hashes` | object | no | per `changed:` path, the file's content hash at filing, empty where none was there |
| `until_hold` | object | no | for `session`, the claim it watches: `seat` (a seat id) and `claimed` (that claim's stamp, as stored) |
| `due` | string (time) | no | when it comes due |
| `when` | array of strings | no | a trap's tool-call triggers: `path:<glob>`, `command:<RE2>` or `tool:<name>` |
| `on` | array of strings | no | the moments a rule is owed at: `claim`, `compact` |
| `proof` | array of strings | no | the tool calls that settle a rule owed at a moment, in the trigger forms of `when` |
| `about` | array of strings | no | the paths the entry is about, relative to the work tree's root |
| `forbids` | array of strings | no | a user rule's RE2 patterns no later entry should match |
| `attachments` | array of objects | no | the files attached to it |
| `attachments[].name` | string | yes | the file's base name |
| `attachments[].sha256` | string | yes | the SHA-256 of its bytes, lower-case hex: its member under `attachments/` |
| `attachments[].size` | integer | yes | its size in bytes |
| `attachments[].stored` | boolean | yes | `false` where the machine held no bytes matching its hash and size, so the archive holds none |
| `attachments[].seat` | string | yes | the seat that attached it |
| `attachments[].at` | string (time) | yes | when it was attached |
| `items` | array of objects | no | its todolist, in its current order, removed items included; a todo carries one, and an entry rekinded from a todo keeps it |
| `items[].id` | string | yes | the item's id, unique within the entry; its address is `<entry id>#<item id>` |
| `items[].text` | string | no | the item's text |
| `items[].link` | string | no | the entry, item or graph id the item stands for |
| `items[].state` | string | yes | `open`, `done`, `dropped` or `removed`, as the log records it |
| `items[].set` | boolean | yes | `true` where an `ack` gave the state; `false` where none did, so a linked item's state follows its target |
| `items[].reason` | string | no | why it was dropped |
| `items[].from` | string | no | the `<entry id>#<item id>` it was carried from |
| `items[].added` | string (time) | yes | when it joined this entry |
| `items[].state_at` | string (time) | no | the item's newest change of state |
| `items_changed` | string (time) | no | the newest item change of any kind, order included |
| `events` | array of objects | yes | every event the store records for the entry, in log order, its filing first |

A linked item's state, a link's flags, and whether an anchor or declared end still holds are worked
out by whoever reads them, from the targets and the files, and are not recorded.

```json
{"id":"m-k3x9q2","name":"gate-fix","title":"gate fix: the MCP path records no reads","body":"The Bash path records reads; the MCP path does not yet.","kind":"todo","from":"agent","retention":"short","seat":"s-4f8k2p","shelf":"h-9d2m7a","session":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","commit":"8ac7f98e","branch":"main","filed":"2026-11-05T10:12:03.418Z","changed":"2026-11-06T16:40:22.007Z","status":"superseded","status_at":"2026-11-06T16:40:22.007Z","status_seat":"s-4f8k2p","superseded_by":"m-p7d2w1","topics":["grounding"],"links":[{"target":"20261030-142233-d-tac-a9x","kind":"graph","via":"explicit","seat":"s-4f8k2p","at":"2026-11-05T10:12:03.418Z"}],"items":[{"id":"a1","text":"record reads on the MCP path","state":"done","set":true,"added":"2026-11-05T10:12:03.418Z","state_at":"2026-11-06T09:02:51Z"}],"items_changed":"2026-11-06T09:02:51Z","events":[{"kind":"new","at":"2026-11-05T10:12:03.418Z","seat":"s-4f8k2p","data":{"title":"gate fix: the MCP path records no reads","topics":["grounding"],"kind":"todo","from":"agent","session":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","commit":"8ac7f98e","branch":"main","links":["20261030-142233-d-tac-a9x"]}},{"kind":"item","at":"2026-11-05T10:12:03.418Z","seat":"s-4f8k2p","data":{"op":"add","id":"a1","text":"record reads on the MCP path"}},{"kind":"item","at":"2026-11-06T09:02:51Z","seat":"s-4f8k2p","data":{"op":"check","id":"a1"}},{"kind":"supersede","at":"2026-11-06T16:40:22.007Z","seat":"s-4f8k2p","target":"m-p7d2w1","data":{"by":"m-p7d2w1"}}]}
```

### Events

| Field | Type | Required | Meaning |
|---|---|---|---|
| `kind` | string | yes | the event kind, below |
| `at` | string (time) | yes | when it happened |
| `seat` | string | yes | the id of the seat that made it |
| `target` | string | no | a `link` or `unlink`'s target, a `supersede`'s successor |
| `data` | object | yes | the event's fields as the store records them, below; `{}` where it has none |

| `kind` | `data` fields | Changes |
|---|---|---|
| `new` | `title`, `topics`, `kind`, `retention` (an override), `from`, `session`, `commit`, `branch`, `anchors`, `dropped`, `for`, `text_links`, `links`, `until`, `until_hashes`, `until_hold`, `when`, `on`, `proof`, `about`, `forbids`, `due`: the values at filing, in the form of the folded field of the same name, except `retention`, the override alone, and `text_links` and `links`, arrays of the target ids the text named and the filing linked; no `body` | files the entry |
| `retire` | `because` | status to `retired` |
| `supersede` | `by`, `because` | status to `superseded` |
| `revive` | `until_hold` | undoes the newest standing retire or supersede; the status falls back to the one before it, else `live`; a `until_hold` replaces the entry's |
| `confirm` | `anchors`, `commit`, `branch` | replaces the anchors, commit and branch; sets `confirmed` |
| `retain` | `level` | sets the retention override |
| `retitle` | `title` | sets the title |
| `tag` | `topics` | adds topics, as written |
| `untag` | `topics` | removes topics, as written |
| `link` | none | adds an explicit link to `target` |
| `unlink` | none | removes the explicit link to `target` |
| `due` | `due` | sets the due time; clears it where `due` is absent |
| `rekind` | `kind`, `line` | sets the kind; where `line` names a state line, the entry's `until`, `until_hashes`, `until_hold` and `due` moved to that line and are cleared |
| `attach` | `name`, `sha256`, `size` | attaches a file |
| `item` | `op` (`add`, `check`, `uncheck`, `drop`, `remove`, `order`), `id`, `text`, `link`, `state`, `reason`, `from`, `order` | changes the todolist: `add` appends an item (with `state`, `reason` and `from` where carried), `check` sets `done`, `uncheck` sets `open`, `drop` sets `dropped` with `reason`, `remove` sets `removed`, `order` moves the items `order` names first, in that order |
| `settle` | `how` (`proof`, `ack`, `decline`), `proof`, `because`, `session` | a rule owed at a moment was done, by the named tool call or an ack, or declined with a reason; changes no field |

The store folds an entry's events older than its revive window, 14 days, into its `new` event, its
latest `confirm` and the retires and supersedes still standing, and drops a link undone by an
unlink. `events` holds what the store still records. Item events are never folded.

## `names.jsonl`

One line per name the store's name index holds for an id no line of `entries.jsonl` carries.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes | the id the name was given to |
| `name` | string | yes | the name, never to be given to another entry |

```json
{"id":"m-q2v8nz","name":"cache-sweep"}
```

## `topic-merges.jsonl`

One line per topic merge, in log order. A topic name reads as the name its merges lead to, each
merge applied in order.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `from` | string | yes | the topic merged away, trimmed and lower-cased |
| `into` | string | yes | the topic it now reads as, trimmed and lower-cased |
| `seat` | string | yes | the seat that merged them |
| `at` | string (time) | yes | when |

```json
{"from":"gate","into":"grounding","seat":"s-4f8k2p","at":"2026-10-12T08:44:09.5Z"}
```

## `state.jsonl`

Each seat's state ledger, one line per ledger line (`"record":"line"`), and the free-text state a
seat wrote before its ledger, where the store still holds one (`"record":"old"`).

`"record":"line"`:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `record` | string | yes | `line` |
| `seat` | string | yes | the seat whose ledger holds it |
| `id` | string | yes | the line's id, unique within the seat's ledger; its address is `state#<id>` |
| `text` | string | yes | the line's text, as it stands |
| `link` | string | no | the target it pins: a memory id, a todo item, or a graph id |
| `hook` | string | no | the hook that wrote it, `landed`, `captured`, `dirty` or `asked`; absent where the agent did |
| `commit` | string | no | a `landed` line's commit |
| `paths` | array of strings | no | a `dirty` line's paths |
| `session` | string | no | the session that added it |
| `until` | array of strings | no | its declared ends, in the forms of an entry's `until` |
| `until_hashes` | object | no | as an entry's |
| `until_hold` | object | no | as an entry's |
| `due` | string (time) | no | when it comes due |
| `added` | string (time) | yes | when it was added |
| `state` | string | yes | `open`, `done` or `removed`, as the log records it |
| `set` | boolean | yes | `true` where an `ack` gave the state |
| `ended` | string | no | for an open line, the end the producer found reached when it wrote the archive, in its own words; absent where none was, or where it could not check |
| `events` | array of objects | yes | every `item` event of the line, in log order, in the shape of an entry's events; `op` is `add`, `check`, `uncheck`, `remove`, or `text`, which replaces `text` and `paths` |

`"record":"old"`:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `record` | string | yes | `old` |
| `seat` | string | yes | the seat that wrote it |
| `at` | string (time) | yes | when |
| `body` | string | yes | the text |
| `session` | string | no | the session that wrote it |
| `branch` | string | no | the branch then |
| `commit` | string | no | HEAD then |
| `draft` | boolean | no | `true` where a hook wrote it, not the agent |
| `settled` | string (time) | no | when the seat ticked or removed `state#old`; absent while it still waits to be split into lines |
| `events` | array of objects | no | the `item` events on `state#old`, in log order |

Lines go grouped by seat id, ascending, each seat's in the order they were added, its `old` record
after them.

```json
{"record":"line","seat":"s-4f8k2p","id":"k3x","text":"gate fix half done: the MCP path is next","session":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","until":["merged:gate-fix"],"added":"2026-11-06T11:20:00.31Z","state":"open","set":false,"events":[{"kind":"item","at":"2026-11-06T11:20:00.31Z","seat":"s-4f8k2p","data":{"op":"add","id":"k3x","text":"gate fix half done: the MCP path is next","session":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","until":["merged:gate-fix"]}}]}
```

## `slices/<seat id>.txt`

The last turns of the seat's last session, the bytes the store holds. The first line is
`session <session id>`; the rest is the turns condensed for an agent to read: a `# <yyyy-mm-dd> UTC`
line opening each day, a `## <turn> <hh:mm>` line opening each turn, then `asked:`, `replied:` and
`did:` lines. The text has no grammar beyond that; a reader takes it as text.

```
session 7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e
# 2026-11-07 UTC
## 41  13:52
asked: commit it
did: Bash git commit
```

## `tools/<tool>/meta.json`

One JSON object per tool folder. `<tool>` is the tool's name: lower-case letters and digits in
hyphen-joined runs, starting with a letter.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `name` | string | yes | the tool's name |
| `current` | integer | no | its current version; absent where it was merged |
| `merged_into` | string | no | the tool it was merged into; a merged tool keeps no versions, and its takes went to that tool |

```json
{"name":"log-scan","current":3}
```

## `tools/<tool>/history.jsonl`, `takes.jsonl`, `pruned.jsonl`

The tool's journals, one line per record, as stored, in the order written.

| File | Field | Type | Required | Meaning |
|---|---|---|---|---|
| `history.jsonl` | `v` | integer | yes | the version written |
| | `by` | string | yes | the session that wrote it |
| | `role` | string | no | the seat name or role the writer held |
| | `at` | string (time) | yes | when |
| | `why` | string | yes | why the version was written |
| `takes.jsonl` | `v` | integer | yes | the version copied |
| | `by` | string | yes | the session that took it |
| | `role` | string | no | the seat name or role the taker held |
| | `at` | string (time) | yes | when |
| `pruned.jsonl` | `v` | integer | yes | the version removed to make room |
| | `at` | string (time) | yes | when |
| | `hashes` | array of objects | yes | each of its files: `name`, `sum` (SHA-256, lower-case hex) and `size` in bytes |

```json
{"v":2,"by":"7c1d9e2a-3b4f-4a5e-8d6c-1f2a3b4c5d6e","role":"main","at":"2026-10-30T21:04:17Z","why":"reads gzip logs too"}
```

## `tools/<tool>/v<n>/`

Every version the library still stores, its folder byte for byte: `tool.json`, then the files it
lists, at the paths it lists them under. A pruned version has no folder; `pruned.jsonl` records it.

| Field of `tool.json` | Type | Required | Meaning |
|---|---|---|---|
| `name` | string | yes | the tool's name |
| `summary` | string | yes | what it does, in one line |
| `usage` | string | yes | how to run it |
| `inputs` | string | no | what it reads |
| `outputs` | string | no | what it writes |
| `files` | array of strings | yes | its files, by path relative to the version folder |
| `forked_from` | object | no | where it was forked from: `project` (a store id), `tool`, and `v` |

```json
{"name":"log-scan","summary":"Find error bursts in a hook log","usage":"log-scan <file>","files":["log-scan.py"]}
```

## `attachments/<sha256>`

The file's original bytes, uncompressed, named for their SHA-256 in lower-case hex. One member per
hash, whatever number of entries and stores name it. An attachment record with `"stored":false`
has no member.

## Encoding

| Topic | Rule |
|---|---|
| Container | a gzip stream holding one tar archive in POSIX pax format; the file name ends `.tar.gz` |
| Members | regular files and directories only; no links, devices or FIFOs. Paths are relative, `/`-separated, with no `.` or `..` segment and no leading `/`. Each path is unique. |
| Member order | `manifest.json` first. Then each store in ascending id order: `identity.json`, `seats.jsonl`, `shelves.jsonl`, `entries.jsonl`, `names.jsonl`, `topic-merges.jsonl`, `state.jsonl`, the slices by seat id, then the tools by name, each as `meta.json`, `history.jsonl`, `takes.jsonl`, `pruned.jsonl`, then its versions in ascending number, each `tool.json` first and its files sorted by path. `attachments/` last, by hash. A reader relies on `manifest.json` coming first and on nothing else of the order. |
| Member metadata | mode, owner and modification time carry no meaning; a reader ignores them |
| Text | UTF-8 without a byte order mark. Text is exported as filed: no normalisation, no escaping beyond JSON's. |
| JSON Lines | one JSON object per line, each line ending in `\n`, no blank lines. A line has no length limit. |
| Absent values | an optional field with no value is left out. A reader treats a missing field, `null`, an empty string and an empty array alike. |
| Times | RFC 3339 in UTC, ending in `Z`, with the fractional seconds the source records, down to nanoseconds. A reader compares times as instants, never as strings. A field marked "as stored" keeps the string the store holds, unparsed: it is compared for equality, never as a time. |
| Ids | as the store holds them: entry, seat, shelf, store, item, line and graph ids alike. Every record names a seat by its id; a tool journal's `role` alone records a name, as the library does. |
| Hashes | lower-case hex |
| Sizes | integers, in bytes |

## Versioning

| Case | A reader |
|---|---|
| `format` is not `agent-memory-export` | refuses the archive |
| `version` is greater than the newest it knows | refuses the archive, naming both versions |
| a field, file, `record` value or event `kind` it does not know | ignores it and reads the rest |
| a field it needs is missing | refuses that record, names it, and reads the rest |

A version 1 reader reads every version 1 archive. A change takes version 2 where a version 1 reader
ignoring what it does not know would read the archive wrongly:

1. a field or file removed, renamed, or given another type or meaning;
2. an optional field made required;
3. a new value in a closed set a reader acts on: `status`, `kind`, `retention`, an item's or line's
   `state`, a link's `kind` or `via`, an item `op`;
4. a change to the layout, the encoding or the time format.

A new optional field, a new file, a new `record` value or a new event `kind` stays version 1.

## Left out

| Left out | Why |
|---|---|
| the whole cache: entry and tool vectors, heat, the folded log, anchor hash caches, graph statuses, embedding calibration, prompt vectors, every session and seat slot (trap indexes, seen marks, nudge ladders, owed and skipped do-now items, landed-commit mappings, ledger pressure) | rebuilt at will from the data, or session bookkeeping a new session starts without |
| machine state: the update check, the cache-prune mark, session records, context readings, the home's `format.json`, `index.json` and locks | about the machine and its sessions, not the user's data |
| the log's binary form (`log/`) | every row it holds is in `entries.jsonl`, `state.jsonl` and `topic-merges.jsonl` |
| the name index (`names.json`) | each entry carries its name; `names.jsonl` keeps the rest |
| a claim's process fields: pid, process start time and name, transcript path, fork uuid, subagent id, reservation, handover and seizure marks | they name processes and files of this machine that end with the session |
| a seat's per-seat `follows` | a store at the current format keeps follows on the shelf |
| the tool index (`index.json`): takers, take counts, roles, registering session, written times, description hash | derived from `history.jsonl`, `takes.jsonl` and the version folders |
| the tool library's `copies/` | working copies taken from stored versions, not versions themselves |
| an attachment's storage form (`raw` or `gzip`) | the archive holds every attachment uncompressed |
| `attachments.refs`, the size ledgers, the clock mark, the scratch path marker | derived from the data, or done once |
| `credentials-found.json` | notices still owed to seats, about entries the archive holds whole |
| `backup/` | the store's earlier formats, before a migration |
| bodies the store dropped | the store no longer holds them; the record says `"dropped":true` |

## Importing

1. Read `manifest.json` first and check `format` and `version` before any other member.
2. Check each attachment's bytes against its name before using them.
3. Create every entry before resolving anything that points at one: a successor, a link target or a
   carried item may be filed after the entry naming it. `entries.jsonl` is in filing order, the
   order to create entries in where order matters.
4. Resolve seats through `seats.jsonl` of the same store: every `seat` and `until_hold.seat` names
   a seat id, an expired seat included.
5. A supersede chain runs from an entry through `superseded_by` while the successor is itself
   superseded. Follow it at most 16 steps, so a cycle ends. An entry superseded without a successor
   ends its chain.
6. A carried item's `from`, `<old id>#<item id>`, finds its origin through the old entry's chain.
7. A link's target may be absent from the archive: a purged entry, a graph entry, another store's
   tool. Keep it as written.
8. A `tool:<name>@<store id>` target names a tool under `stores/<store id>/tools/`; `tool:<name>`
   one in the entry's own store.
9. The folded fields are the entry as the producer read it. An importer replaying `events` instead
   applies them in order, as the events table says, and starts from the `new` event's `data` and
   the record's `body`.
10. A declared end, an anchor, a pin and a linked item are worked out from their targets when read.
    The archive records the conditions, the hashes at filing, and the logged states; an importer
    checks them again or keeps them as declared.
