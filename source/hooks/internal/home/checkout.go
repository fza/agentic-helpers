package home

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
)

var ErrNoRepository = errors.New("not inside a git repository")

type Git interface {
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

// Checkout is what a checkout is matched by. Root is the main checkout's
// directory, CommonDir its git common directory, empty for a directory outside
// git. Pointer is the store id the pointer in CommonDir names. Origin is the
// remote `origin`, Remotes every other remote, both as host and path with no
// credential; a resolution reads them only once the pointer alone does not
// settle it, since asking git costs every hook a spawn. Graph is the graph
// repository's checkout where the checkout's `.sdd/` places its graph in
// another repository.
type Checkout struct {
	Root      string
	CommonDir string
	Volume    string
	Inode     uint64
	Pointer   string
	RepoID    string
	Origin    string
	Remotes   []string
	LegacyKey string
	Graph     *Checkout

	git Git
}

// withRemotes is the checkout with its remotes read, its graph repository's
// too.
func (checkout Checkout) withRemotes(ctx context.Context) Checkout {
	if checkout.git != nil && checkout.CommonDir != "" && checkout.Origin == "" && checkout.Remotes == nil {
		checkout.Origin, checkout.Remotes = remotes(ctx, checkout.Root, checkout.git)
		if checkout.Remotes == nil {
			checkout.Remotes = []string{}
		}
	}

	if checkout.Graph != nil {
		graph := checkout.Graph.withRemotes(ctx)
		checkout.Graph = &graph
	}

	return checkout
}

// Inspect reads the checkout holding base.
func Inspect(ctx context.Context, base string, lookup project.CommonDirLookup, git Git) (Checkout, error) {
	checkout, err := inspectGit(ctx, base, lookup, git)
	if err != nil {
		return Checkout{}, err
	}

	link := graphconfig.Link(checkout.Root)
	if link == "" {
		return checkout, nil
	}

	graph, err := inspectGit(ctx, link, lookup, git)
	if err == nil && graph.CommonDir != checkout.CommonDir {
		checkout.Graph = &graph
	}

	return checkout, nil
}

// InspectDirectory reads a directory outside git: matched by its path alone.
func InspectDirectory(base string) Checkout {
	root, err := filepath.Abs(base)
	if err != nil {
		root = filepath.Clean(base)
	}

	return Checkout{Root: root, LegacyKey: projectkey.DirectoryKey(base)}
}

func inspectGit(ctx context.Context, base string, lookup project.CommonDirLookup, git Git) (Checkout, error) {
	if lookup == nil {
		return Checkout{}, ErrNoRepository
	}

	common, err := lookup.CommonDir(ctx, base)
	if err != nil || common == "" {
		return Checkout{}, ErrNoRepository
	}

	if !filepath.IsAbs(common) {
		common = filepath.Join(base, common)
	}

	common = filepath.Clean(common)
	root := filepath.Dir(common)

	checkout := Checkout{Root: root, CommonDir: common, RepoID: projectkey.RepoID(root)}
	checkout.Volume, checkout.Inode = fileIdentity(common)
	checkout.Pointer = readPointer(common)

	legacy, err := projectkey.Key(ctx, base, lookup)
	if err == nil {
		checkout.LegacyKey = legacy
	}

	checkout.git = git

	return checkout, nil
}

// fileIdentity is the volume UUID and inode of path. Without a volume UUID
// the inode means nothing on its own, since another volume reuses it.
func fileIdentity(path string) (string, uint64) {
	volume := volumeID(path)
	if volume == "" {
		return "", 0
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", 0
	}

	stat, readable := info.Sys().(*syscall.Stat_t)
	if !readable {
		return "", 0
	}

	return volume, uint64(stat.Ino)
}

// pointerLimit caps a pointer read: it holds one id.
const pointerLimit = 256

func readPointer(common string) string {
	file, err := os.Open(filepath.Join(common, PointerName))
	if err != nil {
		return ""
	}

	defer func() { _ = file.Close() }()

	buffer := make([]byte, pointerLimit)

	read, _ := file.Read(buffer)

	id := strings.TrimSpace(string(buffer[:read]))
	if !ValidID(id) {
		return ""
	}

	return id
}

// remotes asks git for every remote's URL in one call: origin, then the
// others, each with no credential.
func remotes(ctx context.Context, root string, git Git) (string, []string) {
	if git == nil {
		return "", nil
	}

	answer, err := git.Output(ctx, root, "config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		return "", nil
	}

	origin := ""

	var others []string

	for line := range strings.Lines(answer) {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}

		normal := NormalRemote(value)
		if normal == "" {
			continue
		}

		if key == "remote.origin.url" {
			origin = normal

			continue
		}

		others = append(others, normal)
	}

	slices.Sort(others)

	return origin, slices.Compact(others)
}

// NormalRemote is a remote URL as host and path: its user, password, port,
// query and fragment dropped, so no credential a URL carries is ever kept, and
// `https://host/org/repo.git` and `git@host:org/repo` read as one remote. A
// local path stays a clean path.
func NormalRemote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return ""
		}

		if parsed.Scheme == "file" {
			return filepath.Clean(parsed.Path)
		}

		return joinRemote(parsed.Hostname(), parsed.Path)
	}

	if filepath.IsAbs(raw) || strings.HasPrefix(raw, ".") {
		return filepath.Clean(raw)
	}

	head, rest, _ := strings.Cut(raw, "/")
	if at := strings.LastIndex(head, "@"); at >= 0 {
		head = head[at+1:]
	}

	host, path, scp := strings.Cut(head, ":")
	if !scp {
		return ""
	}

	if rest != "" {
		path += "/" + rest
	}

	return joinRemote(host, path)
}

func joinRemote(host string, path string) string {
	path, _, _ = strings.Cut(path, "?")
	path, _, _ = strings.Cut(path, "#")
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")

	if host == "" || path == "" {
		return ""
	}

	return strings.ToLower(host) + "/" + path
}
