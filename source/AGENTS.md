# AGENTS.md - `source/`

Go rules, loaded when a session touches code under `source/`.

## Layout

- **One module, `source/hooks`.** Every hook is a binary under `cmd/agentic-<hook>/`, installed with
  `go install -C source/hooks ./cmd/...` into `~/go/bin`. The `agentic-` prefix is not optional: a bare
  name there can shadow a system tool.
- **`main.go` resolves the environment and calls the hook's `Main`, nothing else.** Every decision
  lives under `internal/`, where a test can drive it.
- **No CGO. No `/pkg`.** Every non-`cmd` package lives under `internal/`, except the module's own
  `test/` package: fakes, shared helpers and fixtures, never inline in a `_test.go`.
- **Standard library only**, until a dependency removes a mechanism the hooks would otherwise carry
  and test themselves.

## Hooks

- **A hook stays silent in a project without its directory.** It reads nothing, creates nothing and
  exits 0 before it touches stdin.
- **Output wording is part of the contract.** Skills and agents quote it. Change it on purpose, never
  in passing.

## Code

- **Assign, then check. Never inline** `if err := foo(); err != nil`.
- **Wrap with context:** `fmt.Errorf("reading the payload: %w", err)`. Never log an error and then
  return it.
- **Sentinel errors are package-level `var Err...`**, compared with `errors.Is`.
- **Context first** on anything reaching a process or a file.
- **No comment per function, variable or constant.** Comment only the reasoning a name cannot carry.
  Package comment above the `package` clause of a real source file.

## Tests

- **Run `go test -race ./...` from `source/hooks` to check the code compiles.** Never build for that.
- **Unit tests cover what a function decides; functional tests drive a hook's `Main`** against a tree
  in `t.TempDir()`. No test reaches the network or the developer's home, and a subprocess is faked
  behind an interface.
- **Table-driven with `t.Run`** where the cases share a shape. In-package tests go in
  `*_internal_test.go`; every other test file declares the `<pkg>_test` package.
- **Fixtures are real files under `test/fixtures/`**, embedded with `go:embed`.
- **Assertion messages describe the behaviour**, never repeat the expected value.
- **Break every guard, watch its test fail, restore the file byte-identically.**
