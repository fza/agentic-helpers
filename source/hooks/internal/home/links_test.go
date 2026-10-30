package home_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestBindingNeverWritesAtAPlantedLink(t *testing.T) {
	targets := []struct {
		name   string
		target func(m *machine, app string) string
		// refused says the binding fails: the home's lock, and every store's
		// folder, are needed to bind at all.
		refused func(planting test.Planting) bool
		// rootLevel says the file sits in a root, whose own link is the
		// owner's: a link at its folder is followed by design.
		rootLevel bool
	}{
		{
			name:      "the home's lock",
			target:    func(m *machine, _ string) string { return filepath.Join(m.roots.Data, ".lock") },
			refused:   func(test.Planting) bool { return true },
			rootLevel: true,
		},
		{
			name:      "the index",
			target:    func(m *machine, _ string) string { return filepath.Join(m.roots.Data, "index.json") },
			refused:   func(planting test.Planting) bool { return planting.Name == test.Plantings()[2].Name },
			rootLevel: true,
		},
		{
			name:    "the stores folder",
			target:  func(m *machine, _ string) string { return filepath.Join(m.roots.Stores(), "planted") },
			refused: func(planting test.Planting) bool { return planting.Folder },
		},
		{
			name:      "the pointer in the git common dir",
			target:    func(_ *machine, app string) string { return filepath.Join(app, ".git", home.PointerName) },
			refused:   func(test.Planting) bool { return false },
			rootLevel: true,
		},
	}

	for _, each := range targets {
		for _, planting := range test.Plantings() {
			if planting.Folder && each.rootLevel {
				continue
			}

			t.Run(each.name+", "+planting.Name, func(t *testing.T) {
				m := newMachine(t)
				app := m.checkout("app", "github.com/acme/app", "")
				outside := test.Outside(t)
				planting.Plant(t, each.target(m, app), outside)

				_, err := m.resolver().Ensure(context.Background(), m.inspect(app))

				if !test.Untouched(t, outside) {
					t.Error("binding should never write outside the home")
				}

				if err == nil && each.refused(planting) {
					t.Error("binding through a planted link should be refused")
				}
			})
		}
	}
}
