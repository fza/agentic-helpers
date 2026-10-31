package memory_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfirmNamesOnlyTheEntriesLeftWithAnchorsGone(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	kept := h.file("Gate reads Bash only today", "see the gate", "--about", "internal/gate/gate.go")
	lost := h.file("Readme names the build steps", "see the readme", "--about", "README.md", "--about", "internal/gate/gate.go")

	err := os.Remove(filepath.Join(h.repo, "README.md"))
	if err != nil {
		t.Fatalf("removing the readme: %v", err)
	}

	got := h.must("", "confirm", kept, lost)
	if got != "confirmed\n"+lost+": anchors gone from the tree, dropped: README.md\n" {
		t.Errorf("confirm should name only the entry whose anchor it could not re-hash, and which:\n%s", got)
	}

	if again := h.must("", "confirm", kept, lost); again != "confirmed\n" {
		t.Errorf("a confirm re-hashing every anchor should answer confirmed alone:\n%s", again)
	}
}
