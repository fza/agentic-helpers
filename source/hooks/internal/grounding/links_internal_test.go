package grounding

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTheLedgerNeverLandsAtAPlantedLink(t *testing.T) {
	for _, planting := range test.Plantings() {
		t.Run(planting.Name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, ".sdd", "tmp", "grounding-gate")
			outside := test.Outside(t)
			planting.Plant(t, filepath.Join(dir, test.Session+".json"), outside)

			path, err := ledgerPath(root, dir, hookio.Payload{SessionID: test.Session})
			if err == nil {
				err = saveEvidence(context.Background(), root, path, freshEvidence())
			}

			if !test.Untouched(t, outside) {
				t.Error("the ledger should never be written outside the project")
			}

			if err == nil && planting.Name != test.Plantings()[0].Name {
				t.Error("a ledger write through a planted folder link or onto a hard link should be refused")
			}
		})
	}
}
