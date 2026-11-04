package memory_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func lock(t testing.TB, path string) {
	t.Helper()

	err := os.Chmod(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

func TestAChangedEndOnAFileThatCannotBeReadGivesNoVerdict(t *testing.T) {
	for _, tc := range []struct {
		name  string
		until string
		// prior shapes the tree before the filing.
		prior func(h *harness)
		// reach shapes it after the filing and before the check.
		reach   func(h *harness)
		retired bool
	}{
		{name: "a file that cannot be read", until: "changed:README.md", reach: func(h *harness) {
			lock(h.t, filepath.Join(h.repo, "README.md"))
		}},
		{name: "a link whose file cannot be read", until: "changed:notes/link.txt", prior: func(h *harness) {
			test.WriteFile(h.t, filepath.Join(h.repo, "notes", "plan.txt"), "the plan\n")

			err := os.Symlink("plan.txt", filepath.Join(h.repo, "notes", "link.txt"))
			if err != nil {
				h.t.Fatal(err)
			}
		}, reach: func(h *harness) {
			lock(h.t, filepath.Join(h.repo, "notes", "plan.txt"))
		}},
		{name: "a file removed", until: "changed:README.md", retired: true, reach: func(h *harness) {
			err := os.Remove(filepath.Join(h.repo, "README.md"))
			if err != nil {
				h.t.Fatal(err)
			}
		}},
		{name: "a file replaced by a folder", until: "changed:README.md", retired: true, reach: func(h *harness) {
			err := os.Remove(filepath.Join(h.repo, "README.md"))
			if err != nil {
				h.t.Fatal(err)
			}

			test.WriteFile(h.t, filepath.Join(h.repo, "README.md", "inner.txt"), "inner\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)

			if tc.prior != nil {
				tc.prior(h)
			}

			h.must("", "claim", "main")
			h.file("Gate todo ending on a change", "body", "--kind", "todo", "--item", "settle it", "--until", tc.until)

			tc.reach(h)
			h.must("", "brief")

			out := h.must("", "search", "--term", "ending on a change", "--status", "retired", "--status", "live")
			if retired := strings.Contains(out, "end reached: "+tc.until); retired != tc.retired {
				t.Errorf("a file that cannot be read should end nothing, while one gone or replaced is a change:\n%s", out)
			}
		})
	}
}

func TestAChangedEndRefusesWhatNoHashCovers(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, filepath.Join(h.repo, "notes", "plan.txt"), "the plan\n")
	lock(t, filepath.Join(h.repo, "notes", "plan.txt"))

	for _, tc := range []struct {
		until string
		says  string
	}{
		{until: "changed:internal/gate", says: "--until changed:internal/gate names a folder, whose content no hash covers; " +
			"take gone:internal/gate, or changed: on a file inside it"},
		{until: "changed:notes/plan.txt", says: "--until changed:notes/plan.txt names a file that cannot be read, " +
			"so no hash marks its content at filing"},
	} {
		t.Run(tc.until, func(t *testing.T) {
			got := h.run("body", "new", "--title", "Gate todo ending on a change", "--until", tc.until)
			if got.code != 1 || !strings.Contains(got.err, tc.says) {
				t.Errorf("a changed: end no hash can mark should be refused, saying what to take instead, got %d %q", got.code, got.err)
			}

			got = h.run("", "ack", "state", "--add", "where the gate stands", "--until", tc.until)
			if got.code != 1 || !strings.Contains(got.err, tc.says) {
				t.Errorf("a state line should refuse the same end the same way, got %d %q", got.code, got.err)
			}
		})
	}

	if out := h.must("", "search", "--term", "ending on a change", "--status", "live"); out != "no entry matches\n" {
		t.Errorf("a refused end should file nothing:\n%s", out)
	}

	h.file("Gate todo ending on a new file", "body", "--until", "changed:notes/later.txt")
}

func TestAnAnchorThatCannotBeReadIsNeitherGoneNorDrifted(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	test.WriteFile(t, filepath.Join(h.repo, "notes.txt"), "notes\n")
	test.WriteFile(t, filepath.Join(h.repo, "hashed.txt"), "hashed\n")
	anchored := h.file("Gate finding about notes.txt", "body")
	hashed := h.file("Gate finding about hashed.txt", "body")

	if shown := h.must("", "show", hashed, "--json", "--again"); !strings.Contains(shown, `{"path":"hashed.txt","state":"ok"}`) {
		t.Fatalf("a readable anchor should read ok:\n%s", shown)
	}

	// An edit moves the file's stamp off every cached hash, so only a read of
	// the file could answer for it.
	test.WriteFile(t, filepath.Join(h.repo, "notes.txt"), "notes, edited\n")
	lock(t, filepath.Join(h.repo, "notes.txt"))
	lock(t, filepath.Join(h.repo, "hashed.txt"))

	if shown := h.must("", "show", anchored, "--again"); !strings.Contains(shown, "anchors: notes.txt\n") {
		t.Errorf("an anchor that cannot be read should carry no flag:\n%s", shown)
	}

	brief := h.must("", "brief", "--again")
	line := briefLine(brief, anchored)
	if line == "" || strings.Contains(line, "gone: notes.txt") {
		t.Errorf("an anchor that cannot be read should not be flagged gone:\n%s", brief)
	}

	if line == "" || strings.Contains(line, "retire?") {
		t.Errorf("an anchor that cannot be read should infer no end:\n%s", brief)
	}

	for path, id := range map[string]string{"notes.txt": anchored, "hashed.txt": hashed} {
		if shown := h.must("", "show", id, "--json", "--again"); !strings.Contains(shown, `{"path":"`+path+`","state":"unreadable"}`) {
			t.Errorf("an anchor that cannot be read should say so, whatever a hash cached before says:\n%s", shown)
		}
	}

	if got := h.must("", "confirm", anchored); got != "confirmed\n" {
		t.Errorf("confirm should keep an anchor it cannot re-hash, never drop it as gone:\n%s", got)
	}

	if shown := h.must("", "show", anchored, "--json", "--again"); !strings.Contains(shown, `{"path":"notes.txt","state":"unreadable"}`) {
		t.Errorf("a confirmed entry should still carry the anchor it could not re-hash:\n%s", shown)
	}
}

func TestAnInstructionFileThatCannotBeReadIsNoChange(t *testing.T) {
	_, other := seatlessBeside(t)
	path := filepath.Join(other.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")
	other.must(other.instructionsLoaded(path), "instructions")

	lock(t, path)

	if got := other.said(other.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("an instruction file that cannot be read should not count as changed, got %q", got)
	}

	err := os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got := other.said(other.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("a file readable again with its content unchanged should not count as changed, got %q", got)
	}
}
