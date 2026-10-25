package tools

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
)

var (
	ErrLink       = errors.New("is a link")
	ErrNotRegular = errors.New("is no regular file")
	ErrTooLarge   = errors.New("is over the read limit")
	ErrTooMany    = errors.New("holds more files than a walk reads")
	ErrMoved      = errors.New("changed while it was opened")
	ErrHardLinked = errors.New("has a second name, a hard link; register a copy of it")
)

// Read limits. Every store is untrusted text, so no read is unbounded.
const (
	toolLimit     = 1 << 20
	versionLimit  = toolLimit + 64<<10
	indexLimit    = 4 << 20
	lineFileLimit = 4 << 20
	sessionLimit  = 1 << 20
	currentLimit  = 64
	walkDepth     = 6
	walkFiles     = 2000
)

// dir is a directory opened by descriptor. Every component below it is
// checked with lstat before it is opened relative to its parent, and the
// opened descriptor is held to the checked file, so a link swapped in between
// is caught rather than followed. os.Root alone follows links inside its tree.
type dir struct {
	root *os.Root
	path string
}

// openDir opens a directory the caller names in full, refusing a link at its
// last component.
func openDir(at string) (*dir, error) {
	checked, err := os.Lstat(at)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}

	if !checked.IsDir() {
		return nil, notADirectory(at, checked)
	}

	root, err := os.OpenRoot(at)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", at, err)
	}

	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(checked, opened) {
		_ = root.Close()

		return nil, fmt.Errorf("%s %w", at, ErrMoved)
	}

	return &dir{root: root, path: at}, nil
}

// A link's own lstat is never a folder or a regular file, so the type checks
// refuse it; these name it for what it is.
func notADirectory(at string, checked fs.FileInfo) error {
	if checked.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s %w", at, ErrLink)
	}

	return fmt.Errorf("%s is no directory", at)
}

func notARegularFile(at string, checked fs.FileInfo) error {
	if checked.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s %w", at, ErrLink)
	}

	return fmt.Errorf("%s %w", at, ErrNotRegular)
}

func (held *dir) close() {
	if held != nil {
		_ = held.root.Close()
	}
}

func singleName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

// sub opens one named directory below held.
func (held *dir) sub(name string) (*dir, error) {
	at := path.Join(held.path, name)
	if !singleName(name) {
		return nil, fmt.Errorf("%s names no single entry", at)
	}

	checked, err := held.root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}

	if !checked.IsDir() {
		return nil, notADirectory(at, checked)
	}

	child, err := held.root.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", at, err)
	}

	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(checked, opened) {
		_ = child.Close()

		return nil, fmt.Errorf("%s %w", at, ErrMoved)
	}

	return &dir{root: child, path: at}, nil
}

// subPath opens a slash-separated path below held, one component at a time.
func (held *dir) subPath(relative string) (*dir, error) {
	current := held

	for _, name := range strings.Split(relative, "/") {
		next, err := current.sub(name)
		if current != held {
			current.close()
		}

		if err != nil {
			return nil, err
		}

		current = next
	}

	if current == held {
		return nil, fmt.Errorf("%s names no directory below %s", relative, held.path)
	}

	return current, nil
}

// fileInfo is a regular file below held, checked without opening it.
func (held *dir) fileInfo(name string) (fs.FileInfo, error) {
	at := path.Join(held.path, name)
	if !singleName(name) {
		return nil, fmt.Errorf("%s names no single entry", at)
	}

	checked, err := held.root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}

	if !checked.Mode().IsRegular() {
		return nil, notARegularFile(at, checked)
	}

	// A second name can be an outside file linked in: reading it would hand
	// that file out, and appending to it would write there.
	if hardLinked(checked) {
		return nil, fmt.Errorf("%s %w", at, ErrHardLinked)
	}

	return checked, nil
}

// read reads one regular file below held, at most limit bytes. The open never
// blocks on a pipe, and the descriptor must be the file lstat saw.
func (held *dir) read(name string, limit int64) ([]byte, error) {
	at := path.Join(held.path, name)

	checked, err := held.fileInfo(name)
	if err != nil {
		return nil, err
	}

	if checked.Size() > limit {
		return nil, fmt.Errorf("%s %w", at, ErrTooLarge)
	}

	file, err := held.root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", at, err)
	}

	defer func() { _ = file.Close() }()

	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(checked, opened) {
		return nil, fmt.Errorf("%s %w", at, ErrMoved)
	}

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", at, err)
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s %w", at, ErrTooLarge)
	}

	return data, nil
}

// readOptional is read, with a missing file read as empty.
func (held *dir) readOptional(name string, limit int64) ([]byte, error) {
	data, err := held.read(name, limit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	return data, err
}

func (held *dir) entries() ([]fs.DirEntry, error) {
	opened, err := held.root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", held.path, err)
	}

	defer func() { _ = opened.Close() }()

	listed, err := opened.ReadDir(walkFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("listing %s: %w", held.path, err)
	}

	if len(listed) > walkFiles {
		return nil, fmt.Errorf("%s %w", held.path, ErrTooMany)
	}

	return listed, nil
}

// walked is one regular file a walk found, by its slash path below the walk's
// start.
type walked struct {
	name string
	data []byte
}

// walk reads every regular file below held, at most walkDepth folders deep,
// walkFiles files and limit bytes in all. A link, a pipe or any other
// non-regular entry refuses the whole walk: a tool is files and folders only.
func (held *dir) walk(limit int64, skipTop func(name string) bool) ([]walked, error) {
	var found []walked

	total := int64(0)

	var descend func(current *dir, prefix string, depth int) error

	descend = func(current *dir, prefix string, depth int) error {
		listed, err := current.entries()
		if err != nil {
			return err
		}

		for _, entry := range listed {
			name := entry.Name()
			if prefix == "" && skipTop != nil && skipTop(name) {
				continue
			}

			relative := path.Join(prefix, name)

			if entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
				if depth >= walkDepth {
					return fmt.Errorf("%s is deeper than %d folders", path.Join(held.path, relative), walkDepth)
				}

				child, err := current.sub(name)
				if err != nil {
					return err
				}

				err = descend(child, relative, depth+1)
				child.close()

				if err != nil {
					return err
				}

				continue
			}

			if len(found) >= walkFiles {
				return fmt.Errorf("%s %w", held.path, ErrTooMany)
			}

			data, err := current.read(name, limit-total)
			if err != nil {
				return err
			}

			total += int64(len(data))
			found = append(found, walked{name: relative, data: data})
		}

		return nil
	}

	err := descend(held, "", 0)
	if err != nil {
		return nil, err
	}

	return found, nil
}

// hardLinked says whether a file has a second name.
func hardLinked(info fs.FileInfo) bool {
	stat, readable := info.Sys().(*syscall.Stat_t)

	return readable && stat.Nlink > 1
}
