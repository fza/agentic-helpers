package migration_test

import (
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAMigrationNeverWritesAtAPlantedLink(t *testing.T) {
	targets := []struct {
		name string
		path func(m *oldMachine) string
		// rootLevel says the file sits in the data root, whose own link is
		// the owner's: a link at its folder is followed by design.
		rootLevel bool
		refused   func(planting test.Planting) bool
	}{
		{
			name:      "the migration lock",
			path:      func(m *oldMachine) string { return filepath.Join(m.env.Roots.Data, ".migration.lock") },
			rootLevel: true,
			refused:   func(test.Planting) bool { return true },
		},
		{
			name:    "the backup folder",
			path:    func(m *oldMachine) string { return filepath.Join(m.env.Roots.Data, "backup", "planted") },
			refused: func(planting test.Planting) bool { return planting.Folder },
		},
	}

	for _, each := range targets {
		for _, planting := range test.Plantings() {
			if planting.Folder && each.rootLevel {
				continue
			}

			t.Run(each.name+", "+planting.Name, func(t *testing.T) {
				m := newOldMachine(t)
				outside := test.Outside(t)
				planting.Plant(t, each.path(m), outside)

				m.prepare()

				if !test.Untouched(t, outside) {
					t.Error("a migration should never write outside the home")
				}

				if each.refused(planting) && test.Exists(t, m.env.Roots.FormatFile()) {
					t.Error("a migration through a planted link should be refused")
				}
			})
		}
	}
}
