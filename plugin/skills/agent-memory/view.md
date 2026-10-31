# View

A question no flag answers (what was filed for every seat, what a shelf holds, what changed this
week) → `view`, sdd's layout language over memory. Composes like `sdd view`.

```bash
agentic-memory view --layout 'active:kind(trap):rank(heat):n(5):as-list'
agentic-memory view --layout 'for(all):active:as-list,top(10)'
agentic-memory view --layout 'active:topic(infra):as-counts'
agentic-memory view --explain 'top(5):kind(trap)'   # the layout spelled out whole
agentic-memory view --explain brief                 # the brief's fixed sections; nothing configures them
```

- Section = colon-chained words, ending in `as-list`, `as-counts`, `as-bodies` or `as-grouped`.
  Commas split sections. No spaces outside quotes. Quote paths, titles, dates:
  `anchored("internal/gate")`.
- Filters: `active`, `status(...)`, `kind(...)`, `topic(t)` (prefix by component), `since("7d")`,
  `id(...)`, `not(<filter>)`, `seat(...)`, `shelf(...)`, `for(all|<shelf>)`, `mine` (own shelf and
  what it receives), `from(...)`, `branch(b)`, `linked(<id>)`, `anchored("<path>")`,
  `inbox(<seat>)` (what it is told of: shelf mates' entries and those addressed to it), `unread`
  (after its seen mark; needs `inbox` in the section). A view never moves the seen mark: the
  arrival notice still tells it.
- **`returns` → what needs you, and the verb.** `active:mine:returns:as-list` at a claim or before
  relying on old entries. Under each entry: `anchor drifted` → `confirm` if it still holds, else
  `supersede`; `anchor gone` → `supersede`/`retire`; `link ... closed by|superseded by <id>` → read
  `<id>`, then `supersede`/`retire`; `link ... gone` → `unlink`; `until end met|inferred` → `retire`;
  `branch filed on <b>` → nothing until it merges. The entry's line drops the flags a reason
  restates. `unchecked` = could not tell: check by hand. A view reads only cached graph statuses; `link unchecked: ... cache is cold` → `show <id>` asks sdd,
  then view again.
- `attached` keeps entries holding attachments; `rank(size)` puts the most attachment bytes first.
  Either one lists each file's name and size and the total under the entry:
  `active:attached:rank(size):as-list`.
- `due` keeps entries carrying a due time: `active:mine:due:as-list`.
- Shape: `rank(heat|in-degree|size|by(date))` (`in-degree` = most linked to), `n(N)`, `skip(N)`,
  `name("...")`, `brief` (short lines), macro `top(N)`. `topic(t)` opening a section = hottest
  first, as in sdd; later in a section it only filters.
- `group(by(kind|seat|shelf|from))` + `as-grouped` → blocks per value: who on a shelf filed what.
  `expand(refs)` → each entry's links under it, no `show` per link.
- **A view warms nothing, moves no mark.** Reading is not using.
- **Refused, never repaired**: `layout: unknown word: "knd" at position 1; nearest valid word: kind`.
  Take the nearest word, resend. `layout does not parse: ... expected <what>` → fix the syntax there.
- `search --topic` and `brief --topic` match a topic and those below it, as `topic(t)` does.
