package test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Member is one member of a tar.gz archive.
type Member struct {
	Name string
	Data []byte
}

// ArchiveMembers reads every member of the tar.gz archive at path, in order.
func ArchiveMembers(t *testing.T, path string) []Member {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("opening %s as gzip: %v", path, err)
	}

	archive := tar.NewReader(compressed)

	var members []Member

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return members
		}

		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		content, err := io.ReadAll(archive)
		if err != nil {
			t.Fatalf("reading %s in %s: %v", header.Name, path, err)
		}

		members = append(members, Member{Name: header.Name, Data: content})
	}
}

// WriteArchive writes members as a tar.gz archive at path.
func WriteArchive(t *testing.T, path string, members []Member) {
	t.Helper()

	var packed bytes.Buffer

	compressed := gzip.NewWriter(&packed)
	archive := tar.NewWriter(compressed)

	for _, member := range members {
		err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: member.Name, Size: int64(len(member.Data)),
			Mode: 0o600, Format: tar.FormatPAX})
		if err != nil {
			t.Fatalf("adding %s: %v", member.Name, err)
		}

		_, err = archive.Write(member.Data)
		if err != nil {
			t.Fatalf("adding %s: %v", member.Name, err)
		}
	}

	if archive.Close() != nil || compressed.Close() != nil {
		t.Fatalf("closing the archive %s", path)
	}

	WriteFile(t, path, packed.String())
}

// TreeState is every entry below root: a file's mode and bytes, a folder's
// mode, a link's target. Two equal states are a tree nothing changed.
func TreeState(t *testing.T, root string) map[string]string {
	t.Helper()

	state := map[string]string{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return filepath.SkipAll
		}

		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}

			state[path] = "link " + target
		case entry.IsDir():
			state[path] = "dir " + info.Mode().String()
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			state[path] = info.Mode().String() + " " + string(content)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("reading the tree %s: %v", root, err)
	}

	return state
}
