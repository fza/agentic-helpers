package test

import (
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

//go:embed fixtures
var fixtures embed.FS

func Fixture(t *testing.T, name string) string {
	t.Helper()

	sample, secret := strings.CutPrefix(name, "secrets/")
	if secret {
		content, err := fs.ReadFile(SecretFixtures(t), sample)
		if err != nil {
			t.Fatalf("reading fixture %s: %v", name, err)
		}

		return string(content)
	}

	content, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}

	return string(content)
}

// FixtureTree copies the fixture directory name, every file in it, to dest.
func FixtureTree(t *testing.T, name string, dest string) {
	t.Helper()

	FixtureTreeReplacing(t, name, dest, strings.NewReplacer())
}

// FixtureTreeReplacing copies the fixture directory name to dest as
// FixtureTree does, each file's content passed through replacer, so a fixture
// can name a path only the test knows.
func FixtureTreeReplacing(t *testing.T, name string, dest string, replacer *strings.Replacer) {
	t.Helper()

	root := "fixtures/" + name

	err := fs.WalkDir(fixtures, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		content, err := fixtures.ReadFile(path)
		if err != nil {
			return err
		}

		WriteFile(t, filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(path, root+"/"))),
			replacer.Replace(string(content)))

		return nil
	})
	if err != nil {
		t.Fatalf("copying fixture %s: %v", name, err)
	}
}

// The credential samples are stored base64-encoded, each as <name>.b64:
// GitHub push protection refuses a push holding provider-shaped tokens, and a
// repository's secret_scanning.yml paths-ignore does not lift it. Encoded,
// no sample is a token in any committed blob.
const encodedSuffix = ".b64"

// SecretFixtures is the credential samples the secrets scanner and every
// refusal built on it are tested against, decoded: positive/, negative/,
// clean.txt and gitleaks' own per-rule samples under gitleaks/.
func SecretFixtures(t *testing.T) fs.FS {
	t.Helper()

	tree := fstest.MapFS{}
	root := "fixtures/secrets"

	err := fs.WalkDir(fixtures, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}

		name, encoded := strings.CutSuffix(strings.TrimPrefix(path, root+"/"), encodedSuffix)
		if !encoded {
			return fmt.Errorf("%s is not stored encoded", path)
		}

		content, err := fixtures.ReadFile(path)
		if err != nil {
			return err
		}

		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(content)))
		if err != nil {
			return fmt.Errorf("decoding %s: %w", path, err)
		}

		tree[name] = &fstest.MapFile{Data: decoded}

		return nil
	})
	if err != nil {
		t.Fatalf("reading the secret fixtures: %v", err)
	}

	return tree
}

// SecretFixture is one credential sample, decoded and trimmed of surrounding
// blank space.
func SecretFixture(t *testing.T, name string) string {
	t.Helper()

	content, err := fs.ReadFile(SecretFixtures(t), name)
	if err != nil {
		t.Fatalf("reading secret fixture %s: %v", name, err)
	}

	return strings.TrimSpace(string(content))
}
