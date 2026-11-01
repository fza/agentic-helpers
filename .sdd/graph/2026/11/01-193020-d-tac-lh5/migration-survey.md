# Migration mechanisms in source/hooks (survey 2026-11-01)

Paths relative to source/hooks. Line numbers at main be712a1.

1. Memory store format: internal/memory/migrate.go, format.go (storeFormat 7). Version in
   format.json (missing = 1), newer refused (ErrNewerStore). migrate() under store lock: ==1 F1 (writes 2),
   <=2 F2 (3), switch <=3 F3 (writes 5, skips 4) / ==4 F4 (5), <=5 F5 (6), F6 unconditional (7).
   No journal: in-process rollback closures; format.json last; backups backup/format-N-<stamp>, 30 d expiry.
   F4, F6 only bump to fence older binaries from new event kinds (unknown kind reads as corrupt).
   Steps bundle jobs (F3 credential scan, F5 seal+slot import+ledger).
   Defects (from code, untested): re-check under lock only tests == storeFormat (migrate.go:60), so a store a
   newer binary moved to 8 between prepare and lock gets F6 writing 7 over it; a kill after swapLog before
   writeFormat (F1, F3) leaves log.format-N set aside and the next swap fails, nothing clears it.
   Tests relabel a current-shape store with an old format.json for F3-F6; only F1, F5 use old-shaped data;
   no crash test. Duplicates home.ComponentFormat and copyTree.
2. Scratch-paths rewrite: marker scratch-paths.json, log.RewriteAfter, format unchanged; end of prepare.
3. Home/legacy layout (internal/migration): write-ahead journal, recovery, fences/stubs {"format":999},
   process-gated sweep; crash tests kill at every checkpoint. Fixture stores at formats 1/4/5 only moved,
   never upgraded: no test chains layout migration into the store chain. newestReleasedStoreFormat = 5
   hard-coded (migration_test.go:522).
4. Home format.json Format=1, CheckFormat refuses newer; no stepping.
5. Tools library format 2 (internal/tools/format.go): convert journals, slots.Import, write format,
   delete legacy; read-time legacy fallbacks.
6. slots.File.Adopt (slots.go:746): CheckComponent, lock, recheck, import, write format, remove; used by
   shell-guard state, promptvector, tools seen.
7. memorylog: binary magic+format byte, per-record kind/schema, unknown skipped (but unknown event kind =
   corrupt); manifest format retired; legacy tail.jsonl read-time + seal.
8. Caches keyed by layout/schema number, rebuilt: fold cache (foldLayout 5 + binary Version), home index,
   tools index, vector segments.
9. Delete-only: vectors.gob, tools vectors .json, .space.json.
10. Read-time compat readers without version (seat started text, sensor seconds, Follows, ...).
11. Scratch relocate (internal/scratch): presence of legacy dir, no version, no backup.
12. Plugin config: Renamed table (read-time, problem line), MigrateAgentAnswers copies ConfigOnly answers
    into config.yaml at agentic-helpers check, no version/lock/backup.

Patterns: per-component format.json (shared home.ComponentFormat, memory duplicates); ad hoc step chain
(memory only); journal+recovery+fence (migration only); marker one-shot; import-write-delete (slots);
binary magic+schema; version-keyed caches; read-time tolerant readers; delete-only; stamped backups.
