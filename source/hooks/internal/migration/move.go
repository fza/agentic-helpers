package migration

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

var ErrCopyDiffers = errors.New("the copy differs from its source")

// move moves the tree at from to to: by rename on one volume, and across
// volumes by a copy checked against its source, which waits beside its old
// place, under aside, until the run completes, so an undo never needs what
// only a half-written copy holds.
func (log *journal) move(from string, to string, aside string) error {
	err := os.MkdirAll(filepath.Dir(to), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(to), err)
	}

	err = log.record(step{Kind: stepRename, From: from, To: to})
	if err != nil {
		return err
	}

	err = log.rename(from, to)
	if err == nil {
		return nil
	}

	if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("moving %s: %w", from, err)
	}

	err = log.record(step{Kind: stepRename, From: from, To: aside})
	if err != nil {
		return err
	}

	err = os.Rename(from, aside)
	if err != nil {
		return fmt.Errorf("moving %s aside: %w", from, err)
	}

	err = log.created(to)
	if err != nil {
		return err
	}

	err = copyChecked(aside, to, log.checkpoint)
	if err != nil {
		return fmt.Errorf("copying %s to %s: %w", from, to, err)
	}

	return log.record(step{Kind: stepDiscard, Path: aside})
}

// copyChecked copies the tree at from to to, then reads both back and
// refuses where any file, folder or link differs.
func copyChecked(from string, to string, tick func() error) error {
	err := copyTree(from, to, tick)
	if err != nil {
		return err
	}

	source, err := describe(from)
	if err != nil {
		return err
	}

	copied, err := describe(to)
	if err != nil {
		return err
	}

	if !bytes.Equal(source, copied) {
		return ErrCopyDiffers
	}

	return nil
}

// copyTree copies the tree at from to to, calling tick after each file.
func copyTree(from string, to string, tick func() error) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}

		target := filepath.Join(to, relative)

		info, err := entry.Info()
		if err != nil {
			return err
		}

		switch {
		case entry.IsDir():
			return os.Mkdir(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}

			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			copyErr := copyFile(path, target, info.Mode().Perm())
			if copyErr != nil {
				return copyErr
			}

			return tick()
		}

		return nil
	})
}

func copyFile(from string, to string, perm fs.FileMode) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}

	defer func() { _ = source.Close() }()

	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}

	_, err = io.Copy(target, source)

	return errors.Join(err, target.Close())
}

// describe is every entry below root by path, kind, mode and content hash.
func describe(root string) ([]byte, error) {
	var described bytes.Buffer

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		fmt.Fprintf(&described, "%s %v", relative, info.Mode().Type())

		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}

			fmt.Fprintf(&described, " %s", link)
		case info.Mode().IsRegular():
			sum, sumErr := hashFile(path)
			if sumErr != nil {
				return sumErr
			}

			fmt.Fprintf(&described, " %o %x", info.Mode().Perm(), sum)
		}

		described.WriteByte('\n')

		return nil
	})

	return described.Bytes(), err
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	hash := sha256.New()

	_, err = io.Copy(hash, file)
	if err != nil {
		return nil, err
	}

	return hash.Sum(nil), nil
}

// copyFileInto copies one small file to to, creating its folder.
func copyFileInto(from string, to string) error {
	data, err := readSmall(from)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(to), 0o700)
	if err != nil {
		return err
	}

	return os.WriteFile(to, data, 0o600)
}
