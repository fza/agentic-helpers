package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// A process that can write a store plants links where the next write goes;
// none may land at the link's target.
func TestAWriteNeverFollowsAPlantedLink(t *testing.T) {
	cases := []struct {
		name  string
		plant func(h *harness) string
		// inside names a link target inside the store, which os.Root alone
		// would follow; empty plants a link leaving the store.
		inside string
		write  func(h *harness) ran
	}{
		{
			name:  "the index's temporary file",
			plant: func(h *harness) string { return filepath.Join(h.store(), "index.json.tmp") },
			write: func(h *harness) ran { return h.run("", "reindex") },
		},
		{
			name:  "the current file's temporary file",
			plant: func(h *harness) string { return filepath.Join(h.store(), "word-count", "current.tmp") },
			write: func(h *harness) ran {
				return h.run("", "update", "word-count", "--summary", "Count words, a new way", "--why", "x")
			},
		},
		{
			name:  "a history file",
			plant: func(h *harness) string { return filepath.Join(h.store(), "word-count", "history.jsonl") },
			write: func(h *harness) ran {
				return h.run("", "update", "word-count", "--summary", "Count words, a new way", "--why", "x")
			},
		},
		{
			name:  "a takes file",
			plant: func(h *harness) string { return filepath.Join(h.store(), "word-count", "takes.jsonl") },
			write: func(h *harness) ran { return h.run("", "take", "word-count") },
		},
		{
			name:   "a history file linked inside the store",
			plant:  func(h *harness) string { return filepath.Join(h.store(), "word-count", "history.jsonl") },
			inside: "word-count/spare.txt",
			write: func(h *harness) ran {
				return h.run("", "update", "word-count", "--summary", "Count words, a new way", "--why", "x")
			},
		},
		{
			name:   "a tool folder linked inside the store",
			plant:  func(h *harness) string { return filepath.Join(h.store(), "new-tool") },
			inside: "spare",
			write: func(h *harness) ran {
				return h.run("", "add", "new-tool", "--from", h.file("n.py", "n\n"), "--summary", "A tool planted over", "--usage", "u")
			},
		},
		{
			name:   "the old fixed temporary name of current, linked inside the store",
			plant:  func(h *harness) string { return filepath.Join(h.store(), "word-count", ".current.tmp") },
			inside: "word-count/spare.txt",
			write: func(h *harness) ran {
				return h.run("", "update", "word-count", "--summary", "Count words, a new way", "--why", "x")
			},
		},
		{
			name:  "a tool folder",
			plant: func(h *harness) string { return filepath.Join(h.store(), "new-tool") },
			write: func(h *harness) ran {
				return h.run("", "add", "new-tool", "--from", h.file("n.py", "n\n"), "--summary", "A tool planted over", "--usage", "u")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.tool("word-count", "Count the words of standard input")

			victim := filepath.Join(h.home, "victim")
			test.WriteFile(t, filepath.Join(victim, "keep.txt"), "untouched\n")

			at := tc.plant(h)
			target := filepath.Join(victim, "keep.txt")

			if strings.HasSuffix(tc.name, "folder") {
				target = victim
			}

			_ = os.Remove(at)

			if tc.inside != "" {
				// A file target sits in the tool folder and a folder target in the
				// store, each inside the root the write opens relative to.
				test.WriteFile(t, filepath.Join(h.store(), "spare", "keep.txt"), "untouched\n")
				test.WriteFile(t, filepath.Join(h.store(), "word-count", "spare.txt"), "untouched\n")
				inside := filepath.Join(h.store(), filepath.FromSlash(tc.inside))

				before := test.Tree(t, inside)

				// Relative: os.Root refuses an absolute link on its own.
				relative, err := filepath.Rel(filepath.Dir(at), inside)
				if err == nil {
					err = os.Symlink(relative, at)
				}

				if err != nil {
					t.Fatalf("planting the link: %v", err)
				}

				tc.write(h)
				_ = os.Remove(at)

				if test.Tree(t, inside) != before {
					t.Error("a write should never land at a planted link's target inside the store")
				}

				return
			}

			err := os.Symlink(target, at)
			if err != nil {
				t.Fatalf("planting the link: %v", err)
			}

			tc.write(h)

			data, _ := os.ReadFile(filepath.Join(victim, "keep.txt"))
			listed, _ := os.ReadDir(victim)

			if string(data) != "untouched\n" || len(listed) != 1 {
				t.Error("a write should never land at a planted link's target")
			}
		})
	}
}

func TestNeitherReaderNorWriterUsesAHardLinkedFile(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")

	outside := filepath.Join(h.home, "outside.txt")
	test.WriteFile(t, outside, "untouched\n")

	history := filepath.Join(h.store(), "word-count", "history.jsonl")
	_ = os.Remove(history)

	err := os.Link(outside, history)
	if err != nil {
		t.Fatalf("planting the hard link: %v", err)
	}

	h.run("", "update", "word-count", "--summary", "Count words, a new way", "--why", "x")

	data, _ := os.ReadFile(outside)
	if string(data) != "untouched\n" {
		t.Error("an append should never write into a file with a second name")
	}

	secret := filepath.Join(h.home, "secret.txt")
	test.WriteFile(t, secret, "secret\n")

	file := filepath.Join(h.store(), "word-split", "v1", "word-split.py")
	_ = os.Remove(file)

	err = os.Link(secret, file)
	if err != nil {
		t.Fatalf("planting the hard link: %v", err)
	}

	h.run("", "take", "word-split")

	if test.Exists(t, filepath.Join(h.store(), "copies", "word-split", "v1", "word-split.py")) {
		t.Error("a reader should never read a file with a second name")
	}
}
