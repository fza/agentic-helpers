package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestADevelopmentBuildWritesAFormatOnlyWhereTheCheckoutOptedIn(t *testing.T) {
	const older = `{"format":7}` + "\n"

	cases := []struct {
		name    string
		version string
		binDir  string
		refused bool
	}{
		{name: "reached through another project's PATH", version: "dev", refused: true},
		{name: "run by the checkout that opted in", version: "dev", binDir: "/checkout/.claude/bin"},
		{name: "a release", version: "2.25.0"},
	}

	for _, tc := range cases {
		getenv := func(key string) string {
			if key == binDirVariable {
				return tc.binDir
			}

			return ""
		}

		t.Run("migrating/"+tc.name, func(t *testing.T) {
			mem := migrateMemory(t)
			mem.env.Version, mem.env.Getenv = tc.version, getenv

			path := filepath.Join(mem.store(), "format.json")
			test.WriteFile(t, path, older)

			err := mem.migrate(context.Background())

			written, _ := os.ReadFile(path)
			if tc.refused && (!errors.Is(err, ErrDevelopmentBuild) || string(written) != older) {
				t.Errorf("the store should stay as it is and the build be refused, got %v, %s", err, written)
			}

			if !tc.refused && (err != nil || string(written) == older) {
				t.Errorf("the store should move on, got %v, %s", err, written)
			}
		})

		t.Run("creating/"+tc.name, func(t *testing.T) {
			mem := migrateMemory(t)
			mem.env.Version, mem.env.Getenv = tc.version, getenv

			err := mem.initStore()
			if tc.refused != errors.Is(err, ErrDevelopmentBuild) {
				t.Errorf("only a build outside the opted-in checkout should be refused a new store, got %v", err)
			}

			if tc.refused && mem.storeExists() {
				t.Error("a refused build should leave no store behind")
			}
		})
	}
}
