package vectorseg_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAWriteNeverLandsAtAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "stores", "s1", "memory", "segments")
			outside := test.Outside(t)
			planting.Plant(t, filepath.Join(dir, "segments.lock"), outside)

			err := vectorseg.Open(root, dir).Write([]vectorseg.Row{{ID: "m-one", Hash: "h1", Chunks: [][]float32{unit(4, 0)}}},
				everything)

			if !test.Untouched(t, outside) {
				t.Error("a segment write should never land outside the cache")
			}

			if err == nil {
				t.Error("a segment write through a planted link should be refused")
			}
		})
	}
}
