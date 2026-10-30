package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func oldEnv(t *testing.T) (Env, string) {
	t.Helper()

	root := test.OldHome(t)

	return Env{
		UserHome:   filepath.Join(root, "home"),
		Roots:      test.Roots(root),
		Lookup:     test.GitDirOf{},
		Processes:  test.Executables{},
		Executable: filepath.Join(root, "bin", "agentic-memory"),
		SessionID:  test.Session,
		Now:        func() time.Time { return time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC) },
		MountRoots: []string{filepath.Join(root, "Volumes")},
	}, root
}

// acrossVolumes fails every rename as one across volumes does, so each move
// copies.
func acrossVolumes(from string, to string) error {
	return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EXDEV}
}

// haltAfter stops a run at its nth checkpoint and counts every one it passes.
type haltAfter struct {
	at     int
	passed int
}

func (halt *haltAfter) halt() bool {
	halt.passed++

	return halt.at > 0 && halt.passed >= halt.at
}

// describeHome is everything a migration leaves, with the test's folder and
// the random store ids taken out: each store by its legacy keys, whether it
// waits unclaimed, what it admits and every file of its components; every
// other file of the data root and of the old trees; and the store each
// checkout's pointer names.
func describeHome(t *testing.T, env Env, root string) string {
	t.Helper()

	clean := strings.NewReplacer(root, "@ROOT@")
	keyOf := map[string]string{}

	// The local key of a checkout without a repo_id hashes its path, which
	// differs between two test folders.
	plain, err := home.Inspect(context.Background(), filepath.Join(root, "checkouts", "plain"), test.GitDirOf{}, nil)
	if err != nil {
		t.Fatalf("inspecting the plain checkout: %v", err)
	}

	var lines []string

	files := func(dir string, label string) {
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			relative, _ := filepath.Rel(dir, path)
			info, _ := entry.Info()
			line := label + " " + relative + " " + info.Mode().String()

			if info.Mode().IsRegular() {
				data, _ := os.ReadFile(path)
				sum := sha256.Sum256([]byte(clean.Replace(string(data))))
				line += " " + hex.EncodeToString(sum[:8])
			}

			lines = append(lines, line)

			return nil
		})
	}

	for _, section := range []string{"stores", "unclaimed"} {
		listed, _ := os.ReadDir(filepath.Join(env.Roots.Data, section))

		for _, entry := range listed {
			var identity home.Identity

			data, _ := os.ReadFile(filepath.Join(env.Roots.Data, section, entry.Name(), "identity.json"))

			err := json.Unmarshal(data, &identity)
			if err != nil {
				t.Fatalf("reading the identity of %s: %v", entry.Name(), err)
			}

			key := section + "|" + strings.Join(stableKeys(identity.LegacyKeys, plain.LegacyKey), ",")
			keyOf[entry.Name()] = key

			lines = append(lines, key+" admits "+clean.Replace(strings.Join(identity.Paths(), ","))+
				" repo "+strings.Join(identity.RepoIDs, ","))

			components, _ := os.ReadDir(filepath.Join(env.Roots.Data, section, entry.Name()))
			for _, component := range components {
				if component.IsDir() {
					files(filepath.Join(env.Roots.Data, section, entry.Name(), component.Name()), key+"/"+component.Name())
				}
			}
		}
	}

	top, _ := os.ReadDir(env.Roots.Data)
	for _, entry := range top {
		if !slices.Contains([]string{"stores", "unclaimed", "index.json", ".lock", lockName}, entry.Name()) {
			files(filepath.Join(env.Roots.Data, entry.Name()), "data/"+entry.Name())
		}
	}

	files(filepath.Join(env.UserHome, ".claude"), "claude")

	for _, checkout := range []string{"app", "plain"} {
		pointer, _ := os.ReadFile(filepath.Join(root, "checkouts", checkout, ".git", home.PointerName))
		lines = append(lines, checkout+" points to "+keyOf[strings.TrimSpace(string(pointer))])
	}

	slices.Sort(lines)

	return strings.Join(lines, "\n")
}

// stableKeys is legacy keys as two test folders share them: the plain
// checkout's key, a hash of its path, named alike, and the list sorted after
// that, since the hash sorts anywhere among the other keys.
func stableKeys(keys []string, plain string) []string {
	stable := make([]string, 0, len(keys))
	for _, key := range keys {
		if key == plain {
			key = "@PLAIN@"
		}

		stable = append(stable, key)
	}

	slices.Sort(stable)

	return stable
}

func TestARunKilledAtAnyStepIsUndoneAndThenMigratedAfresh(t *testing.T) {
	variants := []struct {
		name   string
		rename func(string, string) error
	}{
		{name: "on one volume"},
		{name: "across volumes", rename: acrossVolumes},
	}

	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			clean, cleanRoot := oldEnv(t)
			counted := &haltAfter{}
			clean.rename, clean.halt = variant.rename, counted.halt

			done, err := Run(context.Background(), clean)
			if err != nil || !done {
				t.Fatalf("a clean run should migrate, got %v %v", done, err)
			}

			want := describeHome(t, clean, cleanRoot)
			_ = filepath.WalkDir(filepath.Join(clean.UserHome, ".claude"), func(path string, entry fs.DirEntry, err error) error {
				if err == nil && strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".claude" {
					t.Errorf("a completed run should leave no tree set aside, found %s", path)
				}

				return nil
			})
			t.Logf("a clean run passes %d checkpoints", counted.passed)

			// The last two checkpoints stand before and after `format.json`;
			// across volumes most of the others fall inside a copy.
			points := []int{counted.passed - 1, counted.passed}

			for at := 1; at < counted.passed-1; at += max(1, counted.passed/15) {
				points = append(points, at)
			}

			for _, at := range points {
				env, root := oldEnv(t)
				before := test.Snapshot(t, filepath.Join(env.UserHome, ".claude"))
				env.rename, env.halt = variant.rename, (&haltAfter{at: at}).halt

				_, err = Run(context.Background(), env)
				if !errors.Is(err, errHalted) {
					t.Fatalf("checkpoint %d: the run should stop there, got %v", at, err)
				}

				if !exists(env.journalPath()) {
					t.Fatalf("checkpoint %d: a killed run should leave its journal", at)
				}

				env.halt = nil

				if !exists(env.Roots.FormatFile()) {
					err = env.recoverJournal()
					if err != nil {
						t.Fatalf("checkpoint %d: undoing the killed run: %v", at, err)
					}

					left, _ := os.ReadDir(env.Roots.Data)
					if test.Snapshot(t, filepath.Join(env.UserHome, ".claude")) != before || len(left) != 1 ||
						exists(filepath.Join(root, "checkouts", "app", ".git", home.PointerName)) {
						t.Errorf("checkpoint %d: the undo should leave the old home as it was and nothing else", at)
					}
				}

				_, err = Run(context.Background(), env)
				if err != nil {
					t.Fatalf("checkpoint %d: the next call should settle and migrate: %v", at, err)
				}

				if got := describeHome(t, env, root); got != want {
					t.Errorf("checkpoint %d: the home should equal a clean migration's\ngot:\n%s\nwant:\n%s", at, got, want)
				}
			}
		})
	}
}

func TestAJournalLeftWithNoOldTreeStandingIsStillSettled(t *testing.T) {
	env, _ := oldEnv(t)

	for _, gone := range []string{env.memoryRoot(), env.writersRoot()} {
		err := os.RemoveAll(gone)
		if err != nil {
			t.Fatalf("removing %s: %v", gone, err)
		}
	}

	env.halt = func() bool { return !isDir(env.toolsRoot()) }

	_, err := Run(context.Background(), env)
	if !errors.Is(err, errHalted) || env.oldLayout() {
		t.Fatalf("the run should stop with the library set aside and no old tree left, got %v", err)
	}

	env.halt = nil

	done, err := Run(context.Background(), env)
	if err != nil || !done || !isFence(env.toolsRoot()) || exists(env.journalPath()) {
		t.Errorf("the next call should settle the journal and migrate, got %v %v", done, err)
	}
}

func TestACreationNeverRemovesWhatWasThereBefore(t *testing.T) {
	cases := []struct {
		name   string
		taken  func(env Env) string
		rename func(string, string) error
	}{
		{name: "a copy's target", rename: acrossVolumes, taken: func(env Env) string {
			return env.Roots.Shared(attachments)
		}},
		{name: "a reading's session folder", taken: func(env Env) string {
			return filepath.Join(env.memoryRoot(), "sessions", test.Session)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := oldEnv(t)
			env.rename = tc.rename
			taken := tc.taken(env)
			test.WriteFile(t, filepath.Join(taken, "kept"), "kept")

			before := test.Snapshot(t, filepath.Join(env.UserHome, ".claude"))

			_, err := Run(context.Background(), env)
			if err == nil || !errors.Is(err, fs.ErrExist) {
				t.Fatalf("the run should refuse to create over an existing path, got %v", err)
			}

			if !exists(filepath.Join(taken, "kept")) || test.Snapshot(t, filepath.Join(env.UserHome, ".claude")) != before {
				t.Error("the undo should leave what stood there before")
			}
		})
	}
}

func TestAJournalReadsEveryWholeStepAndNoMore(t *testing.T) {
	cases := []struct {
		name    string
		content string
		steps   int
		fails   bool
	}{
		{name: "whole steps", content: `{"kind":"rename","from":"/a","to":"/b"}` + "\n" + `{"kind":"fence","path":"/c"}` + "\n", steps: 2},
		{name: "a last step cut short", content: `{"kind":"rename","from":"/a","to":"/b"}` + "\n" + `{"kind":"fen`, steps: 1},
		{name: "a broken step before others", content: `{"kind":` + "\n" + `{"kind":"fence","path":"/c"}` + "\n", fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), journalName)
			test.WriteFile(t, path, tc.content)

			steps, err := readJournal(path)
			if (err != nil) != tc.fails || len(steps) != tc.steps {
				t.Errorf("a journal should yield its whole steps, a cut last one ignored, got %d %v", len(steps), err)
			}
		})
	}
}

func TestAnUndoReversesOnlyAChangeThatHappened(t *testing.T) {
	cases := []struct {
		name   string
		layout func(dir string)
		step   func(dir string) step
		fails  bool
		left   []string
	}{
		{
			name:   "a rename that happened",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "to", "file"), "x") },
			step: func(dir string) step {
				return step{Kind: stepRename, From: filepath.Join(dir, "from"), To: filepath.Join(dir, "to")}
			},
			left: []string{"from/file"},
		},
		{
			name:   "a rename a kill prevented",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "from", "file"), "x") },
			step: func(dir string) step {
				return step{Kind: stepRename, From: filepath.Join(dir, "from"), To: filepath.Join(dir, "to")}
			},
			left: []string{"from/file"},
		},
		{
			name:   "a rename whose trees are both gone",
			layout: func(string) {},
			step: func(dir string) step {
				return step{Kind: stepRename, From: filepath.Join(dir, "from"), To: filepath.Join(dir, "to")}
			},
		},
		{
			name:   "a stub",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "stub", formatName), string(stubText())) },
			step:   func(dir string) step { return step{Kind: stepStub, Path: filepath.Join(dir, "stub")} },
		},
		{
			name: "a stub folder a kill left empty",
			layout: func(dir string) {
				_ = os.Mkdir(filepath.Join(dir, "stub"), 0o700)
			},
			step: func(dir string) step { return step{Kind: stepStub, Path: filepath.Join(dir, "stub")} },
		},
		{
			name:   "data where a stub was named",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "stub", "seats.json"), "{}") },
			step:   func(dir string) step { return step{Kind: stepStub, Path: filepath.Join(dir, "stub")} },
			fails:  true,
			left:   []string{"stub/seats.json"},
		},
		{
			name:   "a fence",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "fence"), string(fenceText("/data"))) },
			step:   func(dir string) step { return step{Kind: stepFence, Path: filepath.Join(dir, "fence")} },
		},
		{
			name:   "a file where a fence was named",
			layout: func(dir string) { test.WriteFile(t, filepath.Join(dir, "fence"), "someone else's\n") },
			step:   func(dir string) step { return step{Kind: stepFence, Path: filepath.Join(dir, "fence")} },
			left:   []string{"fence"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.layout(dir)

			err := undo(dir, tc.step(dir))
			if (err != nil) != tc.fails {
				t.Errorf("the undo should refuse only where it would remove data, got %v", err)
			}

			var left []string

			_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr == nil && !entry.IsDir() {
					relative, _ := filepath.Rel(dir, path)
					left = append(left, filepath.ToSlash(relative))
				}

				return nil
			})

			if !slices.Equal(left, tc.left) {
				t.Errorf("the undo should reverse exactly the change that happened, left %v", left)
			}
		})
	}
}
