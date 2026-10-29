package pieces_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSessionEndStartsTheSweepAndTheSweepRemovesTheFences(t *testing.T) {
	root := t.TempDir()
	starts := &test.Starts{}
	env := pieces.Env{Migration: migration.Env{
		UserHome: filepath.Join(root, "home"), Roots: test.Roots(root), Processes: test.Executables{},
		Starter: starts, Executable: filepath.Join(root, "bin", "agentic-helpers"),
	}}

	fence := filepath.Join(root, "home", ".claude", "agentic-tools")
	test.WriteFile(t, env.Migration.Roots.FormatFile(), `{"format":1}`+"\n")
	test.WriteFile(t, fence, migration.FencePrefix+env.Migration.Roots.Data+"\n")

	var captured test.Captured

	code := pieces.Main(context.Background(), []string{"end"}, env, captured.Streams(""))
	if code != 0 || captured.Out.Len()+captured.Err.Len() != 0 || len(starts.Started()) != 1 {
		t.Errorf("the session end should start the owed sweep and say nothing, got %d %q %v", code,
			captured.Out.String()+captured.Err.String(), starts.Started())
	}

	code = pieces.Main(context.Background(), []string{"sweep"}, env, captured.Streams(""))
	if code != 0 || test.Exists(t, fence) {
		t.Errorf("the sweep should remove a fence once no older release runs, got %d %q", code, captured.Err.String())
	}
}
