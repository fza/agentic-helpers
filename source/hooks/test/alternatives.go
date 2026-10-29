package test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Stubs is a directory holding an executable stub for each name, for a
// fake PATH: nothing ever runs one, a lookup only finds it, and it carries no
// flavour marker.
func Stubs(t *testing.T, names ...string) string {
	t.Helper()

	binaries := map[string]string{}
	for _, name := range names {
		binaries[name] = "unmarked"
	}

	return Binaries(t, binaries)
}

// Binaries is a directory holding, for each name, an executable with the
// content of the fixture under fixtures/alternatives it maps to: `gnu-sed`,
// `bsd-sed`, `gnu-date`, `bsd-date` carry their flavour's marker, `unmarked`
// none.
func Binaries(t *testing.T, binaries map[string]string) string {
	t.Helper()

	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the stub directory: %v", err)
	}

	for name, fixture := range binaries {
		err = os.WriteFile(filepath.Join(directory, name), []byte(Fixture(t, "alternatives/"+fixture)), 0o755)
		if err != nil {
			t.Fatalf("writing the stub %s: %v", name, err)
		}
	}

	return directory
}

// FlavouredPath is a directory holding each installed name: sed and date as
// GNU's where gnu names them and BSD's otherwise, every other name unmarked.
func FlavouredPath(t *testing.T, installed []string, gnu []string) string {
	t.Helper()

	binaries := map[string]string{}

	for _, name := range installed {
		binaries[name] = "unmarked"

		if name == "sed" || name == "date" {
			binaries[name] = "bsd-" + name
			if slices.Contains(gnu, name) {
				binaries[name] = "gnu-" + name
			}
		}
	}

	return Binaries(t, binaries)
}

// Getenv answers PATH with path and every other variable with nothing.
func Getenv(path string) func(string) string {
	return func(key string) string {
		if key == "PATH" {
			return path
		}

		return ""
	}
}
