// Package writers keeps the one list per project, under
// `~/.claude/agentic-helpers/<project>/writers.json`, of the checkouts that may
// write the project's agentic-memory store and agentic-tools library.
//
// Both are keyed by the `repo_id` a checkout declares, which any checkout can
// declare. The right to write therefore belongs to checkouts, by their git
// common directory, which a cloned repository cannot declare for itself: the
// first call claims an unlisted project, a listed checkout trusts another, and
// a list that cannot be read lets no one write.
//
// Every value under `~/.claude` is untrusted, since a sandboxed process may
// write there: the list is read through descriptors with no link, no pipe and
// no second name followed, and written through a fresh name renamed over it.
package writers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
)

const (
	fileName  = "writers.json"
	listLimit = 64 << 10
	lockWait  = 2 * time.Second
)

var (
	ErrUnreadable = errors.New("writers.json cannot be read")
	ErrNoCheckout = errors.New("is no git checkout")
	ErrUnclaimed  = errors.New("no checkout is listed for this project yet")
)

// NotListed is a refusal: the checkouts that may write, and the one asking.
type NotListed struct {
	Checkouts []string
	Here      string
}

func (refusal NotListed) Error() string {
	return fmt.Sprintf("this project belongs to other checkouts: %s", strings.Join(refusal.Checkouts, ", "))
}

// List is one project's list as both pieces reach it.
type List struct {
	Home   string
	Key    string
	Lookup project.CommonDirLookup
}

func (list List) root() string { return filepath.Join(list.Home, ".claude", "agentic-helpers") }

// legacy is where agentic-tools kept a library's own list before the project's
// one list. Every check consults it while the project's list is missing, so a
// piece that never wrote it cannot claim the project past it; the first change
// moves it in.
func (list List) legacy() string {
	return filepath.Join(projectkey.Dir(filepath.Join(list.Home, ".claude", "agentic-tools"), list.Key), fileName)
}

// current is the project's list, or the legacy one while the project's is
// missing.
func (list List) current() ([]string, bool, error) {
	listed, present, err := readList(list.root(), list.Key)
	if err != nil || present {
		return listed, present, err
	}

	return readList(filepath.Dir(list.legacy()), "")
}

// Check says whether the checkout at base is listed, without claiming
// anything: nil where it is, ErrUnclaimed where no list exists, NotListed or
// ErrUnreadable otherwise. A read uses it; only a write claims.
func (list List) Check(ctx context.Context, base string) error {
	own, err := list.CommonDir(ctx, base)
	if err != nil {
		return err
	}

	listed, present, err := list.current()

	switch {
	case err != nil:
		return err
	case !present:
		return ErrUnclaimed
	case slices.Contains(listed, own):
		return nil
	}

	return list.notListed(ctx, base, listed)
}

func (list List) notListed(ctx context.Context, base string, listed []string) NotListed {
	checkouts := make([]string, 0, len(listed))
	for _, common := range listed {
		checkouts = append(checkouts, filepath.Dir(common))
	}

	return NotListed{Checkouts: checkouts, Here: project.Root(ctx, base, list.Lookup)}
}

// Path is where the list lives.
func (list List) Path() string {
	return filepath.Join(projectkey.Dir(list.root(), list.Key), fileName)
}

// CommonDir is the git common directory of the checkout holding at.
func (list List) CommonDir(ctx context.Context, at string) (string, error) {
	if list.Lookup == nil {
		return "", fmt.Errorf("%s %w", at, ErrNoCheckout)
	}

	common, err := list.Lookup.CommonDir(ctx, at)
	if err != nil || common == "" {
		return "", fmt.Errorf("%s %w", at, ErrNoCheckout)
	}

	if !filepath.IsAbs(common) {
		common = filepath.Join(at, common)
	}

	return filepath.Clean(common), nil
}

// Admit lets the checkout at base write: listed, or claiming an unlisted
// project. A list that cannot be read admits no one.
func (list List) Admit(ctx context.Context, base string) error {
	own, err := list.CommonDir(ctx, base)
	if err != nil {
		return err
	}

	// Listed in the project's own list: nothing to change. Anything else, a
	// legacy list included, goes through the lock, which moves it in.
	listed, claimed, err := readList(list.root(), list.Key)
	if err != nil {
		return err
	}

	if claimed && slices.Contains(listed, own) {
		return nil
	}

	return list.change(ctx, func(current []string, present bool) ([]string, error) {
		if !present {
			return []string{own}, nil
		}

		if slices.Contains(current, own) {
			return current, nil
		}

		return nil, list.notListed(ctx, base, current)
	})
}

// Trust adds the checkout at target, the caller at base admitted first. It
// answers the checkout it added.
func (list List) Trust(ctx context.Context, base string, target string) (string, error) {
	common, err := list.CommonDir(ctx, target)
	if err != nil {
		return "", err
	}

	err = list.Admit(ctx, base)
	if err != nil {
		return "", err
	}

	err = list.change(ctx, func(current []string, _ bool) ([]string, error) {
		if slices.Contains(current, common) {
			return current, nil
		}

		return append(current, common), nil
	})
	if err != nil {
		return "", err
	}

	return filepath.Dir(common), nil
}

// change rewrites the list under its lock: the legacy list moves in first,
// then update decides the new list from the current one.
func (list List) change(ctx context.Context, update func(current []string, present bool) ([]string, error)) error {
	folder, err := ensureDir(list.root(), list.Key)
	if err != nil {
		return err
	}

	defer func() { _ = folder.Close() }()

	unlock, err := lock(ctx, folder)
	if err != nil {
		return err
	}

	defer unlock()

	current, present, err := readList(list.root(), list.Key)
	if err != nil {
		return err
	}

	moved := false

	if !present {
		current, present, err = readList(filepath.Dir(list.legacy()), "")
		if err != nil {
			return fmt.Errorf("moving %s: %w", list.legacy(), err)
		}

		moved = present
	}

	next, err := update(current, present)
	if err != nil {
		if moved {
			_ = write(folder, current)
			removeLegacy(list.legacy())
		}

		return err
	}

	err = write(folder, next)
	if err != nil {
		return err
	}

	if moved {
		removeLegacy(list.legacy())
	}

	return nil
}

func removeLegacy(at string) {
	folder, err := openChecked(filepath.Dir(at), "")
	if err != nil {
		return
	}

	defer func() { _ = folder.Close() }()

	_ = folder.Remove(filepath.Base(at))
}

// readList reads the list in the folder key names below root.
func readList(root string, key string) ([]string, bool, error) {
	folder, err := openChecked(root, key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}

	defer func() { _ = folder.Close() }()

	data, err := readFile(folder, fileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, true, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}

	var listed []string

	err = json.Unmarshal(data, &listed)
	if err != nil {
		return nil, true, fmt.Errorf("%w: it is no JSON array of paths", ErrUnreadable)
	}

	for _, at := range listed {
		if !filepath.IsAbs(at) || filepath.Clean(at) != at {
			return nil, true, fmt.Errorf("%w: it names %q, which is no clean absolute path", ErrUnreadable, at)
		}
	}

	return listed, true, nil
}

// openChecked opens root, then each folder of key below it, every one
// lstat-checked to be a real folder and held to what lstat saw.
func openChecked(root string, key string) (*os.Root, error) {
	checked, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}

	if !checked.IsDir() {
		return nil, fmt.Errorf("%s is a link or no folder", root)
	}

	current, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}

	for _, name := range strings.Split(key, "/") {
		if name == "" {
			continue
		}

		next, err := sub(current, name)
		_ = current.Close()

		if err != nil {
			return nil, err
		}

		current = next
	}

	return current, nil
}

func sub(folder *os.Root, name string) (*os.Root, error) {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("%q names no single folder", name)
	}

	checked, err := folder.Lstat(name)
	if err != nil {
		return nil, err
	}

	if !checked.IsDir() {
		return nil, fmt.Errorf("%s is a link or no folder", path.Join(folder.Name(), name))
	}

	child, err := folder.OpenRoot(name)
	if err != nil {
		return nil, err
	}

	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(checked, opened) {
		_ = child.Close()

		return nil, fmt.Errorf("%s changed while it was opened", path.Join(folder.Name(), name))
	}

	return child, nil
}

// ensureDir is openChecked, making each missing folder relative to its parent.
func ensureDir(root string, key string) (*os.Root, error) {
	err := os.MkdirAll(root, 0o700)
	if err != nil {
		return nil, fmt.Errorf("creating %s: %w", root, err)
	}

	current, err := openChecked(root, "")
	if err != nil {
		return nil, err
	}

	for _, name := range strings.Split(key, "/") {
		if name == "" {
			continue
		}

		err = current.Mkdir(name, 0o700)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			_ = current.Close()

			return nil, fmt.Errorf("creating %s: %w", name, err)
		}

		next, err := sub(current, name)
		_ = current.Close()

		if err != nil {
			return nil, err
		}

		current = next
	}

	return current, nil
}

// readFile reads a regular file of one name, never blocking on a pipe.
func readFile(folder *os.Root, name string) ([]byte, error) {
	checked, err := folder.Lstat(name)
	if err != nil {
		return nil, err
	}

	if !checked.Mode().IsRegular() || secondName(checked) || checked.Size() > listLimit {
		return nil, fmt.Errorf("%s is a link, a pipe, a hard link or too large", name)
	}

	file, err := folder.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}

	defer func() { _ = file.Close() }()

	opened, err := file.Stat()
	if err != nil || !os.SameFile(checked, opened) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}

	return io.ReadAll(io.LimitReader(file, listLimit))
}

func secondName(info fs.FileInfo) bool {
	stat, readable := info.Sys().(*syscall.Stat_t)

	return readable && stat.Nlink > 1
}

// write replaces the list through a fresh name renamed over it.
func write(folder *os.Root, listed []string) error {
	data, err := json.Marshal(listed)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", fileName, err)
	}

	random := make([]byte, 8)

	_, err = rand.Read(random)
	if err != nil {
		return fmt.Errorf("naming a temporary file: %w", err)
	}

	temporary := "." + fileName + "-" + hex.EncodeToString(random)

	file, err := folder.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", fileName, err)
	}

	_, err = file.Write(append(data, '\n'))
	closeErr := file.Close()

	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = folder.Rename(temporary, fileName)
	}

	if err != nil {
		_ = folder.Remove(temporary)

		return fmt.Errorf("writing %s: %w", fileName, err)
	}

	return nil
}

// lock takes the list's `.lock` exclusively, waiting at most lockWait.
func lock(ctx context.Context, folder *os.Root) (func(), error) {
	file, err := folder.OpenFile(".lock", os.O_RDWR|os.O_CREATE|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the writers lock: %w", err)
	}

	checked, err := folder.Lstat(".lock")
	opened, statErr := file.Stat()

	if err != nil || statErr != nil || !checked.Mode().IsRegular() || secondName(checked) || !os.SameFile(checked, opened) {
		_ = file.Close()

		return nil, errors.New("the writers lock is a link, a pipe or a hard link")
	}

	deadline := time.Now().Add(lockWait)

	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}

		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) || ctx.Err() != nil {
			_ = file.Close()

			return nil, errors.New("the writers list is locked; try again once its writer finishes")
		}

		time.Sleep(25 * time.Millisecond)
	}

	_ = file.Truncate(0)
	_, _ = file.Write([]byte(strconv.Itoa(os.Getpid()) + "\n"))

	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
