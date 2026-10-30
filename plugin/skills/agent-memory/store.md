# Store question

One store per project, every agent and worktree sharing it. Only where two checkouts compete does a
hook ask, always about this checkout:

- **`agentic-memory: no store is bound to the checkout <dir> yet: <reason>. Ask the user ...`** with
  candidate lines `<store id>: <name>; checkouts <paths>` → ask the user (AskUserQuestion): each
  listed store, or a new one. Answer → `agentic-memory bind <store id>` or `agentic-memory bind
  --new`, from this checkout. Until then every memory call and tool library write is refused with
  the same text. Reason ``its `.sdd/` names the graph repository <folder>, whose store does not
  admit it`` → a split layout's committed link: ask whether this checkout belongs with that graph
  repository (`bind <graph store>`) or keeps a store of its own (`bind --new`).
- **`agentic-memory: the checkout <dir> and its graph repository keep their memory in two stores.`**
  → ask keep apart (`agentic-memory bind <own store>`) or join (`agentic-memory merge <own> <graph>`).
- **`moved <identifier> from the store <id> ...; agentic-memory merge <id> <new> joins the two
  stores`** after a bind → ask the user whether to join them; never merge unasked. No question lists
  that store any more, so a join is the user's to run at a shell.
- **`agentic-memory: <store> is not offered for this checkout; run the bind in a terminal`** (or
  `run the merge`) → that store is not among the ones the question lists, so no agent may reach it.
  Tell the user the command to run at a shell outside Claude Code; never retry it another way
  (`script`, a pty, a detached process, the pointer file).
- **`agentic-memory: neither <a> nor <b> is this checkout's own store; run the merge in a terminal`**
  → a merge from an agent joins only the checkout's own store with a listed one. Same: the user
  runs it at a shell.
  `bind --new` always runs.
- Never edit `agentic-store` in `.git/` or a store's `identity.json`: `bind` and `merge` do.
- **`agentic-helpers: migrating to <data> failed: <cause>; nothing moved, and the next call tries
  again`** → the move of the old `~/.claude` layout into the home failed; memory works on the old
  layout meanwhile. Tell the user the cause once; never move or edit those trees by hand.
- **`agentic-memory: writers.json cannot be read: ~/.claude/agentic-helpers is a link or no folder;
  no call lands until ... is mended`** → the session began before the update moved memory into the
  home, so it runs the older release. Nothing filed is lost: tell the user a restart brings memory
  back. Never mend the path.
