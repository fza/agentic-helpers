# Traps, file notices, do-now rules

```bash
agentic-memory new --kind trap --title '...' --topic t --when 'path:.autopilot/plan/parts/jobs/**' <<'EOF'
agentic-memory new --kind rule --from user --title '...' --topic t --on claim --proof tool:Monitor <<'EOF'
agentic-memory ack <id>... --done | --decline '<why>'   # settle what no tool call proves
```

- **A trap fires at the tool call.** Every file it names counts; `--when` adds `path:<glob>` (`**`
  crosses directories), `command:<regex>` (Bash, case-sensitive), `tool:<name>`. Traps only.
  `agentic-memory: trap for <what matched>:` then the trap's line → stop and read it before going
  on. Once per session; again after a compaction.
- **A note or todo about a file → `--about <path>`.** Told at the first call naming that path:
  `agentic-memory: about <path>: <line>` → read it before going on. A path only mentioned in the body
  tells nothing. Once per session; again after a compaction.
- **A rule owed at a moment → `--on claim|compact`**, the user's rules only, `--proof <trigger>` for
  the call that proves it. `do now (<moment>):` then numbered items, before the brief → do each
  now, first. `proof: <trigger>` → the matching tool call settles it. `ack when done` → do it, then
  `ack <id> --done`; can't → `ack <id> --decline '<why>'`, and tell the user why.
- **User says something never goes into memory** → a rule with `--forbids '<regex>'` (RE2,
  `(?i)` for any case), quoting the user. `forbidden by <rule id> <rule title>` / `matched: "..."`
  after a filing → the entry landed against the rule: run the `retire ... --because 'forbidden by
  <rule id>'` it names, unless the match is a false hit.
- **`agentic-memory: do-now items pending:`** at `Stop` → the turn is held for them; settle them.
- The `Work shipped since your last state write` hold at `Stop` comes once per state write: HEAD
  moved, a graph capture, or `state_stale_after_writes` (5) file writes. Twice per moment at most;
  what stays pending opens the next list as `skipped last session:`.
- Rules are capped at a third of the brief. `rules exceed a third of the brief: <n>; retire or
  rekind some` → do it.
