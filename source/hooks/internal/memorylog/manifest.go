package memorylog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// formerFormat is the version the manifest carried while it had one. The
// store's format lives in the store's own format.json now; a manifest holding
// this number still reads.
const formerFormat = 1

var segmentName = regexp.MustCompile(`^[0-9a-f]{8}\.seg$`)

type manifest struct {
	Format   int      `json:"format,omitempty"`
	Segments []string `json:"segments"`
	Sealing  *sealing `json:"sealing,omitempty"`
}

// sealing names the tail bytes the last listed segment holds, while the tail
// may still hold them too.
type sealing struct {
	TailSHA256 string `json:"tail_sha256"`
	TailSize   int64  `json:"tail_size"`
	// Legacy says the sealed bytes are those of a `tail.jsonl` an earlier
	// release left, not of the journal tail.
	Legacy bool `json:"legacy,omitempty"`
}

func newSealing(tail []byte) *sealing {
	digest := sha256.Sum256(tail)

	return &sealing{TailSHA256: hex.EncodeToString(digest[:]), TailSize: int64(len(tail))}
}

// strip drops the sealed bytes from the front of tail, reporting whether tail
// still held them.
func (s *sealing) strip(tail []byte) ([]byte, bool) {
	if s == nil || int64(len(tail)) < s.TailSize {
		return tail, false
	}

	digest := sha256.Sum256(tail[:s.TailSize])
	if hex.EncodeToString(digest[:]) != s.TailSHA256 {
		return tail, false
	}

	return tail[s.TailSize:], true
}

// readManifest reads the manifest at path along with the identity of the file
// it read, nil where none exists yet.
func readManifest(path string) (manifest, os.FileInfo, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return manifest{}, nil, nil
	}

	if err != nil {
		return manifest{}, nil, fmt.Errorf("opening the manifest: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return manifest{}, nil, fmt.Errorf("reading the manifest: %w", err)
	}

	var content bytes.Buffer

	_, err = content.ReadFrom(file)
	if err != nil {
		return manifest{}, nil, fmt.Errorf("reading the manifest: %w", err)
	}

	current, err := decodeManifest(content.Bytes())
	if err != nil {
		return manifest{}, nil, err
	}

	return current, info, nil
}

func decodeManifest(content []byte) (manifest, error) {
	var current manifest

	err := json.Unmarshal(content, &current)
	if err != nil {
		return manifest{}, fmt.Errorf("%w: decoding the manifest: %w", ErrCorrupt, err)
	}

	if current.Format != 0 && current.Format != formerFormat {
		return manifest{}, fmt.Errorf("%w: manifest format %d", ErrCorrupt, current.Format)
	}

	for _, name := range current.Segments {
		if !segmentName.MatchString(name) {
			return manifest{}, fmt.Errorf("%w: manifest lists %q", ErrCorrupt, name)
		}
	}

	return current, nil
}

func (l *Log) writeManifest(next manifest) error {
	next.Format = 0
	if next.Segments == nil {
		next.Segments = []string{}
	}

	content, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encoding the manifest: %w", err)
	}

	return l.replaceFile(manifestName, content)
}

// sameFile is whether two stats name one unchanged file, or both name none.
func sameFile(before os.FileInfo, after os.FileInfo) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}

	return os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func statFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", path, err)
	}

	return info, nil
}
