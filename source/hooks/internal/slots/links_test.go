package slots_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestAPutNeverWritesAtAPlantedLink(t *testing.T) {
	for _, name := range []string{"sessions.slots", "sessions.slots.lock"} {
		for _, planting := range test.Plantings() {
			t.Run(name+", "+planting.Name, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, "stores", "s1", "tools")
				outside := test.Outside(t)
				planting.Plant(t, filepath.Join(dir, name), outside)

				err := slots.Open(root, filepath.Join(dir, "sessions.slots"), width).Put(context.Background(), "s1", []byte("x"))

				if !test.Untouched(t, outside) {
					t.Error("a put should never write outside the cache")
				}

				if err == nil {
					t.Error("a put through a planted link should be refused")
				}
			})
		}
	}
}
