package tools

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// Writers trust no path a store already holds: a sandboxed process can plant
// a link wherever the next write would go, or swap a checked folder for one
// before the write opens it. So a write never opens a path: it holds the
// descriptor of each folder, opened relative to its verified parent as reads
// do, and creates, appends and renames relative to that descriptor. A file is
// created exclusively, a replacement goes through a fresh name renamed over
// the old one, and an appended file must be the regular file its folder names.

// ensureDir opens relative below root, making each missing folder relative to
// its parent's descriptor. root itself must be no link.
func ensureDir(root string, relative string) (*dir, error) {
	err := os.MkdirAll(root, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", root, err)
	}

	current, err := openDir(root)
	if err != nil {
		return nil, err
	}

	for _, name := range strings.Split(filepath.ToSlash(relative), "/") {
		if name == "" || name == "." {
			continue
		}

		next, err := current.ensureSub(name)
		current.close()

		if err != nil {
			return nil, err
		}

		current = next
	}

	return current, nil
}

// ensureSub opens the folder name below held, making it first where it is
// missing.
func (held *dir) ensureSub(name string) (*dir, error) {
	if !singleName(name) {
		return nil, fmt.Errorf("%s names no single folder", path.Join(held.path, name))
	}

	err := held.root.Mkdir(name, 0o700)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("creating %s: %w", path.Join(held.path, name), err)
	}

	return held.sub(name)
}

// ensureSubPath is ensureSub for each component of a slash path.
func (held *dir) ensureSubPath(relative string) (*dir, error) {
	current := held

	for _, name := range strings.Split(relative, "/") {
		if name == "" || name == "." {
			continue
		}

		next, err := current.ensureSub(name)
		if current != held {
			current.close()
		}

		if err != nil {
			return nil, err
		}

		current = next
	}

	if current == held {
		return held.sub(".")
	}

	return current, nil
}

// create writes a new file below held, refusing one already there, a link
// included: O_EXCL never opens an existing name.
func (held *dir) create(name string, data []byte) error {
	at := path.Join(held.path, name)
	if !singleName(name) {
		return fmt.Errorf("%s names no single file", at)
	}

	file, err := held.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", at, err)
	}

	_, err = file.Write(data)
	closeErr := file.Close()

	if err != nil {
		return fmt.Errorf("writing %s: %w", at, err)
	}

	if closeErr != nil {
		return fmt.Errorf("closing %s: %w", at, closeErr)
	}

	return nil
}

// replace writes name below held through a fresh name renamed over it: the
// fresh name is never one a planted link holds, and a rename replaces a link
// at name rather than writing through it.
func (held *dir) replace(name string, data []byte) error {
	random := make([]byte, 8)

	_, err := rand.Read(random)
	if err != nil {
		return fmt.Errorf("naming a temporary file: %w", err)
	}

	temporary := "." + name + "-" + hex.EncodeToString(random)

	err = held.create(temporary, data)
	if err != nil {
		_ = held.root.Remove(temporary)

		return err
	}

	err = held.root.Rename(temporary, name)
	if err != nil {
		_ = held.root.Remove(temporary)

		return fmt.Errorf("placing %s: %w", path.Join(held.path, name), err)
	}

	return nil
}

// appendTo opens name below held for appending. The descriptor must be the
// regular file the folder names: os.Root follows a link inside its tree, so
// one found there is refused before anything is written.
func (held *dir) appendTo(name string) (*os.File, error) {
	at := path.Join(held.path, name)
	if !singleName(name) {
		return nil, fmt.Errorf("%s names no single file", at)
	}

	file, err := held.root.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", at, err)
	}

	opened, err := file.Stat()
	if err == nil {
		var checked fs.FileInfo

		checked, err = held.fileInfo(name)
		if err == nil && (!opened.Mode().IsRegular() || !os.SameFile(checked, opened) || hardLinked(opened)) {
			err = fmt.Errorf("%s %w", at, ErrMoved)
		}
	}

	if err != nil {
		_ = file.Close()

		return nil, err
	}

	return file, nil
}
