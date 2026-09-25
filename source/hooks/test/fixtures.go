package test

import (
	"embed"
	"testing"
)

//go:embed fixtures
var fixtures embed.FS

func Fixture(t *testing.T, name string) string {
	t.Helper()

	content, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	return string(content)
}
