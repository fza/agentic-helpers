package capture_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func saysAboutTheDraft(stderr string) bool {
	return strings.Contains(stderr, "draft removed") || strings.Contains(stderr, "draft kept")
}

func TestACapturedDraftIsRemoved(t *testing.T) {
	cases := []struct {
		name  string
		stock bool
	}{
		{name: "after the capture step"},
		{name: "after the single call of a stock sdd", stock: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			f.runner.Stock = tc.stock
			path := f.draft("capture/full.md")

			got := f.run(path)
			if got.code != 0 {
				t.Fatalf("a clean draft should capture, got: %+v", got)
			}

			if test.Exists(t, path) {
				t.Error("the draft should be gone once its entry is captured")
			}

			if !strings.Contains(got.stderr, "draft removed: "+path+"\n") {
				t.Errorf("the caller should learn the draft is gone, got: %s", got.stderr)
			}
		})
	}
}

func TestADraftUnderTheTemporaryDirectoryIsRemoved(t *testing.T) {
	f := newFixture(t, "")
	f.repo.ListOutsideRepository = true
	path := filepath.Join(f.temp, "drafts", "entry.md")
	test.WriteFile(t, path, test.Fixture(t, "capture/full.md"))

	got := f.run(path)
	if got.code != 0 {
		t.Fatalf("a clean draft should capture, got: %+v", got)
	}

	if test.Exists(t, path) || !strings.Contains(got.stderr, "draft removed: "+path+"\n") {
		t.Errorf("a draft outside any work tree but under the temporary directory should go, got: %s", got.stderr)
	}
}

func TestAFailedCaptureKeepsTheDraft(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		prepare func(f *fixture)
	}{
		{name: "the lint reports", config: "grounding/full.yaml", prepare: func(f *fixture) {
			f.runner.Lint = process.Result{Code: 1, Stdout: "x:1:1:House.Passive:passive voice\n"}
		}},
		{name: "the pre-flight holds it back", prepare: func(f *fixture) {
			f.runner.DryRun = process.Result{Code: 1, Stdout: rejected}
		}},
		{name: "sdd new fails after the pre-flight", prepare: func(f *fixture) {
			f.runner.Capture = process.Result{Code: 1, Stderr: "error: git commit failed\n"}
		}},
		{name: "a stock sdd's single call fails", prepare: func(f *fixture) {
			f.runner.Stock = true
			f.runner.Capture = process.Result{Code: 1, Stderr: "error: git commit failed\n"}
		}},
		{name: "a stock sdd's single call runs past the limit", prepare: func(f *fixture) {
			f.runner.Stock = true
			f.runner.HangCapture = true
			f.limit = "10ms"
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.config)
			tc.prepare(f)
			path := f.draft("capture/full.md")

			got := f.run(path)
			if got.code == 0 {
				t.Fatalf("the capture should fail, got: %+v", got)
			}

			if !test.Exists(t, path) {
				t.Error("a failed capture should keep the draft to retry from")
			}

			if saysAboutTheDraft(got.stderr) {
				t.Errorf("a failed capture should say nothing about the draft, got: %s", got.stderr)
			}
		})
	}
}

func (f *fixture) draftAt(path string) string {
	f.t.Helper()

	test.WriteFile(f.t, path, test.Fixture(f.t, "capture/full.md"))

	return path
}

func TestACapturedDraftIsKeptWithALine(t *testing.T) {
	const swapped = "a file written while git ran\n"

	cases := []struct {
		name    string
		place   func(f *fixture) string
		prepare func(f *fixture, path string)
		says    string
		after   func(t *testing.T, f *fixture, path string)
	}{
		{
			name:  "git tracks it",
			place: func(f *fixture) string { return f.draftAt(filepath.Join(f.root, "drafts", "entry.md")) },
			prepare: func(f *fixture, path string) {
				f.repo.Tracked = []string{graphconfig.RealPath(path)}
			},
			says: " is tracked by git",
		},
		{
			name:  "it lies outside the work tree and the temporary directory",
			place: func(f *fixture) string { return f.draftAt(filepath.Join(f.t.TempDir(), "entry.md")) },
			says:  " is outside the work tree",
		},
		{
			name:  "the capture runs outside any work tree",
			place: func(f *fixture) string { return f.draftAt(filepath.Join(f.root, "drafts", "entry.md")) },
			prepare: func(f *fixture, _ string) {
				f.repo.OutsideWorkTree = true
			},
			says: " is outside the work tree",
		},
		{
			name: "it is a symlink",
			place: func(f *fixture) string {
				target := f.draftAt(filepath.Join(f.root, "drafts", "target.md"))
				link := filepath.Join(f.root, "drafts", "entry.md")

				err := os.Symlink(target, link)
				if err != nil {
					f.t.Fatalf("linking the draft: %v", err)
				}

				return link
			},
			says: " is not a regular file",
			after: func(t *testing.T, f *fixture, _ string) {
				if test.ReadFile(t, filepath.Join(f.root, "drafts", "target.md")) != test.Fixture(t, "capture/full.md") {
					t.Error("the symlink's target should stay untouched")
				}
			},
		},
		{
			name:  "another file takes its place while git runs",
			place: func(f *fixture) string { return f.draftAt(filepath.Join(f.root, "drafts", "entry.md")) },
			prepare: func(f *fixture, path string) {
				f.repo.Before = func(args []string) {
					if args[0] != "ls-files" {
						return
					}

					err := os.Remove(path)
					if err != nil {
						f.t.Errorf("removing the judged draft: %v", err)
					}

					test.WriteFile(f.t, path, swapped)
				}
			},
			says: " changed while it was checked",
			after: func(t *testing.T, _ *fixture, path string) {
				if test.ReadFile(t, path) != swapped {
					t.Error("the file that took the draft's place should stay untouched")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			path := tc.place(f)

			if tc.prepare != nil {
				tc.prepare(f, path)
			}

			got := f.run(path)
			if got.code != 0 {
				t.Fatalf("a clean draft should capture, got: %+v", got)
			}

			if !test.Exists(t, path) {
				t.Error("the draft should stay")
			}

			if !strings.Contains(got.stderr, "draft kept: "+path+tc.says+"\n") {
				t.Errorf("the caller should learn why the draft stays, got: %s", got.stderr)
			}

			if tc.after != nil {
				tc.after(t, f, path)
			}
		})
	}
}

func TestACapturedDraftIsKeptSilently(t *testing.T) {
	cases := []struct {
		name    string
		temp    bool
		prepare func(f *fixture, path string)
	}{
		{name: "the option is off", prepare: func(f *fixture, _ string) {
			f.settings = filepath.Join(f.t.TempDir(), "settings.json")
			test.WriteFile(f.t, f.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"capture_remove_draft":false}}}}`)
		}},
		{name: "git fails in the work tree", prepare: func(f *fixture, _ string) {
			f.repo.ListFails = true
		}},
		{name: "git finds no repository around a draft in the work tree", prepare: func(f *fixture, _ string) {
			f.repo.ListOutsideRepository = true
		}},
		{name: "git fails under the temporary directory", temp: true, prepare: func(f *fixture, _ string) {
			f.repo.ListFails = true
		}},
		{name: "the draft cannot be deleted", prepare: func(f *fixture, path string) {
			dir := filepath.Dir(path)

			err := os.Chmod(dir, 0o500)
			if err != nil {
				f.t.Fatalf("making the drafts directory read-only: %v", err)
			}

			f.t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")

			path := filepath.Join(f.root, ".tmp", "drafts", "entry.md")
			if tc.temp {
				path = filepath.Join(f.temp, "drafts", "entry.md")
			}

			f.draftAt(path)
			tc.prepare(f, path)

			got := f.run(path)
			if got.code != 0 {
				t.Fatalf("a clean draft should capture, got: %+v", got)
			}

			if !test.Exists(t, path) {
				t.Error("the draft should stay")
			}

			if saysAboutTheDraft(got.stderr) {
				t.Errorf("a draft kept this way should go unmentioned, got: %s", got.stderr)
			}
		})
	}
}

func TestAVanishedDraftGoesUnmentioned(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *fixture, vanish func())
	}{
		{name: "during the capture", prepare: func(f *fixture, vanish func()) {
			f.runner.Before = func(argv []string) {
				if slices.Contains(argv, "--preflight-verified") {
					vanish()
				}
			}
		}},
		{name: "while git runs", prepare: func(f *fixture, vanish func()) {
			f.repo.Before = func(args []string) {
				if args[0] == "ls-files" {
					vanish()
				}
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			path := f.draft("capture/full.md")
			tc.prepare(f, func() {
				err := os.Remove(path)
				if err != nil {
					t.Errorf("removing the draft: %v", err)
				}
			})

			got := f.run(path)
			if got.code != 0 {
				t.Fatalf("a clean draft should capture, got: %+v", got)
			}

			if saysAboutTheDraft(got.stderr) {
				t.Errorf("a draft already gone should go unmentioned, got: %s", got.stderr)
			}
		})
	}
}
