package scratch_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const week = 7 * 24 * time.Hour

func payload(session string) string {
	return `{"session_id": "` + session + `"}`
}

func project(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	err := os.Mkdir(filepath.Join(root, ".tmp"), 0o755)
	if err != nil {
		t.Fatalf("creating .tmp: %v", err)
	}

	return root
}

func sessionDir(t *testing.T, root string, name string, age time.Duration) string {
	t.Helper()

	held := filepath.Join(root, ".tmp", "claude", name)
	test.WriteFile(t, filepath.Join(held, "probe.txt"), "scratch")

	if age > 0 {
		test.Age(t, held, age)
	}

	return held
}

func run(t *testing.T, root string, mode string, stdin string) (int, *test.Captured) {
	t.Helper()

	captured := &test.Captured{}
	code := scratch.Main(context.Background(), []string{mode}, root, captured.Streams(stdin))

	return code, captured
}

func TestEndRemovesOnlyTheEndingSession(t *testing.T) {
	root := project(t)
	mine := sessionDir(t, root, test.Session, 0)
	theirs := sessionDir(t, root, test.OtherSession, 0)
	keep := filepath.Join(root, ".tmp", "claude", "keep", "worth-keeping.sh")
	drafts := filepath.Join(root, ".tmp", "drafts", "entry.md")
	test.WriteFile(t, keep, "#!/bin/sh\n")
	test.WriteFile(t, drafts, "draft")

	code, captured := run(t, root, "end", payload(test.Session))

	if code != 0 {
		t.Fatalf("end should succeed, stderr: %s", captured.Err.String())
	}

	if test.Exists(t, mine) {
		t.Error("the ending session's directory should be removed")
	}

	if !strings.Contains(captured.Out.String(), "swept") {
		t.Error("the sweep should be reported")
	}

	for _, survivor := range []string{theirs, keep, drafts} {
		if !test.Exists(t, survivor) {
			t.Errorf("%s should survive another session's end", survivor)
		}
	}
}

func TestEndRemovesNothingWithoutASession(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
	}{
		{name: "no session", stdin: `{}`},
		{name: "not a session identifier", stdin: payload("keep")},
		{name: "path escape", stdin: payload("../../..")},
		{name: "garbled payload", stdin: `{not json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			mine := sessionDir(t, root, test.Session, 0)
			test.WriteFile(t, filepath.Join(root, ".tmp", "claude", "keep", "x"), "x")

			code, captured := run(t, root, "end", tc.stdin)

			if code != 0 {
				t.Errorf("end should succeed, stderr: %s", captured.Err.String())
			}

			if !test.Exists(t, mine) || !test.Exists(t, filepath.Join(root, ".tmp", "claude", "keep")) {
				t.Error("nothing should be removed")
			}
		})
	}
}

func TestEndNeverFollowsALink(t *testing.T) {
	root := project(t)
	outside := filepath.Join(t.TempDir(), "precious")
	test.WriteFile(t, filepath.Join(outside, "keep.txt"), "keep")

	link := filepath.Join(root, ".tmp", "claude", test.Session)

	err := os.MkdirAll(filepath.Dir(link), 0o755)
	if err != nil {
		t.Fatalf("creating the sessions directory: %v", err)
	}

	err = os.Symlink(outside, link)
	if err != nil {
		t.Fatalf("linking: %v", err)
	}

	run(t, root, "end", payload(test.Session))

	if !test.Exists(t, filepath.Join(outside, "keep.txt")) {
		t.Error("a directory reached through a link should never be emptied")
	}
}

func TestStartReapsOnlyAbandonedSessions(t *testing.T) {
	root := project(t)
	stale := sessionDir(t, root, test.OtherSession, 30*24*time.Hour)
	fresh := sessionDir(t, root, test.Session, 0)
	named := filepath.Join(root, ".tmp", "claude", "keep")
	test.WriteFile(t, filepath.Join(named, "x"), "x")
	test.Age(t, named, 99*24*time.Hour)

	code, captured := run(t, root, "start", payload(test.Session))

	if code != 0 {
		t.Fatalf("start should succeed, stderr: %s", captured.Err.String())
	}

	if test.Exists(t, stale) {
		t.Error("an abandoned session directory should be reaped")
	}

	if !test.Exists(t, fresh) {
		t.Error("a directory inside the window should survive")
	}

	if !test.Exists(t, named) {
		t.Error("a named directory should survive whatever its age")
	}

	if !strings.Contains(captured.Out.String(), "reaped 1 session directory(s)") {
		t.Errorf("the reap should be reported, got: %q", captured.Out.String())
	}
}

func TestStartKeepsASessionJustInsideTheWindow(t *testing.T) {
	root := project(t)
	held := sessionDir(t, root, test.OtherSession, week-time.Hour)

	run(t, root, "start", payload(test.Session))

	if !test.Exists(t, held) {
		t.Error("a directory younger than the window should survive")
	}
}

func TestAProjectWithoutScratchIsLeftAlone(t *testing.T) {
	for _, mode := range []string{"start", "end"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			test.WriteFile(t, filepath.Join(root, ".memory", "MEMORY.md"), test.Fixture(t, "memory-index/dead-pointer.md"))

			code, captured := run(t, root, mode, payload(test.Session))

			if code != 0 || captured.Out.Len() != 0 {
				t.Error("the hook should stay silent")
			}

			if test.Exists(t, filepath.Join(root, ".tmp")) {
				t.Error("the hook should create nothing")
			}
		})
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	for _, args := range [][]string{{}, {"sweep"}, {"end", "extra"}} {
		captured := &test.Captured{}
		code := scratch.Main(context.Background(), args, project(t), captured.Streams(""))

		if code != 1 || !strings.Contains(captured.Err.String(), "usage") {
			t.Errorf("%v should be refused with usage", args)
		}
	}
}

func TestStartReportsWhatMemoryLetSlip(t *testing.T) {
	cases := []struct {
		name     string
		carry    int
		index    string
		reported string
	}{
		{name: "oversized carry-forward", carry: 21 * 1024, reported: "main-memory.md is 21 KB"},
		{name: "carry-forward within the limit", carry: 1024},
		{name: "pointer at nothing", index: "memory-index/dead-pointer.md", reported: "points at carryforward/gone-memory.md"},
		{name: "pointer that resolves", carry: 1024, index: "memory-index/live-pointer.md"},
		{name: "link to the web", index: "memory-index/web-link.md"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			memory := filepath.Join(root, ".memory")

			err := os.MkdirAll(filepath.Join(memory, "carryforward"), 0o755)
			if err != nil {
				t.Fatalf("creating the memory directory: %v", err)
			}

			if tc.carry > 0 {
				test.WriteFile(t, filepath.Join(memory, "carryforward", "main-memory.md"), strings.Repeat("x", tc.carry))
			}

			if tc.index != "" {
				test.WriteFile(t, filepath.Join(memory, "MEMORY.md"), test.Fixture(t, tc.index))
			}

			_, captured := run(t, root, "start", payload(test.Session))
			said := captured.Out.String()

			if tc.reported == "" && said != "" {
				t.Errorf("nothing should be reported, got: %q", said)
			}

			if tc.reported != "" && !strings.Contains(said, tc.reported) {
				t.Errorf("the slip should be reported, got: %q", said)
			}
		})
	}
}
