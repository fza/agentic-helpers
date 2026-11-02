# Slot files: bounds, exceed scenarios, design options

Scope: every `internal/slots` file the hooks write. The memory store's `sessions.slots`
(`internal/memory/sessionslots.go:22`, width 128 KiB) and the tool library's per-store
`sessions.slots` (`internal/tools/session.go:47`, 128 KiB) as asked, plus the three more slot files
the code opens: the tool library's shared seen file (`internal/tools/seen.go:41`, 128 KiB), prompt
vectors (`internal/promptvector/promptvector.go:24`, 128 KiB) and shell-guard's state
(`internal/shellguard/permission.go:164`, 4 KiB).

Measurements: `slot-measure.py`, `ladder-fields.py`, `scan-time.py` (read-only, `O_RDONLY`, over
`~/.cache/agentic-helpers`) and `apfs-probe*.py` (files in this scratch folder only, truncated to 0
bytes after each measurement). Raw output sits beside this file as `*.txt`.

## 0. Findings that drive everything else

1. **APFS zero-fills every hole shorter than 16 MiB that a write extends past.** Measured on this
   machine's data volume (`apfs-probe3.txt`): a single 300-byte write 12 MiB into an empty sparse
   file allocates 12,304 KiB; at 16 MiB and beyond it allocates 16 KiB. Two writes 16 MiB apart cost
   16,400 KiB; 32 MiB apart, 32 KiB. A write landing inside an existing hole (below allocated data)
   allocates only its own 16 KiB. So a slot file whose written twins sit less than 16 MiB apart
   costs its whole span up to the highest written twin: **on APFS a sparse slot file costs what a
   dense file of its high-water mark costs.** Every file under 16 MiB is fully allocated once
   written: each 8-pair file (2.1 MiB) holding 0 or 1 key costs 2.1 MiB on disk.
2. **Allocation units:** a lone write into a hole costs 16 KiB; a whole small file costs 4 KiB per
   started 4 KiB (300 B file = 4 KiB, 5,000 B = 8 KiB) (`apfs-probe4.txt`).
3. **`F_PUNCHHOLE` works on APFS** (fcntl 99, `unix.F_PUNCHHOLE = 0x63` in `golang.org/x/sys`):
   punching 2 MiB of a dense 4 MiB file frees 2 MiB, and a later write inside the hole allocates only
   16 KiB (`apfs-probe2.txt`, `apfs-probe3.txt`).
4. **Live cost today:** 24 slot files, about 2.46 GiB on disk. The fdbox memory store alone:
   32,768 pairs (8 GiB apparent), 16,327 pairs ever used, **2,307.6 MiB on disk** for roughly 45 MB
   of values. Linux: 4 KiB per twin write of a value under 4 KiB (`sparse-linux.txt`).
5. **The ladder value already runs at the width.** fdbox: 2,866 ladder keys, 1,918 over 4 KiB, 220
   over 64 KiB, 30 over 100 KB, largest 126,006 B of the 130,997 B room. Every large one is
   `last_prompt` (125,258 B of the 126,006). A write above the room is refused and leaves no trace,
   so how many already failed is unknowable from the file.
6. **A lookup missing its key scans every used pair.** fdbox: 32,738 `pread`s of 512 B per miss,
   85 ms in Python (`scan-time.txt`); Go saves interpreter time, not the syscalls. Every
   `readSlot` of an absent key, every first `Update` of a key (`find` then `freePair`), every
   `Delete` pays it.
7. **No write error is ever acted on.** Every memory `writeSlot` call site and every tool
   `saveSession`/`seen.save` call discards the error (`_ =`). A refused write leaves the older value
   in place, silently.
8. **Time Machine includes the cache:** `tmutil isexcluded ~/.cache/agentic-helpers` answers
   `[Included]` (no destination configured on this machine). A backup copies a changed file whole,
   so any design keeping state in one large mutable file has every backup copy all of it.

## 1. Inventory

Notation. `E(s)`: bytes of string `s` as `encoding/json` writes it, at most `6·len(s)+2` (`<`, `>`,
`&`, U+2028/2029 and each invalid UTF-8 byte become 6 bytes; control characters 2 or 6). `P`: the
longest path (1,024 on macOS, 4,096 on Linux). Room: width minus the 32-byte head minus the key,
about 130,995 B at 128 KiB. Measured columns: largest value across every live store (stores where
the purpose is busiest named).

### Memory store, `stores/<id>/memory/sessions.slots`, 128 KiB

| Key | Writer (file:line) | Go type | Grows with | Cap / trimmer | Error | Worst case | Measured |
|---|---|---|---|---|---|---|---|
| `ladder/<session>` | `hooks.go:474` (recordWrite), `:720` (prompt), `:987` (stop) | `ladder` (`hooks.go:440`) | `LastPrompt` = the whole last prompt; `Wrote` | `Wrote` ≤ 20 paths (`writtenFiles`, `ledger.go:43,1060`); prompt uncapped (cut to `EntryTargetWords` only when the asked line is built, `ledger.go:1045`) | ignored | `330 + Σ₂₀(E(path)+1) + E(prompt)`: unbounded | fdbox median 14,096, p99 102,017, max 126,006; elsewhere ≤ 10,011 |
| `shown/<session>` | `hooks.go:1132,1139,1189` (recall), `presented.go:53`, `triggers.go:356`, `landed.go:1071` | `shownSet` (`hooks.go:1075`) | one mark per entry line or body shown (a new mark per changed state), per trap, note, landed match | `shownSet.trim` halves every list (`sessionslots.go:172`) via `writeSlot` | ignored | ≈ `24·ids + 24·bodies + 11·traps + 11·notes + 30·landed`; trimmed to room | max 5,823 (fdbox, 205 ids) |
| `shown/<session>/agent/<agent>` | `subagents.go:302`, `triggers.go:356`, `presented.go:53` | `shownSet` | as above, per subagent | `shownSet.trim` | ignored | as above | 61 |
| `spawn/<session>/<tool_use_id>` | `subagents.go:287` | `forkSpawn` (`subagents.go:243`) | copy of the spawner's shown set | `forkSpawn.trim` (`subagents.go:249`) | ignored | shown + `E(level)+E(seat)+40` | none live |
| `agents/<session>` | `subagents.go:171` (`Update`) | `subagentTable` (`subagents.go:142`) | one record per subagent the session ever had; SubagentStop only clears `Seat` (`subagents.go:745`) | none | ignored | `13 + Σ(E(agentID)+30+E(seatKey))`, ≈ 45 to 70 B each: refused past about 1,900 to 2,900 subagents | 902 |
| `fork/<session>` | `hooks.go:237` | `bool` | fixed | n/a | ignored | 4 | 4 |
| `waiter/<session>` | `hooks.go:928` | `string` "pid ns" | fixed | n/a | ignored | ≈ 30 | 27 |
| `migration-failed/<session>` | `memory.go:299` | `bool` | fixed | n/a | ignored | 4 | 4 |
| `instructions/<session>` | `instructions.go:123` (`Update`), callers `:43`, `:81` | `loadedInstructions` (`instructions.go:19`) | one entry per instruction file loaded (nested `CLAUDE.md`, rules files) | none | returned, both callers discard | `11 + Σ(E(path)+E(hash)+4)`: unbounded count; per file ≤ `6P+70` | 1,764 (fdops); fdbox max 7 files, 539 |
| `graph-seen/<session>` | `captures.go:65,117` | `graphMark` (`captures.go:24`) | `Dir` path; `Seen` bitset over the month's names | bounded by `graph-names` | ignored | `≈ 80 + E(Dir) + E(stamp) + 4·⌈N/8⌉/3`, N ≤ 6,549: ≤ 2 KB plus `6P` | 479 |
| `graph-names/<yyyy>/<mm>` | `captures.go:160` (`Update`) | raw lines, 19-byte name + `\n` | captures in the graph's month directory; every graph a store's sessions reach shares the one key | none; a full table gives a new name no position, "its capture goes unnamed" (`captures.go:148`) | refused: falls back to reading the table | `20·N`: refused past N ≈ 6,549 per month | 41,020 (fdbox October, about 2,050 names); November 5,240 B by the 2nd |
| `traps/<seat>` | `triggers.go:265` | `trapIndex` (`triggers.go:206`) | traps and `--about` notes of the seat: ids, anchors, triggers | none | ignored | `11 + Σtraps(20+E(id)+ΣE(anchor)+ΣE(when)) + Σnotes(…)`: unbounded | 1,265 (claude-code-rules); fdbox 424 |
| `do-now/<seat>` | `donow.go:86` | `doNowState` (`donow.go:66`) | rules owed at a moment, each with `Title` (no hard cap: 3 to 10 words is a note, `entries.go:354`), trigger, proof | none | ignored | `≈40 + Σ(80+E(id)+E(title)+E(trigger)+ΣE(proof))`: unbounded by title | none live |
| `skipped/<seat>` | `donow.go:98` | `[]doNowItem` | appended at each session end (`donow.go:366`) and on a session change (`donow.go:118`), duplicates included, until the next do-now list prints | none | ignored | as do-now, times sessions between prints | none live |
| `brief/<seat>` | `brief.go:613` | `briefMark` | fixed | n/a | ignored | 36 | 36 |
| `seen/<seat>` | `due.go:287`, `shared.go:308,336,343,350` | `seenMark` (`shared.go:242`) | `Due`: one key per entry currently due | pruned to still-due entries (`markDue`, `due.go:256`) | ignored | `≈150 + 33·due`: refused past about 3,900 due entries | 218 |
| `seen/agent/<agent>` | same functions, key `subagents.go:359` | `seenMark` | as above | as above | ignored | as above | none live |
| `ledger-ended/<seat>` | `ledger.go:755` | `ledgerEnds` (`ledger.go:719`) | ended state lines of the seat | replaced by the current set | ignored | `11 + Σ(E(lineID)+1)` | 41 |
| `landed` (one per store) | `landed.go:356,946` | `landedSet` (`landed.go:90`) | one mapping per live todo, trap, open item and state line matched | `pendingMappings` drops settled targets; subject ≤ 72 runes (`landed.go:730`) | ignored | `14 + Σ(≈90 + E(target) + 40 + E(subject) + E(test))`, subject ≤ `6·288`: refused past about 500 pending | 1,233 |
| `landed-scan/<checkout hash>` | `landed.go:275` | `scanMark` | fixed | n/a | ignored | 121 | 121 |
| `branch-tips` (one per store) | `landed.go:1158` | `map[string]string` | every watched branch ever seen; never pruned | none | ignored | `2 + Σ(E(branch)+44)` | 74 |

Key length: `checkKey` refuses keys over 255 B (`slots.go:133`). Memory takes the payload's session
id unchecked (tools and prompt vectors check `^[A-Za-z0-9_-]{1,128}$`), so a session id over about
230 B would make every per-session key `ErrBadKey`. Claude Code sends UUIDs; the gap is theoretical.

### Tool library, per store `stores/<id>/tools/sessions.slots`, 128 KiB

| Key | Writer | Go type | Grows with | Cap / trimmer | Error | Worst case | Measured |
|---|---|---|---|---|---|---|---|
| `session/<id>` | `session.go:83` (callers `hooks.go:371,373,468`, `take.go:114`, `linked.go:186`, `update.go:210`, `session.go:148`) | `sessionState` (`session.go:33`) | holdings (each with every file's name, sum, size), `Notified`, `Pending` nudge lines (one lists every new scratch script, `hooks.go:447`), `Nudged` keys | `saveSession` halves `Nudged`, then `Holdings` (`session.go:88`); `Pending` never trimmed | ignored | `≈70 + Σholdings(60+E(project)+E(tool)+E(copy)+Σfiles(110+E(name))) + ΣE(pending) + ΣE(nudged)`: unbounded | 3,790 (fdbox) |

Halving `Holdings` drops the copies whose change the "you changed your copy" nudge detects.

### Tool library, shared `tools/sessions.slots`, 128 KiB

| Key | Writer | Go type | Grows with | Cap / trimmer | Error | Worst case | Measured |
|---|---|---|---|---|---|---|---|
| `seen/<pid>-<start>` | `seen.go:150` (`save`) | `seenSet` (`seen.go:23`) | tool versions printed, own tools offered, per owning process | `save` halves both lists | ignored | trimmed to room | 214 |

No key here is ever reclaimed: 68 of 128 pairs used, 32.1 MiB on disk for 22 values under 214 B.

### Prompt vectors, `prompt-vectors/sessions.slots`, 128 KiB

| Key | Writer | Go type | Grows with | Cap / trimmer | Error | Worst case | Measured |
|---|---|---|---|---|---|---|---|
| `<session>` | `promptvector.go:104` (`Save`, callers `tools/hooks.go:248`, `memory/vectors.go:303`) | `slot` (`promptvector.go:32`), `[]float32` as JSON | embedding dimension | none | returned, both callers discard | `≈150 + Σdims(≤15)`: refused past about 8,700 to 13,700 dimensions | 9,692 (about 9.5 B per dimension) |

Removed only at SessionEnd (`memory/hooks.go:1033`, `tools/hooks.go:606`): a session that never
ends keeps its vector forever (88 of 128 pairs used, 32.1 MiB on disk).

### Shell-guard, `shell-guard/sessions.slots`, 4 KiB

| Key | Writer | Go type | Grows with | Cap / trimmer | Error | Worst case | Measured |
|---|---|---|---|---|---|---|---|
| `<session>` | `permission.go:257` (`save`) | `sessionState` (`permission.go:153`) | `RanOut` times; `Attempts` keyed by two fixed names (`rewrite.go:15`, `shellguard.go:334,456`) | `save` halves `RanOut`; reclaims keys a week stale (`permission.go:252`) | ignored | `≈20 + 37·ranOut + 2·20`, trimmed: bounded | 321 |

Shell-guard is the one slot file already bounded by construction.

## 2. Exceed scenarios

Today a value over its room makes `Update` return `ErrTooLarge` before writing (`slots.go:232`).
The key keeps its previous value, or stays absent. Only `shownSet`, `forkSpawn`, the tool session,
tools' seen set and shell-guard trim and retry; everything else is lost silently.

| Scenario | Purposes hit | What happens today |
|---|---|---|
| **Long prompt** (above ≈130,950 B as JSON, or ≈21,800 B of `<`/`>`/`&`, or invalid UTF-8 at 6 B per byte) | `ladder` | Every ladder write of the session is refused while `LastPrompt` holds it: prompt (`hooks.go:720`), Stop (`:987`), each file write (`:474`). `Nudges`, `NudgedAt`, `Rung`, `TranscriptBase`, `ReplayCheckedSize` and `Wrote` stay frozen: the context nudge can fire again on every prompt (`Nudges` never reaches `nudgesBeforeBackstop`), against the scarce-messages rule; the dirty line misses written files; the asked line names the previous prompt. Lasts until a shorter prompt arrives. fdbox job prompts already reach 125 KB. |
| **One huge token** (base64, minified code, a URL; no spaces) | `ladder` asked line, `do-now` titles, entry titles | `words` (`render.go:226`) counts a run without spaces as one word, so `cutWords` and the 3 to 10 word title note bound nothing in bytes. A one-word 100 KB title makes the seat's `do-now` write refused: the old pending list stays, owed rules are not recorded. |
| **CJK text** | `ladder` asked line | Each CJK rune counts as one word, so a 500-word cut keeps at most 500 runes, 1,500 B: bounded. Only the stored `LastPrompt` is uncut. |
| **Long paths** (up to `P`, ×6 when escaped) | `ladder.Wrote` (20 paths: up to 491 KB on Linux), `instructions`, `graph-seen.Dir`, `traps` anchors, tool holding `Copy` | Refused write, previous value stays. |
| **Many subagents in one session** (a workflow or orchestrator) | `agents` | Past ≈1,900 to 2,900 records the `Update` is refused (`subagents.go:171`, ignored): a new subagent's level is re-learnt from its transcript on each hook call, and a full-level subagent's seat binding is not kept. |
| **Many forks** | `spawn` | Each keeps a copy of the shown set, trimmed; cleared at the fork's first hook or session end. Bounded per key. |
| **Many files loaded** (monorepo with nested `CLAUDE.md`, rules files) | `instructions` | Past ≈1,700 files at 75 B, fewer with long paths: the record stops growing; a file loaded later is never watched, so its change is never told. |
| **Long-running session** | `shown`, `shown/agent` | Marks accumulate (each changed entry line adds a mark). Past the room the set halves: the oldest half is forgotten and those entries print in full again, costing tokens and repeating lines. |
| **Busy graph month** | `graph-names` | Past ≈6,549 captures in a month a new name gets no position: the capture notice stays silent for it. fdbox reached ≈2,050 in October; November is on pace for ≈3,900. |
| **A store running a seat per job** | key count: `traps`, `seen`, `brief`, `do-now`, `skipped`, `ledger-ended` per seat; `ladder`, `waiter`, `graph-seen` per session | No value exceeds, but keys multiply: fdbox holds 16,327 used pairs in a file grown to 32,768 pairs (2.3 GiB on disk). Seat keys stay until the registry drops the seat; `Reclaim` rewrites the file only once a quarter or fewer pairs stay used (`slots.go:312`); every missing-key lookup scans all used pairs (85 ms). |
| **Many traps or `--about` notes on a seat** | `traps` | Past ≈1,300 traps: refused (`triggers.go:265`); the previous index stays, so new traps never fire. Where no index was written before, `seenMark.Traps` still counts them (`noteLog`, `hooks.go:874`) and the hook reads an absent index: no trap notice at all. |
| **Many live todos and items** | `landed` | Past ≈500 pending mappings: refused (`landed.go:356`), yet the scan mark advances (`finish(reached)`, `landed.go:325`), so the commits of that round are never rescanned: those landed matches are lost for good. |
| **Many branches watched over time** | `branch-tips` | Never pruned; past ≈1,600 branches refused: new tips are lost and `merged:` ends go undetected. |
| **Many due entries on a seat** | `seen` | Past ≈3,900: refused; the told list stays as it was, so newly due entries are told again on every call. |
| **Do-now items skipped across sessions** | `skipped` | Appended with duplicates while no do-now list prints; refused past the room: skipped items are lost. |
| **Many scratch scripts or tool holdings** | tool `session` | `Pending` is never trimmed: one nudge line listing many script names, or many pending lines, can exceed alone; the write is then refused and the nudges lost. Holdings with many files are halved away. |
| **Large embedding models** | prompt vectors | Past ≈8,700 to 13,700 dimensions the vector is never cached: each prompt embeds again. |
| **Sessions that never reach SessionEnd** | prompt vectors, tool `session`, tools' shared `seen` | No age reclaim: keys stay forever; the file only grows. |
| **Legacy import** | every file | A legacy file larger than the room is skipped without notice (`slots.go:873`). |
| **Width change in code** | every file | A file keeps its header's width (`slots.go:83`); a narrower `sessionSlotWidth` reaches only files created afterwards. Narrowing needs a migration step. |

## 3. Design options

All options share one requirement: **the guarantee comes from caps applied at encode time, not
from storage.** Each purpose declares a ceiling in encoded bytes and a `fit` that degrades a value
deterministically (drop oldest marks, cut a string at a rune boundary, cap a count, finally the
purpose's minimal value). A table-driven test per purpose builds the worst case from the caps
(strings of `<` and `0xff` at full length, every list at its cap) and asserts that
`len(encode(fit(v))) ≤ ceiling`; raising a cap past the ceiling turns it red. Storage decides only
what a ceiling costs.

Disk figures use the fdbox store's live distribution (14,4xx keys: ≈13,400 under 430 B, 2,866
ladders) after the caps of section 4 (ladders ≤ 7.4 KB).

### A. Per-purpose slot files, fitted widths, caps

One slot file per purpose (or per group of tiny purposes), width = the purpose's ceiling.

- **Bounds:** ceiling = width by construction; `ErrTooLarge` becomes unreachable (kept as the guard).
- **Disk, APFS:** Σ purposes of pairs used × 2 × width. Every key pays its purpose's worst case:
  traps (1,939 keys) at a 4 KiB ceiling 15 MiB, at 32 KiB 121 MiB; shown (745) at 16 KiB 23 MiB.
  fdbox ≈ 110 to 270 MiB depending on how tight the caps are. Linux: 4 KiB per twin written.
- **Write:** one `pwrite`, as today.
- **Crash:** today's twins.
- **Concurrency:** today's lock per file; a cross-purpose update holds two locks (none today).
- **Lookup:** still a linear scan, now per purpose (fdbox ladder file: 5,700 `pread`s per miss).
- **Migration:** one format step per component: read format 1, fit, write each purpose file.
- **Code:** smallest, ≈300 lines plus the fit registry; `internal/slots` unchanged.
- **Weakness:** every later cap change is a width change, so a migration. Generous caps cost
  ceiling × key count; tight caps cost correctness.

### B. Variable-length records

**B1. Append log per store with compaction.** Records `{key, generation, crc, value}` appended
under the writer lock; compaction rewrites live records once garbage passes the live size.
Disk ≈ 2 × live (≈30 MiB fdbox). Write: one append. Crash: CRC stops a reader at a torn tail
(as the journals). **Fatal for hooks:** a lockless reader finds a key only by scanning the log or a
separate index; short-lived hook processes cannot keep one in memory. With an on-disk index it
becomes option B2.

**B2. Inline-or-spill over size classes (the winner, details below).** One narrow slot file holds
every key; a value past its inline room spills to a slot file of the next size class under the
same key, and the narrow slot holds a pointer.

### C. One small file per session, purpose or seat, written whole with atomic rename

What d-tac-i5f replaced.

- **Bounds:** no width, so nothing is refused; caps still bound per-write cost and disk.
- **Disk, APFS:** 4 KiB per started 4 KiB per file. Per key: ≈13,400 × 4 KiB + 2,866 × 8 KiB ≈
  75 MiB at fdbox; one record per session and per seat (purposes merged): ≈5,400 files, ≈33 MiB,
  but each write then rewrites every purpose of the session (write amplification up to the shown
  set's 32 KiB).
- **Write:** temp file, write, rename: 3 to 5 syscalls and a new inode per write (≈50 to 100 µs).
- **Crash:** rename is atomic: old or new, never torn. Stronger than twins.
- **Concurrency:** readers lockless; read-modify-write needs the store lock, as today.
- **Lookup:** O(1) by path. Reclaim: `readdir` plus a stat per file (fdbox ≈5,400 to 14,400).
- **Migration:** a format step writing files; small.
- **Code:** small; `safefs.ReplaceWith` already exists.
- **Costs i5f named, still true:** files by the thousand under the cache root (fdbox 5,400 to
  14,400), the size ledger of d-tac-x9j listing every file or exempting the folder, temp files left
  by a crash needing a sweep. In its favour: Time Machine copies only the changed small files.

### D. Punch-hole or dense layout

- **Dense (no sparse file):** what APFS already does below 16 MiB gaps (finding 0.1). On Linux it
  makes things worse (full span instead of touched blocks). No gain on its own.
- **Punch-hole on today's layout:** after each write, `F_PUNCHHOLE` the twin's unused tail, and the
  gap behind a write that extended the allocation. Measured: holes stay holes. Disk ≥ 16 KiB per
  touched twin: 16,327 pairs × 32 KiB ≈ 510 MiB at fdbox (4.5 times better than today). Write: one
  extra `fcntl`, darwin-only (`unix.F_PUNCHHOLE`; Linux would use `fallocate` but needs none).
  Crash, concurrency, format: unchanged. **Fixes no bound**, and keeps the linear scan. Only worth it
  where no format change is allowed.

### E. Embedded Go key-value store, no CGO

`go.etcd.io/bbolt` (v1.4.3 is in the local module cache; facts below read from its source there):
B+tree over 4 KiB pages, values up to `MaxValueSize = 2^31-2`, keys up to 32,768 B.

- **Bounds:** nothing refused below 2 GiB; caps still needed for cost.
- **Disk:** pages packed, ≈2 × live: ≈30 MiB at fdbox; the file never shrinks without
  `bbolt.Compact` to a new file.
- **Write:** copy-on-write pages plus two `fdatasync`s per commit; `NoSync` is documented "THIS IS
  UNSAFE" (`db.go:51-61`), which breaks i5f's "a power loss may cost, never corrupt".
- **Concurrency:** `Open` takes `flock` exclusive for read-write, shared for read-only, and holds it
  until `Close` (`db.go:248`, `bolt_unix.go:18`): every reader waits while any hook holds the store
  open for writing, and every writer waits for readers. Today's readers take no lock.
- **Migration:** a format step; the whole `internal/slots` package goes.
- **Code:** removes ≈950 lines plus tests; adds a dependency to every binary that keeps state, the
  same objection i5f raised against SQLite (binary growth, an open per hook call). Needs the owner's
  confirmation under the prior-art rule.

### Winner: B2, inline-or-spill over size classes

**Layout.** Per component, `sessions.slots` becomes a narrow directory file of **512 B twins**
(today's codec, `MinWidth`), plus one slot file per size class: **2 KiB, 8 KiB, 32 KiB, 128 KiB**
(`sessions-2k.slots` and so on, names to be fixed). A value fitting the inline room (512 − 32 − key,
≈430 B, ≈93 % of fdbox's keys) lives in the directory twin. A larger value goes to the smallest class
that fits it, under the same key; the directory twin holds a tagged pointer `{class, generation}`.

- **Bounds:** the largest class's room (131,072 − 32 − 255 = 130,785 B) is the hard ceiling every
  purpose's `fit` targets; classes absorb the skew, so caps can be generous without disk cost, and
  a cap change needs no migration while it stays under 130,785 B.
- **Disk, APFS:** each key pays its own class, not its purpose's worst case. fdbox ≈ directory
  14.4 MiB (first-free placement) or 32 MiB (hashed, load ≤ 0.5) + ladders 2,866 × 16 KiB ≈ 45 MiB +
  a few larger values ≈ **60 to 80 MiB, against 2,307.6 MiB today**. Linux: the same or less, since
  512 B twins share 4 KiB blocks.
- **Write:** inline: one `pwrite` (as today). Spilled: data twin, then directory twin: two. Class
  move: write the new class, flip the pointer, clear the old class key: three.
- **Crash:** today's twin rule per file, plus ordering: data before pointer. A torn pointer reads the
  older twin's pointer, whose data twin is the other side of the pair and still whole. Writes stay
  unsynced: reordering on power loss can at worst make a key read as absent; a checksum never lets
  garbage through.
- **Concurrency:** one writer lock for the component's files, as today; readers lockless. A reader
  that follows a pointer and finds the class key missing or of another generation (a move raced it,
  or a grow renamed a file) re-reads the directory, bounded to a few tries, then reads absent.
- **Lookup:** add **hash placement** to the directory (format 2 of `internal/slots`): home pair from
  a hash of the key, linear probing to the first never-used pair, table at load ≤ 0.5, rebuilt
  smaller by `Reclaim` below 1/8. A lookup then reads one or two pairs instead of 32,738. Class files
  keep first-free placement (few keys).
- **Migration:** one `formatsteps` step per component (memory store cache, tools per store, tools
  shared, prompt vectors): read the format-1 file, `fit` every value, write the new set, remove the
  old file; `format.json` last; an older release refuses the newer format (cost i5f accepted). Since
  all of it is session state, dropping the old file instead costs only one round of repeated
  notices.
- **Code:** ≈200 to 300 lines of spill layer over the unchanged codec, ≈100 for hash placement, the
  per-purpose `fit` registry (≈20 purposes), minus every trimmer loop (`writeSlot`, `saveSession`,
  `seen.save`, shell-guard keeps its own). No dependency.

**Why it wins.** It keeps everything i5f chose and still holds (a fixed handful of files per store,
`pwrite` touching only its key's twin, lockless readers, the codec and its tests) and removes what
the measurements break: the worst-case width charged to every key (APFS charges every written twin
in full), the refusal of large values, and the linear scan. Against C it costs the same order of
disk without bringing back files by the thousand; against A it does not multiply ceilings by key
counts or tie every cap change to a migration; against E it needs no dependency, no `fsync` per
commit, no reader blocking; against D it fixes the bounds.

**Independent of the choice:**
- Set Time Machine's sticky exclusion (`com.apple.metadata:com_apple_backup_excludeItem` xattr, or
  `tmutil addexclusion`) on `~/.cache/agentic-helpers` when the cache root is created: all of it is
  rebuildable.
- Reclaim keys of prompt vectors, the tool session file and tools' shared seen file by age (a week),
  as shell-guard does.
- Validate memory's session id with the shape tools and prompt vectors already use.
- Clear a store's seat keys when a temporary seat's session ends, rather than when the registry
  expires it, so seat-per-job stores stop multiplying keys.

## 4. Caps of the winner, so nothing can exceed

All caps are in **encoded JSON bytes** (never runes or words: `words` bounds nothing in bytes).
"Ceiling" is what `fit` guarantees; every ceiling ≤ 130,785 B.

| Purpose | Caps | Ceiling | `fit` falls back to |
|---|---|---|---|
| `ladder` | `LastPrompt` = `cutWords(prompt, EntryTargetWords)`, then ≤ 3,500 B cut at a rune boundary; `Wrote` ≤ 20 paths and ≤ 3,500 B, a path not fitting skipped; fixed fields ≤ 400 B | 7,400 B (8 KiB class) | fixed fields only |
| `shown`, `shown/agent` | halve oldest marks (today's `trim`) until ≤ ceiling | 32,768 B | empty set |
| `spawn` | shown set as above, plus level and seat | 33,000 B | level and seat |
| `agents` | SubagentStop deletes the record (bounds it by live subagents); agent id ≤ 64 B, seat key ≤ 64 B; ≤ 800 records | ≈110,000 B | empty table (re-learnt from transcripts) |
| `instructions` | path ≤ 1,024 B, else not watched; add files while ≤ ceiling | 65,536 B | empty record |
| `graph-seen` | `Dir` ≤ 1,024 B, else the no-graph mark; `Seen` follows `graph-names` parts | 2,200 B | month and no-graph mark |
| `graph-names/<yyyy>/<mm>/<part>` | 6,000 names per part, parts appended; names are fixed 19 B (`isGraphID`) | 120,000 B per part | n/a: exact |
| `traps` | anchor ≤ 1,024 B, trigger ≤ 512 B; total ≤ ceiling | 65,536 B | a "match from the log" marker: the tool hook loads the log and matches directly (a cache, so correct, only slower) |
| `do-now`, `skipped` | store id, trigger ≤ 256 B, ≤ 4 proof triggers ≤ 128 B each, since; the title read from the log at print time; `skipped` deduplicated by id; overflow kept as a count printed "+N more" | 65,536 B | count only |
| `brief`, `waiter`, `fork`, `migration-failed`, `landed-scan` | fixed shapes | ≤ 200 B (inline) | n/a |
| `seen`, `seen/agent` | `Due` pruned as today, ≤ ceiling, oldest dropped past it | 65,536 B (≈1,900 due keys) | fixed fields |
| `ledger-ended` | ≤ ceiling, oldest dropped | 65,536 B | empty |
| `landed` | subject ≤ 72 runes and ≤ 200 B; target ≤ 64 B; ≤ 300 mappings; a round meeting the cap leaves the scan mark at the last commit whose mappings were stored | ≈120,000 B | the mappings held before the round |
| `branch-tips` | pruned to watched branches at every save; branch name ≤ 255 B, else not recorded; ≤ 300 | ≈95,000 B | watched branches only |
| tool `session` | copy path ≤ 1,024 B; one digest per holding instead of the per-file list; ≤ 50 holdings; `Pending` ≤ 20 lines ≤ 512 B each (script list cut to "+N more"); `Nudged` halved | ≈110,000 B | holdings only |
| tools' shared `seen` | halve (today's) | 32,768 B | empty |
| prompt vector | binary little-endian `float32`, not JSON; ≤ 32,000 dimensions, else not cached | 128,100 B | not cached |
| shell-guard | unchanged: its own 4 KiB file, already bounded | 4,064 B | n/a |
| any key | ≤ 255 B; session ids `^[A-Za-z0-9_-]{1,128}$` | n/a | no write |

## 5. Facts that would change the choice

1. **APFS's 16 MiB zero-fill rule.** Measured here, undocumented by Apple. If holes below 16 MiB
   stayed holes (as on ext4, overlayfs, tmpfs), today's 128 KiB width would cost 4 to 16 KiB per twin
   and option A with caps alone would suffice. A probe of the same script on each release's macOS
   would catch a change.
2. **Typical key counts.** Stores other than fdbox hold 1 to 60 keys. If seat-per-job stores were
   not a supported pattern, the linear scan and the key-count multiplier would not matter, and A
   would be enough.
3. **Time Machine.** The cache root is included here. If owners back it up and an exclusion is
   unwanted, every single-file design (A, B2, E) copies its files whole per backup; C copies only
   changed small files.
4. **A dependency's acceptability.** If the owner accepts bbolt's reader/writer `flock` coupling and
   an `fsync` per commit, E removes the most code and halves disk against B2.
5. **Torn-write atomicity.** If a 512 B or 4 KiB aligned `pwrite` were atomic under power loss on
   APFS and ext4 (POSIX does not promise it), the directory could drop its twins and halve again.
6. **Where large values come from.** Every large value at fdbox is `last_prompt`. If the ledger's
   asked line were computed at prompt time and the prompt not kept, ladders fall to the 2 KiB class
   and A's disk drops to about B2's.
7. **Whether entries' titles get a hard byte cap.** Today 3 to 10 words is a note only; a hard cap at
   filing would remove the title from every slot cap above.
