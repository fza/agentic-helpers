package graphconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
)

func TestGraphDir(t *testing.T) {
	root := graphconfig.RealPath(t.TempDir())
	below := filepath.Join(root, "source", "internal")

	err := os.MkdirAll(below, 0o755)
	if err != nil {
		t.Fatalf("creating the tree: %v", err)
	}

	if graphconfig.GraphDir(below) != "" {
		t.Error("a tree without a graph should have none")
	}

	err = os.Mkdir(filepath.Join(root, ".sdd"), 0o755)
	if err != nil {
		t.Fatalf("creating the graph: %v", err)
	}

	if graphconfig.GraphDir(below) != filepath.Join(root, ".sdd") {
		t.Error("the graph above should govern the directory below")
	}
}
