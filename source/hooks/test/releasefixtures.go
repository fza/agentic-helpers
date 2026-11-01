package test

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// all: keeps the dotted files a store holds, its size ledger among them.
//
//go:embed all:fixtures/releases
var releaseFixtures embed.FS

const (
	releasesRoot = "fixtures/releases"
	workMark     = "@WORK@"
	packedSuffix = ".fixture-gz"
)

// ReleaseRead is one read scripts/release-fixture recorded: what ran, as
// which session, and the JSON the release answered.
type ReleaseRead struct {
	Binary  string          `json:"binary"`
	Args    []string        `json:"args"`
	Session string          `json:"session"`
	Out     json.RawMessage `json:"out"`
}

// ReleaseDump is a release fixture's dump.json.
type ReleaseDump struct {
	Release  string        `json:"release"`
	At       time.Time     `json:"at"`
	Entries  []string      `json:"entries"`
	Commands []ReleaseRead `json:"commands"`
}

// ReleaseVersions is every release with a committed fixture.
func ReleaseVersions(t *testing.T) []string {
	t.Helper()

	entries, err := fs.ReadDir(releaseFixtures, releasesRoot)
	if err != nil {
		t.Fatalf("listing the release fixtures: %v", err)
	}

	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		versions = append(versions, entry.Name())
	}

	return versions
}

// ReleaseDumpOf is the dump.json of the release's fixture.
func ReleaseDumpOf(t *testing.T, version string) ReleaseDump {
	t.Helper()

	data, err := releaseFixtures.ReadFile(releasesRoot + "/" + version + "/dump.json")
	if err != nil {
		t.Fatalf("reading the dump of %s: %v", version, err)
	}

	var dump ReleaseDump

	err = json.Unmarshal(data, &dump)
	if err != nil {
		t.Fatalf("parsing the dump of %s: %v", version, err)
	}

	return dump
}

// ReleaseTree lays the release's fixture into root as the script left it:
// data and cache under the home root/home, the checkout at root/repo, root
// standing wherever a text file names the folder the release ran in.
func ReleaseTree(t *testing.T, version string, root string) {
	t.Helper()

	for from, to := range map[string]string{
		"data":     filepath.Join("home", ".local", "share", "agentic-helpers"),
		"cache":    filepath.Join("home", ".cache", "agentic-helpers"),
		"checkout": "repo",
	} {
		base := releasesRoot + "/" + version + "/" + from

		err := fs.WalkDir(releaseFixtures, base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}

			content, err := releaseFixtures.ReadFile(path)
			if err != nil {
				return err
			}

			name := strings.TrimPrefix(path, base+"/")

			packed, isPacked := strings.CutSuffix(name, packedSuffix)
			if isPacked {
				name = packed

				content, err = unpacked(content)
				if err != nil {
					return err
				}
			}

			if !bytes.ContainsRune(content, 0) && utf8.Valid(content) {
				content = bytes.ReplaceAll(content, []byte(workMark), []byte(root))
			}

			WriteFile(t, filepath.Join(root, to, filepath.FromSlash(name)), string(content))

			return nil
		})
		if err != nil {
			t.Fatalf("laying %s of %s: %v", from, version, err)
		}
	}

	WriteFile(t, filepath.Join(root, "repo", ".git", "HEAD"), "ref: refs/heads/main\n")
}

func unpacked(content []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}

	return io.ReadAll(reader)
}
