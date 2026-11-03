# AGENTS.md - `source/`

Go rules, loaded when a session touches code under `source/`.

## Layout

- **One module, `source/hooks`.** Every hook program is a binary under `cmd/agentic-<hook>/`,
  published by each release and built for development as the root [`AGENTS.md`](../AGENTS.md) sets
  out. The `agentic-` prefix is not optional: a bare name on a PATH can shadow a system tool.
- **The commands are one program.** `cmd/agentic-helpers` answers as `agentic-memory`,
  `agentic-tools` and `agentic-capture` to the name it runs under, and as itself to any other name;
  each command's environment is resolved in a file of its own there. A new command is a name in its
  `commands` table and a link in its `links/`, which the release archive packs. A hook that refuses
  a call when it fails, shell-guard or the grounding gate, stays a program of its own, so no other
  package's startup can fail it.
- **`.goreleaser.yaml` lists every binary the release builds.** A new `cmd/` directory is added
  there, or the plugin's launcher finds no binary for it.
- **`main.go` resolves the environment and calls the hook's `Main`, nothing else.** Every decision
  lives under `internal/`, where a test can drive it.
- **No CGO. No `/pkg`.** Every non-`cmd` package lives under `internal/`, except the module's own
  `test/` package: fakes, shared helpers and fixtures, never inline in a `_test.go`.
- **Standard library first.** A dependency earns its place by removing a mechanism the hooks would
  otherwise carry and test themselves. `github.com/goccy/go-yaml` reads a draft's header and
  `.sdd/grounding.yaml`, since the standard library parses no YAML; `golang.org/x/text` normalises
  seat names to NFC, which the standard library cannot; `mvdan.cc/sh/v3` parses bash for
  shell-guard and the grounding gate, the only way to tell a call from text that names one;
  `golang.org/x/sys` reads darwin's process table through sysctl, which the standard library does
  not expose and which saves a `ps` spawn per read, and maps memory's vector segments with
  `madvise`, which `syscall` lacks on darwin, and asks standard input for its terminal settings,
  which the standard library cannot, and renames between two folder descriptors with `renameat`,
  and links one with `linkat` for a move that never replaces a file, which neither `syscall` on
  darwin nor `os.Root` offers; `github.com/bmatcuk/doublestar/v4` matches
  memory's `path:` triggers, since `path.Match` has no `**`.

## Hooks

- **A hook stays silent in a project without its directory.** It reads nothing, creates nothing and
  exits 0 before it touches stdin. Shell-guard has no directory to wait for: its work is every Bash
  call.

## Code

- **Assign, then check. Never inline** `if err := foo(); err != nil`.
- **Wrap with context:** `fmt.Errorf("reading the payload: %w", err)`. Never log an error and then
  return it.
- **Sentinel errors are package-level `var Err...`**, compared with `errors.Is`.
- **Accept interfaces, return structs.** An interface is declared beside the code that consumes it.
- **No `panic` outside `main`**, and none there past argument parsing. The one exception re-raises:
  a `recover` that turns a memory fault into an error panics again with anything else it caught.
- **Names say what a thing is for**, not what it holds.
- **Context first** on anything reaching a process or a file.
- **No comment per function, variable or constant.** Package comment above the `package` clause of
  a real source file.

## Tests

- **Run `go test -race ./...` from `source/hooks` to check the code compiles.** Never build for that.
- **Unit tests cover what a function decides; functional tests drive a hook's `Main`** against a tree
  in `t.TempDir()`. No test reaches the network or the developer's home, and a subprocess is faked
  behind an interface.
- **Table-driven with `t.Run`** where the cases share a shape. In-package tests go in
  `*_internal_test.go`; every other test file declares the `<pkg>_test` package.
- **Fixtures are real files under `test/fixtures/`**, embedded with `go:embed`.
- **Assertion messages describe the behaviour**, never repeat the expected value.
