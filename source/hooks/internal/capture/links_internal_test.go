package capture

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheCaptureLockNeverOpensAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			graph := filepath.Join(root, ".sdd")
			outside := test.Outside(t)
			planting.Plant(t, filepath.Join(graph, "tmp", "capture.lock"), outside)

			unlock, err := (&capture{root: root, graph: graph}).lock()
			if err == nil {
				unlock()
				t.Error("a capture lock at a planted link should be refused")
			}

			if !test.Untouched(t, outside) {
				t.Error("the capture lock should never be opened outside the project")
			}
		})
	}
}
