package safefs

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Each function here takes a root and a full path below it, and walks from the
// root to the path's folder one component at a time.

func relative(root string, target string) (string, error) {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %w %s", target, ErrOutside, root)
	}

	return filepath.ToSlash(rel), nil
}

// At opens the existing folder dir below root.
func At(root string, dir string) (*Dir, error) {
	rel, err := relative(root, dir)
	if err != nil {
		return nil, err
	}

	top, err := Root(root)
	if err != nil {
		return nil, err
	}

	if rel == "." {
		return top, nil
	}

	defer top.Close()

	return top.SubPath(rel)
}

// EnsureAt opens the folder dir below root, making the root and every missing
// folder down to dir first, each folder below the root with perm.
func EnsureAt(root string, dir string, perm fs.FileMode) (*Dir, error) {
	rel, err := relative(root, dir)
	if err != nil {
		return nil, err
	}

	top, err := EnsureRoot(root)
	if err != nil {
		return nil, err
	}

	if rel == "." {
		return top, nil
	}

	defer top.Close()

	return top.EnsureSubPath(rel, perm)
}

// MkdirAll is EnsureAt, closing the folder again.
func MkdirAll(root string, dir string, perm fs.FileMode) error {
	held, err := EnsureAt(root, dir, perm)
	if err != nil {
		return err
	}

	held.Close()

	return nil
}

// Parent opens the existing folder of target below root, and answers target's
// own name in it.
func Parent(root string, target string) (*Dir, string, error) {
	rel, err := relative(root, target)
	if err != nil {
		return nil, "", err
	}

	if rel == "." {
		return nil, "", fmt.Errorf("%s is its root and no entry below it", target)
	}

	held, err := At(root, filepath.Join(root, filepath.FromSlash(path.Dir(rel))))
	if err != nil {
		return nil, "", err
	}

	return held, path.Base(rel), nil
}

// Mkdir makes the folder dir below root, refusing one already there; its
// parent must exist.
func Mkdir(root string, dir string, perm fs.FileMode) error {
	held, name, err := Parent(root, dir)
	if err != nil {
		return err
	}

	defer held.Close()

	return held.Mkdir(name, perm)
}

// WriteFile replaces target below root with data, through a fresh name renamed
// over it; its folder must exist.
func WriteFile(root string, target string, data []byte, perm fs.FileMode) error {
	held, name, err := Parent(root, target)
	if err != nil {
		return err
	}

	defer held.Close()

	return held.Replace(name, data, perm)
}

// Create writes the new file target below root, refusing one already there;
// its folder must exist.
func Create(root string, target string, data []byte, perm fs.FileMode) error {
	held, name, err := Parent(root, target)
	if err != nil {
		return err
	}

	defer held.Close()

	return held.Create(name, data, perm)
}

// CreateFile opens the new file target below root for writing, refusing one
// already there; its folder must exist.
func CreateFile(root string, target string, perm fs.FileMode) (*os.File, error) {
	held, name, err := Parent(root, target)
	if err != nil {
		return nil, err
	}

	defer held.Close()

	return held.CreateFile(name, perm)
}

// OpenChecked opens target below root with flag, refused unless it is the
// regular file of one name its folder names; its folder must exist.
func OpenChecked(root string, target string, flag int, perm fs.FileMode) (*os.File, error) {
	held, name, err := Parent(root, target)
	if err != nil {
		return nil, err
	}

	defer held.Close()

	return held.OpenChecked(name, flag, perm)
}

// Append opens target below root for appending, creating it where missing.
func Append(root string, target string, perm fs.FileMode) (*os.File, error) {
	return OpenChecked(root, target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
}

// Lock opens the lock file target below root, creating it where missing.
func Lock(root string, target string, perm fs.FileMode) (*os.File, error) {
	return OpenChecked(root, target, os.O_CREATE|os.O_RDWR, perm)
}

// Rename moves from to to, both below root.
func Rename(root string, from string, to string) error {
	return Move(root, from, root, to)
}

// Move moves from below fromRoot to to below toRoot, walking each side from
// its own root. A path whose folder is the owner's names that folder as its
// root.
func Move(fromRoot string, from string, toRoot string, to string) error {
	source, fromName, err := Parent(fromRoot, from)
	if err != nil {
		return err
	}

	defer source.Close()

	target, toName, err := Parent(toRoot, to)
	if err != nil {
		return err
	}

	defer target.Close()

	return source.Rename(fromName, target, toName)
}
