// Package test holds the fakes and helpers the hook tests share.
package test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

const (
	Session      = "c3fb65eb-dbdf-47a8-9e04-e0477053b2b6"
	OtherSession = "a1b2c3d4-0000-4000-8000-000000000000"
)

type CommonDir struct {
	Dir string
	Err error
}

func (fake CommonDir) CommonDir(context.Context, string) (string, error) {
	return fake.Dir, fake.Err
}

type Captured struct {
	Out bytes.Buffer
	Err bytes.Buffer
}

func (captured *Captured) Streams(stdin string) hookio.Streams {
	return hookio.Streams{In: strings.NewReader(stdin), Out: &captured.Out, Err: &captured.Err}
}

func WriteFile(t *testing.T, path string, content string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("creating the parent of %s: %v", path, err)
	}

	err = os.WriteFile(path, []byte(content), 0o644)
	if err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func Age(t *testing.T, path string, age time.Duration) {
	t.Helper()

	then := time.Now().Add(-age)

	err := os.Chtimes(path, then, then)
	if err != nil {
		t.Fatalf("aging %s: %v", path, err)
	}
}

func Exists(t *testing.T, path string) bool {
	t.Helper()

	_, err := os.Lstat(path)

	return err == nil
}
