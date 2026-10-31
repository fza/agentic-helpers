package memory

import "testing"

func TestRelativeKeepsADotStartingAPath(t *testing.T) {
	root := "/repo"

	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"a dot folder", ".tmp/x", ".tmp/x"},
		{"a dot folder ending a sentence", ".sdd/config.yaml.", ".sdd/config.yaml"},
		{"a dot folder in quotes", `".github/workflows/release.yml"`, ".github/workflows/release.yml"},
		{"a dot folder in parentheses", "(.tmp/x),", ".tmp/x"},
		{"a dot file", ".editorconfig", ".editorconfig"},
		{"a path from the current folder", "./internal/gate.go", "internal/gate.go"},
		{"a plain path ending a clause", "internal/gate.go;", "internal/gate.go"},
		{"a path with a line suffix", "internal/gate.go:12", "internal/gate.go"},
		{"an absolute path below the root", "/repo/.tmp/x", ".tmp/x"},
		{"a path leaving the root", "../other/x", ""},
		{"the root itself", ".", ""},
		{"an ellipsis", "...", ""},
		{"a url", "https://example.com/x", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relative(root, tc.token); got != tc.want {
				t.Errorf("a token should name its path inside the root with its punctuation trimmed, got %q", got)
			}
		})
	}
}
