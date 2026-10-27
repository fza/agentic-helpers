package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type commonDirAt string

func (common commonDirAt) CommonDir(context.Context, string) (string, error) {
	return string(common), nil
}

func TestTheGraphConfigIsReadOncePerProcess(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, ".sdd", "config.yaml")

	err := os.MkdirAll(filepath.Dir(config), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(config, []byte("repo_id: github.com/acme/one\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	mem := &memory{env: Env{Base: root, Checkout: commonDirAt(filepath.Join(root, ".git"))}}
	first := mem.graphProject(context.Background()).repoID

	err = os.WriteFile(config, []byte("repo_id: github.com/acme/two\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if first == "" || mem.graphProject(context.Background()).repoID != first {
		t.Error("the graph's config should be read once per process and answered from memory after")
	}
}
