package export

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	manifestName   = "manifest.json"
	storesDir      = "stores/"
	attachmentsDir = "attachments/"
	identityName   = "identity.json"
)

var ErrInvalidArchive = errors.New("not a whole agent-memory-export archive")

// Manifest is the archive's `manifest.json`.
type Manifest struct {
	Format      string               `json:"format"`
	Version     int                  `json:"version"`
	Exported    string               `json:"exported"`
	Producer    string               `json:"producer"`
	Stores      []ManifestStore      `json:"stores"`
	Attachments *ManifestAttachments `json:"attachments,omitempty"`
}

type ManifestStore struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Unclaimed bool           `json:"unclaimed,omitempty"`
	Formats   map[string]int `json:"formats,omitempty"`
	// MigratedFrom is, by component, the format the store held where the
	// export migrated a copy of it to read it.
	MigratedFrom map[string]int `json:"migrated_from,omitempty"`
	Counts       map[string]int `json:"counts,omitempty"`
}

type ManifestAttachments struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

var (
	sha256Name = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// lineFiles are the store files counted by line, by their manifest stem.
	lineFiles = map[string]string{
		"seats.jsonl": "seats", "shelves.jsonl": "shelves", "entries.jsonl": "entries", "names.jsonl": "names",
		"topic-merges.jsonl": "topic-merges", "state.jsonl": "state",
	}
	sliceName       = regexp.MustCompile(`^slices/s-[0-9a-z]{6}\.txt$`)
	toolMetaName    = regexp.MustCompile(`^tools/[a-z][a-z0-9]*(-[a-z0-9]+)*/meta\.json$`)
	toolJournalName = regexp.MustCompile(`^tools/[a-z][a-z0-9]*(-[a-z0-9]+)*/(history|takes|pruned)\.jsonl$`)
	toolVersionName = regexp.MustCompile(`^tools/[a-z][a-z0-9]*(-[a-z0-9]+)*/v[1-9][0-9]*/(.+)$`)
)

// tally is what the members of one store hold, counted as the manifest
// counts them.
type tally struct {
	identity bool
	counts   map[string]int
}

// Verify reads the archive at path whole: one gzip stream holding one pax
// tar, `manifest.json` first and of this format, every member a regular file
// or folder at a path the layout allows, each JSON Lines member a run of
// objects, every attachment's bytes matching its name, every attachment an
// entry says is stored present, and every count the manifest gives matching
// what the members hold.
func Verify(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("opening %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	manifest, err := verify(file)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s is %w: %w", path, ErrInvalidArchive, err)
	}

	return manifest, nil
}

func verify(reader io.Reader) (Manifest, error) {
	compressed, err := gzip.NewReader(bufio.NewReader(reader))
	if err != nil {
		return Manifest{}, err
	}

	compressed.Multistream(false)

	archive := tar.NewReader(compressed)

	var (
		manifest  Manifest
		read      bool
		tallies   = map[string]*tally{}
		seen      = map[string]bool{}
		attached  = map[string]bool{}
		needed    = map[string]bool{}
		files     int
		fileBytes int64
	)

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return Manifest{}, err
		}

		name := strings.TrimSuffix(header.Name, "/")

		switch {
		case !memberPath(name):
			return Manifest{}, fmt.Errorf("member %q has a path the format refuses", header.Name)
		case seen[name]:
			return Manifest{}, fmt.Errorf("member %s is there twice", name)
		case header.Typeflag == tar.TypeDir:
			seen[name] = true

			continue
		case header.Typeflag != tar.TypeReg:
			return Manifest{}, fmt.Errorf("member %s is no regular file or folder", name)
		}

		seen[name] = true

		if !read {
			manifest, err = readManifest(name, archive)
			if err != nil {
				return Manifest{}, err
			}

			read = true

			for _, store := range manifest.Stores {
				tallies[store.ID] = &tally{counts: map[string]int{}}
			}

			continue
		}

		if sha, found := strings.CutPrefix(name, attachmentsDir); found {
			size, err := checkAttachment(sha, archive)
			if err != nil {
				return Manifest{}, err
			}

			attached[sha] = true
			files++
			fileBytes += size

			continue
		}

		err = checkStoreMember(name, archive, tallies, needed)
		if err != nil {
			return Manifest{}, err
		}
	}

	if !read {
		return Manifest{}, errors.New("it holds no manifest.json")
	}

	// The tar ends before the gzip stream does: read the rest, so a stream
	// cut short or failing its checksum is caught.
	_, err = io.Copy(io.Discard, compressed)
	if err != nil {
		return Manifest{}, err
	}

	return manifest, checkTallies(manifest, tallies, attached, needed, files, fileBytes)
}

func readManifest(name string, archive io.Reader) (Manifest, error) {
	if name != manifestName {
		return Manifest{}, fmt.Errorf("its first member is %s, not %s", name, manifestName)
	}

	var manifest Manifest

	data, err := io.ReadAll(archive)
	if err != nil {
		return Manifest{}, err
	}

	err = json.Unmarshal(data, &manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("reading %s: %w", manifestName, err)
	}

	switch {
	case manifest.Format != FormatName:
		return Manifest{}, fmt.Errorf("%s names the format %q", manifestName, manifest.Format)
	case manifest.Version != Version:
		return Manifest{}, fmt.Errorf("%s names version %d; this release reads version %d", manifestName, manifest.Version, Version)
	case manifest.Exported == "" || manifest.Producer == "" || manifest.Stores == nil:
		return Manifest{}, fmt.Errorf("%s lacks a required field", manifestName)
	}

	listed := map[string]bool{}

	for _, store := range manifest.Stores {
		if store.ID == "" || !memberPath(store.ID) || strings.Contains(store.ID, "/") || listed[store.ID] {
			return Manifest{}, fmt.Errorf("%s lists the store %q wrongly or twice", manifestName, store.ID)
		}

		listed[store.ID] = true
	}

	return manifest, nil
}

func checkAttachment(sha string, archive io.Reader) (int64, error) {
	if !sha256Name.MatchString(sha) {
		return 0, fmt.Errorf("member %s%s is named for no SHA-256", attachmentsDir, sha)
	}

	digest := sha256.New()

	size, err := io.Copy(digest, archive)
	if err != nil {
		return 0, err
	}

	if hex.EncodeToString(digest.Sum(nil)) != sha {
		return 0, fmt.Errorf("member %s%s does not hold the bytes it is named for", attachmentsDir, sha)
	}

	return size, nil
}

type entryAttachments struct {
	Attachments []struct {
		SHA256 string `json:"sha256"`
		Stored bool   `json:"stored"`
	} `json:"attachments"`
}

func checkStoreMember(name string, archive io.Reader, tallies map[string]*tally, needed map[string]bool) error {
	rest, found := strings.CutPrefix(name, storesDir)
	id, member, nested := strings.Cut(rest, "/")

	held := tallies[id]

	switch {
	case !found || !nested:
		return fmt.Errorf("member %s lies outside the layout", name)
	case held == nil:
		return fmt.Errorf("member %s belongs to a store the manifest does not list", name)
	}

	data, err := io.ReadAll(archive)
	if err != nil {
		return err
	}

	switch {
	case member == identityName:
		var identity struct {
			ID string `json:"id"`
		}

		err = json.Unmarshal(data, &identity)
		if err != nil || identity.ID != id {
			return fmt.Errorf("member %s names no store %s", name, id)
		}

		held.identity = true
	case lineFiles[member] != "":
		lines, err := objectLines(name, data, func(line []byte) {
			if member != "entries.jsonl" {
				return
			}

			var refs entryAttachments
			if json.Unmarshal(line, &refs) == nil {
				for _, ref := range refs.Attachments {
					if ref.Stored {
						needed[ref.SHA256] = true
					}
				}
			}
		})
		if err != nil {
			return err
		}

		held.counts[lineFiles[member]] += lines
	case sliceName.MatchString(member):
		held.counts["slices"]++
	case toolMetaName.MatchString(member):
		if !json.Valid(data) {
			return fmt.Errorf("member %s holds no JSON object", name)
		}

		held.counts["tools"]++
	case toolJournalName.MatchString(member):
		_, err = objectLines(name, data, nil)
		if err != nil {
			return err
		}
	case toolVersionName.MatchString(member):
		if strings.HasSuffix(member, "/tool.json") && strings.Count(member, "/") == 3 {
			held.counts["tool_versions"]++
		}
	default:
		return fmt.Errorf("member %s lies outside the layout", name)
	}

	return nil
}

// objectLines counts the lines of a JSON Lines member, each a JSON object
// ending in a newline, handing each to each where given.
func objectLines(name string, data []byte, each func(line []byte)) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	if !bytes.HasSuffix(data, []byte("\n")) {
		return 0, fmt.Errorf("member %s does not end its last line", name)
	}

	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))

	for index, line := range lines {
		var object map[string]json.RawMessage

		err := json.Unmarshal(line, &object)
		if err != nil || object == nil {
			return 0, fmt.Errorf("line %d of member %s is no JSON object", index+1, name)
		}

		if each != nil {
			each(line)
		}
	}

	return len(lines), nil
}

func checkTallies(manifest Manifest, tallies map[string]*tally, attached map[string]bool, needed map[string]bool,
	files int, fileBytes int64,
) error {
	for _, store := range manifest.Stores {
		held := tallies[store.ID]
		if !held.identity {
			return fmt.Errorf("the store %s has no %s", store.ID, identityName)
		}

		for _, key := range slices.Sorted(maps.Keys(store.Counts)) {
			if held.counts[key] != store.Counts[key] {
				return fmt.Errorf("the store %s holds %d %s, and the manifest counts %d", store.ID, held.counts[key],
					key, store.Counts[key])
			}
		}
	}

	for _, sha := range slices.Sorted(maps.Keys(needed)) {
		if !attached[sha] {
			return fmt.Errorf("an entry names the attachment %s as stored, and the archive does not hold it", sha)
		}
	}

	if manifest.Attachments != nil && (manifest.Attachments.Files != files || manifest.Attachments.Bytes != fileBytes) {
		return fmt.Errorf("the archive holds %d attachments of %d bytes, and the manifest counts %d of %d", files,
			fileBytes, manifest.Attachments.Files, manifest.Attachments.Bytes)
	}

	return nil
}
