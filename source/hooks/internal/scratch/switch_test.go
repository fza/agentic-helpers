package scratch_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheSweepOffOnTheMachine(t *testing.T) {
	root := project(t)
	mine := sessionDir(t, root, test.Session, 0)

	settings := filepath.Join(t.TempDir(), "settings.json")
	test.WriteFile(t, settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"scratch_enabled":false}}}}`)

	captured := &test.Captured{}
	code := scratch.Main(context.Background(), []string{"end"}, scratch.Env{Root: root, Settings: []string{settings}},
		captured.Streams(`{"session_id":"`+test.Session+`"}`))

	if code != 0 || captured.Out.String() != "" || captured.Err.String() != "" || !test.Exists(t, mine) {
		t.Errorf("the sweep off on the machine should remove nothing and say nothing, got %d %q %q", code,
			captured.Out.String(), captured.Err.String())
	}
}
