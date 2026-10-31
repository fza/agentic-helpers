# Shelves, shared entries, lifetime

## Shelf

Shelf = the memory seats share. Every seat files onto a shelf: its own unless claimed onto another.
Several seats on one shelf (agents on one board row) read and write one memory; each still holds its
own seat.

```bash
agentic-memory claim <seat> --shelf <s> # a new seat onto shelf s, made where none exists
agentic-memory claim --shelf <s>        # a new seat <s>-<n> onto shelf s
```

- **"<seat>, shared"** ("main, shared", "Haupt-Session, geteilt") → a seat on `<seat>`'s memory,
  without asking wherever one reading fits:
  - `<seat>` exists and is free → `claim <seat>`;
  - `<seat>` exists and is held → `claim --shelf <its shelf>`, a new seat beside it;
  - `<seat>` is new and the store holds exactly one shelf → `claim <seat> --shelf <that shelf>`;
  - `<seat>` is new and several shelves, or none, exist → ask the user which shelf, naming them.
- **A seat stays on its shelf for good.** `claim <seat> --shelf <other>` → refused
  `<seat> is on shelf <name>`. New seat named like a shelf, no `--shelf` → refused
  `<name> names a shelf; claim it with --shelf <name>`.
- **The shelf is the seat's memory**: brief, search, recall cover every entry on it.
  `search --seat <s>` narrows to one filer. Brief opens `seat <seat> on shelf <shelf>` where the
  shelf has another name.
- **Every seat on the shelf may `retire`, `retain`, `rekind`, `retitle`, `tag`, `untag` its
  entries.** Seat on another shelf → refused `<id> is on shelf <name>`. `supersede` and `confirm`
  of another shelf's entry are allowed; its seats' next brief lists them.
- **`agentic-memory: changed on shelf <name> (<n>):`** → a shelf mate retired, retained, rekinded,
  retitled, tagged or untagged an entry. One line each, `<id> <title>: <what> by <seat>`. Act on it,
  say nothing about the mechanism.
- Store → `~/.local/share/agentic-helpers/stores/<id>/memory/`, outside the repo, one per project.
  Found by `.git/agentic-store`, else the checkout's path, `repo_id` or origin. Worktrees share it,
  and so do a code repo and its graph repo.

## Shared entries

```bash
agentic-memory new --title '...' --topic infra --for idea --for row-7ah <<'EOF'  # a seat's shelf, a shelf
agentic-memory new --title '...' --topic infra --for all <<'EOF'                 # every seat
agentic-memory follow infra        # this seat's shelf receives everything filed under infra
agentic-memory unfollow infra
```

- **Handover, blocker, changed fact another seat needs → `--for`.** A seat name stands for its
  shelf. It lands in every seat on it: brief, search, recall, as if their own. Only your shelf
  retires it.
- **`agentic-memory: new for <seat> (<n>):`** at a prompt, a tool call, or as a turn that wakes an
  idle session → entries filed for this seat, or by a shelf mate, one short line each (kind and
  source named only when not a note from an agent). Read them, act on them, say nothing about the
  mechanism.

## Lifetime

A shelf is **durable** (the default) or **temporary**: purged, with its seats and entries, a
retention after its last seat closes. Claiming a seat on it before then keeps everything.

```bash
agentic-memory lifetime temporary | <span> | durable   # the shelf of this seat; span 90m, 12h, 2d, 1w, 0
agentic-memory claim <seat> --lifetime <value>          # same, while claiming
agentic-memory drop <shelf>                             # purge an unused temporary shelf now
```

- `temporary` = default retention, `temporary_seat_lifetime` in `config.yaml`, 1d unless set. `0` = purged as the
  last seat closes.
- **Closes**: `release`, or the session ending (not `/clear`, not `/resume`).
- **User's words map onto `--lifetime` without asking**: "temporary" → `temporary`; a span
  ("2 days", "for an hour") → `2d`, `1h`; "durable", "keep it" → `durable`. Combines with seat and
  shelf words: "main, shared, temporary" → the shared claim with `--lifetime temporary`.
- Something meant to outlive a temporary shelf → file it `--for` a durable seat's shelf. It survives
  the purge, filer shown `<seat> (expired)`.
