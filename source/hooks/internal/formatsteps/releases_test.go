package formatsteps_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	currentMemoryFormat = 8
	currentToolsFormat  = 2
	claimOwnerPID       = test.OwnerPID
)

// successors turn what a release printed into what the current release must
// print for the same data, by the store format that changed the answer. A
// format with no entry keeps every answer as it was.
var successors = map[int]func(answer map[string]any){
	// Format 8 reads the free-text state slot a seat left as the ledger's
	// state#old, and starts the ledger empty.
	8: func(answer map[string]any) {
		held, isBrief := answer["state"].(map[string]any)
		if !isBrief {
			return
		}

		answer["state"] = map[string]any{"lines": []any{}, "old": held}
	},
}

// releaseRun is the migrated fixture of one release in root, the environment
// a binary of the current release finds it in.
type releaseRun struct {
	t    *testing.T
	root string
	dump test.ReleaseDump
}

func (run releaseRun) userHome() string { return filepath.Join(run.root, "home") }

func (run releaseRun) roots() home.Roots {
	return home.Locate(run.userHome(), func(string) string { return "" })
}

func getenv(name string) string {
	switch name {
	case "CLAUDE_PLUGIN_OPTION_EMBEDDING_PROVIDER":
		return "off"
	case "CLAUDE_PLUGIN_OPTION_UPDATE_NOTICE", "CLAUDE_PLUGIN_OPTION_MEMORY_TURNS_OFF_AUTO":
		return "false"
	}

	return ""
}

func noEmbedder(embed.Config) (embed.Embedder, error) { return nil, os.ErrNotExist }

func (run releaseRun) memory(session string) memory.Env {
	repo := filepath.Join(run.root, "repo")

	return memory.Env{
		Base: repo, Home: run.userHome(), Roots: run.roots(), CacheRoot: filepath.Join(run.userHome(), ".cache"),
		OwnerName: "claude", SessionID: session, Settings: []string{filepath.Join(run.root, "absent.json")}, Getenv: getenv,
		Now: func() time.Time { return run.dump.At }, Tmp: filepath.Join(run.root, "tmp"), Random: test.Random(),
		Processes: test.NewProcesses(), Checkout: test.GitDirOf{},
		Git: &test.Git{Top: repo, Commit: "520005e4", Branch: "main"}, Embedders: noEmbedder, Version: "test",
		Graph: &test.Graph{Err: os.ErrNotExist},
	}
}

func (run releaseRun) tools(session string) tools.Env {
	repo := filepath.Join(run.root, "repo")

	return tools.Env{
		Base: repo, Home: run.userHome(), Roots: run.roots(), OwnerName: "claude", SessionID: session,
		Settings: []string{filepath.Join(run.root, "absent.json")}, Getenv: getenv,
		Now: func() time.Time { return run.dump.At }, Processes: test.NewProcesses(), Checkout: test.GitDirOf{},
		Git: &test.Git{Top: repo, Commit: "520005e4", Branch: "main"}, Embedders: noEmbedder, Version: "test",
	}
}

func (run releaseRun) call(read test.ReleaseRead) string {
	run.t.Helper()

	var captured test.Captured

	code := 0

	switch read.Binary {
	case "agentic-memory":
		code = memory.Main(context.Background(), read.Args, run.memory(read.Session), captured.Streams(""))
	case "agentic-tools":
		code = tools.Main(context.Background(), read.Args, run.tools(read.Session), captured.Streams(""))
	default:
		run.t.Fatalf("the dump names a binary this test does not run: %s", read.Binary)
	}

	if code != 0 {
		run.t.Fatalf("%s %v should answer: %s%s", read.Binary, read.Args, captured.Out.String(), captured.Err.String())
	}

	return strings.ReplaceAll(strings.TrimSpace(captured.Out.String()), run.root, "@WORK@")
}

// storeOf is the one store folder of the fixture.
func (run releaseRun) storeOf(component string) string {
	run.t.Helper()

	stores, err := filepath.Glob(filepath.Join(run.roots().Stores(), "*", component))
	if err != nil || len(stores) != 1 {
		run.t.Fatalf("the fixture should hold one store, got %v %v", stores, err)
	}

	return stores[0]
}

// liveClaim hands the seat the fixture holds to the test's own process table:
// the process that claimed it is gone, the session is the one the reads run as.
func (run releaseRun) liveClaim() {
	run.t.Helper()

	files, err := filepath.Glob(filepath.Join(run.storeOf(home.Memory), "seats", "s-??????.json"))
	if err != nil {
		run.t.Fatal(err)
	}

	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			run.t.Fatal(err)
		}

		var claim map[string]any

		err = json.Unmarshal(raw, &claim)
		if err != nil {
			run.t.Fatal(err)
		}

		claim["pid"], claim["started"] = claimOwnerPID, test.OwnerStart.UTC().Format(time.RFC3339)
		raw, _ = json.Marshal(claim)
		test.WriteFile(run.t, path, string(raw))
	}
}

func canonical(t *testing.T, raw []byte, successor []func(map[string]any)) string {
	t.Helper()

	var answer map[string]any

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	err := decoder.Decode(&answer)
	if err != nil && err != io.EOF {
		t.Fatalf("an answer should be JSON: %v: %s", err, raw)
	}

	for _, change := range successor {
		change(answer)
	}

	sorted, err := json.MarshalIndent(answer, "", " ")
	if err != nil {
		t.Fatal(err)
	}

	return string(sorted)
}

func TestEveryCommittedReleaseMigratesToWhatItHeld(t *testing.T) {
	versions := test.ReleaseVersions(t)
	if len(versions) == 0 {
		t.Fatal("no release fixture is committed")
	}

	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			run := releaseRun{t: t, root: t.TempDir(), dump: test.ReleaseDumpOf(t, version)}
			test.ReleaseTree(t, version, run.root)
			run.liveClaim()

			written, err := formatsteps.Read(run.storeOf(home.Memory))
			if err != nil {
				t.Fatal(err)
			}

			var changes []func(map[string]any)

			for format := written + 1; format <= currentMemoryFormat; format++ {
				if change, found := successors[format]; found {
					changes = append(changes, change)
				}
			}

			migration.Prepare(context.Background(), migration.Env{UserHome: run.userHome(), Roots: run.roots()}, io.Discard)

			err = run.roots().CheckFormat()
			if err != nil {
				t.Errorf("the home should stand at a format this release reads: %v", err)
			}

			for _, read := range run.dump.Commands {
				want := canonical(t, read.Out, changes)

				if got := canonical(t, []byte(run.call(read)), nil); got != want {
					t.Errorf("%s %v should answer what the release held, got:\n%s\nwant:\n%s", read.Binary, read.Args[0], got, want)
				}
			}

			for dir, format := range map[string]int{run.storeOf(home.Memory): currentMemoryFormat, run.storeOf(home.Tools): currentToolsFormat} {
				version, err := formatsteps.Read(dir)
				if err != nil || version != format {
					t.Errorf("%s should stand at the current format, got %d %v", dir, version, err)
				}
			}

			if mismatches := test.LedgerMismatches(t, run.storeOf(home.Memory)); len(mismatches) > 0 {
				t.Errorf("the size ledger should match the files: %s", strings.Join(mismatches, "; "))
			}
		})
	}
}

// A release cannot ship without its fixture: scripts/release-fixture.go writes
// one for the version plugin.json names.
func TestTheNamedReleaseHasAFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "plugin", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}

	var plugin struct {
		Version string `json:"version"`
	}

	err = json.Unmarshal(raw, &plugin)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(test.ReleaseVersions(t), "v"+plugin.Version) {
		t.Errorf("no fixture exists for v%s; run go run scripts/release-fixture.go v%s", plugin.Version, plugin.Version)
	}
}
