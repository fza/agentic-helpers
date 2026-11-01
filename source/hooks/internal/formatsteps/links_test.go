package formatsteps_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestWriteNeverWritesAtAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "stores", "s1", "memory")
			outside := test.Outside(t)
			planting.Plant(t, filepath.Join(dir, formatsteps.FileName), outside)

			_ = formatsteps.Write(root, dir, 2)

			if !test.Untouched(t, outside) {
				t.Error("writing a format should never write outside the root")
			}
		})
	}
}
