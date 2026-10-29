package capture_test

import (
	"os"
	"path/filepath"
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
	f.repo.ListFails = true
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

func TestACapturedDraftIsKeptWithALine(t *testing.T) {
	cases := []struct {
		name    string
		place   func(f *fixture) string
		prepare func(f *fixture, path string)
		says    string
	}{
		{
			name:  "git tracks it",
			place: func(f *fixture) string { return filepath.Join(f.root, "drafts", "entry.md") },
			prepare: func(f *fixture, path string) {
				f.repo.Tracked = []string{graphconfig.RealPath(path)}
			},
			says: " is tracked by git",
		},
		{
			name:    "it lies outside the work tree and the temporary directory",
			place:   func(f *fixture) string { return filepath.Join(f.t.TempDir(), "entry.md") },
			prepare: func(*fixture, string) {},
			says:    " is outside the work tree",
		},
		{
			name:  "the capture runs outside any work tree",
			place: func(f *fixture) string { return filepath.Join(f.root, "drafts", "entry.md") },
			prepare: func(f *fixture, _ string) {
				f.repo.OutsideWorkTree = true
			},
			says: " is outside the work tree",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			path := tc.place(f)
			test.WriteFile(t, path, test.Fixture(t, "capture/full.md"))
			tc.prepare(f, path)

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
		})
	}
}

func TestACapturedDraftIsKeptSilently(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *fixture, path string)
	}{
		{name: "the option is off", prepare: func(f *fixture, _ string) {
			f.settings = filepath.Join(f.t.TempDir(), "settings.json")
			test.WriteFile(f.t, f.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"capture_remove_draft":false}}}}`)
		}},
		{name: "git cannot say whether it tracks the draft", prepare: func(f *fixture, _ string) {
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
			path := f.draft("capture/full.md")
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
