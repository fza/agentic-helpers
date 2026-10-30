package space_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestARefreshNeverWritesAtAPlantedLink(t *testing.T) {
	files := []struct {
		name     string
		replaces bool
	}{
		{name: space.LedgerName, replaces: true},
		{name: space.LedgerName + ".lock"},
	}

	for _, file := range files {
		for _, planting := range test.Plantings() {
			t.Run(file.name+", "+planting.Name, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, "stores", "s1", "tools")
				test.WriteFile(t, filepath.Join(dir, "data"), "x")

				outside := test.Outside(t)
				planting.Plant(t, filepath.Join(dir, file.name), outside)

				err := space.Refresh(context.Background(), root, dir, nil)

				if !test.Untouched(t, outside) {
					t.Error("a refresh should never write outside the home")
				}

				if err == nil && (planting.Folder || !file.replaces) {
					t.Error("a refresh through a planted link should be refused")
				}
			})
		}
	}
}
