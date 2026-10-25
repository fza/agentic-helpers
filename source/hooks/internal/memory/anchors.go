package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// pathToken is anything that may name a file: a run of path characters, letters
// and digits of any script among them, holding a dot or a slash. `grounding.go:324` is a token whose line suffix goes.
var (
	pathToken  = regexp.MustCompile(`[\p{L}\p{N}\p{M}_./~-]*[./][\p{L}\p{N}\p{M}_./~-]+(?::[0-9]+(?::[0-9]+)?)?`)
	lineSuffix = regexp.MustCompile(`(?::[0-9]+)+$`)
)

type tree struct {
	root   string
	commit string
	branch string
}

// checkout is the caller's own checkout: its top level, HEAD commit (at least
// eight characters, longer where git needs it unambiguous) and branch.
func (mem *memory) checkout(ctx context.Context) tree {
	held := tree{root: mem.env.Base}

	top, err := mem.env.Git.Output(ctx, mem.env.Base, "rev-parse", "--show-toplevel")
	if err == nil && strings.TrimSpace(top) != "" {
		held.root = strings.TrimSpace(top)
	}

	commit, err := mem.env.Git.Output(ctx, held.root, "rev-parse", "--short=8", "HEAD")
	if err == nil {
		held.commit = strings.TrimSpace(commit)
	}

	branch, err := mem.env.Git.Output(ctx, held.root, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil {
		held.branch = strings.TrimSpace(branch)
	}

	return held
}

func fileHash(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])[:16], true
}

// relative turns a token into a path inside root, or empty where it names
// nothing there.
func relative(root string, token string) string {
	token = strings.Trim(token, ".,;:()[]{}<>\"'`")
	token = lineSuffix.ReplaceAllString(token, "")

	if token == "" || strings.Contains(token, "://") {
		return ""
	}

	path := token
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	inside, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || inside == "." || strings.HasPrefix(inside, "..") {
		return ""
	}

	return filepath.ToSlash(inside)
}

// anchorsFor is every file of root the text names, plus the explicit ones,
// each with its content hash.
func anchorsFor(root string, text string, explicit []string) []anchor {
	var anchors []anchor

	seen := map[string]bool{}
	add := func(token string) {
		path := relative(root, token)
		if path == "" || seen[path] {
			return
		}

		hash, found := fileHash(filepath.Join(root, filepath.FromSlash(path)))
		if !found {
			return
		}

		seen[path] = true
		anchors = append(anchors, anchor{Path: path, Hash: hash})
	}

	for _, token := range explicit {
		add(token)
	}

	for _, token := range pathToken.FindAllString(text, -1) {
		add(token)
	}

	slices.SortFunc(anchors, func(a, b anchor) int { return strings.Compare(a.Path, b.Path) })

	return anchors
}

// rehash anchors the same paths to the tree as it stands; a path gone from it
// drops out.
func rehash(root string, anchors []anchor) []anchor {
	var fresh []anchor

	for _, held := range anchors {
		hash, found := fileHash(filepath.Join(root, filepath.FromSlash(held.Path)))
		if found {
			fresh = append(fresh, anchor{Path: held.Path, Hash: hash})
		}
	}

	return fresh
}

// staleness is every flag an entry earns against the caller's checkout.
func staleness(here tree, held *entry) []string {
	var flags []string

	for _, anchored := range held.anchors {
		hash, found := fileHash(filepath.Join(here.root, filepath.FromSlash(anchored.Path)))

		switch {
		case !found:
			flags = append(flags, "gone: "+anchored.Path)
		case hash != anchored.Hash:
			flags = append(flags, "drifted: "+anchored.Path)
		}
	}

	if held.branch != "" && here.branch != "" && held.branch != here.branch {
		flags = append(flags, "other branch: "+held.branch)
	}

	return flags
}
