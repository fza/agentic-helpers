package alternatives

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const (
	cacheName = "command-flavours.json"
	// readLimit bounds the bytes read from a binary for its flavour markers:
	// a marker past it leaves the binary without a flavour, never with a wrong
	// one.
	readLimit = 8 << 20
)

// Machine is the machine a call runs on: Path the PATH its commands are
// looked up in, and CacheFile where each binary's flavour is kept, keyed by
// its resolved path, modification time and size, so an upgraded binary is
// read again.
type Machine struct {
	Path      string
	CacheFile string

	mutex    sync.Mutex
	flavours map[string]string
}

// CacheFile is where a machine keeps its flavours under the cache root, empty
// where there is no root.
func CacheFile(cacheRoot string) string {
	if cacheRoot == "" {
		return ""
	}

	return filepath.Join(cacheRoot, cacheName)
}

// Find looks name up on the machine's PATH as exec.LookPath looks it up on
// the process's own: the first regular file of that name with an execute bit.
// Only an absolute PATH entry counts: an empty or relative one names a
// directory under the working directory, which a hook does not share with
// the shell, and where a project's own file could stand in for the system's.
func (machine *Machine) Find(name string) (string, bool) {
	if strings.Contains(name, "/") {
		return "", false
	}

	for _, directory := range filepath.SplitList(machine.Path) {
		if !filepath.IsAbs(directory) {
			continue
		}

		candidate := filepath.Join(directory, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, true
		}
	}

	return "", false
}

// Installed is the first of the entry's alternatives bundled with the plugin
// or on the PATH.
func (machine *Machine) Installed(entry Entry) (Alternative, bool) {
	for _, alternative := range entry.Alternatives {
		if alternative.Bundled {
			return alternative, true
		}

		_, found := machine.Find(alternative.Program)
		if found {
			return alternative, true
		}
	}

	return Alternative{}, false
}

// Flavour reads the binary at path for the entry's flavour markers, the GNU
// ones first, and never runs it: a hook running a binary to learn its flavour
// would run whatever the PATH holds before any call is approved. path is what
// Find returns, a regular file. It is empty where the path is not absolute,
// cannot be read, or carries no marker within readLimit bytes; no dialect
// usage is then refused.
func (machine *Machine) Flavour(ctx context.Context, entry Entry, path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return ""
	}

	key := fmt.Sprintf("%s|%d|%d", resolved, info.ModTime().UnixNano(), info.Size())

	machine.mutex.Lock()
	defer machine.mutex.Unlock()

	if machine.flavours == nil {
		machine.flavours = machine.load()
	}

	held, found := machine.flavours[key]
	if found {
		return held
	}

	content, err := readHead(ctx, resolved)
	if err != nil {
		return ""
	}

	flavour := markedFlavour(entry, content)

	machine.flavours[key] = flavour
	machine.save()

	return flavour
}

func markedFlavour(entry Entry, content []byte) string {
	for _, flavour := range []string{FlavourGNU, FlavourBSD} {
		for _, marker := range entry.Markers[flavour] {
			if bytes.Contains(content, []byte(marker)) {
				return flavour
			}
		}
	}

	return ""
}

func readHead(ctx context.Context, path string) ([]byte, error) {
	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	content, err := io.ReadAll(io.LimitReader(file, readLimit))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return content, nil
}

func (machine *Machine) load() map[string]string {
	held := map[string]string{}

	if machine.CacheFile == "" {
		return held
	}

	raw, err := os.ReadFile(machine.CacheFile)
	if err != nil {
		return held
	}

	err = json.Unmarshal(raw, &held)
	if err != nil {
		return map[string]string{}
	}

	return held
}

// save keeps the flavours for the next call. A cache that cannot be written
// only costs the next call a read.
func (machine *Machine) save() {
	if machine.CacheFile == "" {
		return
	}

	raw, err := json.Marshal(machine.flavours)
	if err != nil {
		return
	}

	err = safefs.MkdirAll(filepath.Dir(machine.CacheFile), filepath.Dir(machine.CacheFile), 0o700)
	if err != nil {
		return
	}

	_ = atomicfile.Write(filepath.Dir(machine.CacheFile), machine.CacheFile, append(raw, '\n'), 0o644)
}

// Line is what the SessionStart check says the machine needs: each missing
// command with the alternative standing in for it, then the commands present
// in another flavour. It is empty where nothing needs a substitution.
func (machine *Machine) Line(ctx context.Context) string {
	var parts, bsd []string

	for _, entry := range Table {
		switch entry.Kind {
		case Missing:
			_, present := machine.Find(entry.Name)
			if present {
				continue
			}

			alternative, installed := machine.Installed(entry)
			if installed {
				parts = append(parts, entry.Name+" -> "+alternative.Shown())
			}
		case Dialect:
			path, present := machine.Find(entry.Name)
			if !present {
				continue
			}

			if machine.Flavour(ctx, entry, path) == FlavourBSD {
				bsd = append(bsd, entry.Name)
			}
		}
	}

	switch {
	case len(bsd) == 1:
		parts = append(parts, bsd[0]+" is "+FlavourBSD)
	case len(bsd) > 1:
		parts = append(parts, spokenList(bsd)+" are "+FlavourBSD)
	}

	if len(parts) == 0 {
		return ""
	}

	return "this machine: " + strings.Join(parts, "; ")
}

func spokenList(names []string) string {
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
