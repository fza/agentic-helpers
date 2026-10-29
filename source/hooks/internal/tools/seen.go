package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// seenSet is what the calling agent's context already holds of the library:
// the tool versions find or list printed (Lines, `<shown name> v<n>`), and
// the own tools the prompt offer named (Offered). It belongs to the owning
// `claude` process rather than the session id, which an MCP server keeps from
// its start across a `/clear`; a compaction, `/clear` or resume empties it.
type seenSet struct {
	Lines   []string `json:"lines"`
	Offered []string `json:"offered"`
}

type presented struct {
	path    string
	set     seenSet
	again   bool
	changed bool
}

// seenPath is the owning process's seen file, empty where no owner is found.
func (lib *library) seenPath(ctx context.Context) string {
	if lib.env.Processes == nil {
		return ""
	}

	owner := seat.Owner(ctx, lib.env.Processes, lib.env.OwnerName)
	if owner <= 1 {
		return ""
	}

	started, found := lib.env.Processes.StartTime(ctx, owner)
	if !found {
		return ""
	}

	name := strconv.Itoa(owner) + "-" + strconv.FormatInt(started.Unix(), 10) + ".json"

	return filepath.Join(lib.env.Roots.SharedCache(home.Tools), "seen", name)
}

// presenting opens the seen set for one read; nil where no owner is found,
// which prints everything in full.
func (lib *library) presenting(ctx context.Context, again bool) *presented {
	path := lib.seenPath(ctx)
	if path == "" {
		return nil
	}

	seen := &presented{path: path, again: again}

	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &seen.set)
	}

	return seen
}

func versionKey(hit found) string {
	return fmt.Sprintf("%s v%d", hit.shown(), hit.tool.Version)
}

// printed is whether hit's version already stands in the context; it marks
// it as standing from now on.
func (seen *presented) printed(hit found) bool {
	if seen == nil {
		return false
	}

	key := versionKey(hit)
	if !seen.again && slices.Contains(seen.set.Lines, key) {
		return true
	}

	if !slices.Contains(seen.set.Lines, key) {
		seen.set.Lines = append(seen.set.Lines, key)
		seen.changed = true
	}

	return false
}

func (seen *presented) save() {
	if seen == nil || !seen.changed {
		return
	}

	data, err := json.Marshal(seen.set)
	if err != nil {
		return
	}

	err = os.MkdirAll(filepath.Dir(seen.path), 0o700)
	if err == nil {
		_ = atomicfile.Write(seen.path, data, 0o600)
	}
}

func seenPointer(hit found) string {
	return versionKey(hit) + " (seen)"
}

// seenTool is a tool version the calling agent's context already holds: its
// name and version alone, as the text answers with its pointer.
type seenTool struct {
	Name    string  `json:"name"`
	Label   *string `json:"label"`
	Version int     `json:"version"`
	Seen    bool    `json:"seen"`
}

func (held listedTool) MarshalJSON() ([]byte, error) {
	if held.Seen {
		return json.Marshal(seenTool{Name: held.Name, Label: held.Label, Version: held.Version, Seen: true})
	}

	type plain listedTool

	return json.Marshal(plain(held))
}

func (listedTool) SchemaAlternative() any {
	return seenTool{}
}

// forgetSeen empties the owning process's seen set, where its context starts
// afresh or ends.
func (lib *library) forgetSeen(ctx context.Context) {
	path := lib.seenPath(ctx)
	if path != "" {
		_ = os.Remove(path)
	}
}

func (held scoredTool) MarshalJSON() ([]byte, error) {
	if held.Seen {
		return json.Marshal(seenTool{Name: held.Name, Label: held.Label, Version: held.Version, Seen: true})
	}

	type plainListed listedTool

	return json.Marshal(struct {
		plainListed
		Score *float64 `json:"score"`
	}{plainListed(held.listedTool), held.Score})
}

func (scoredTool) SchemaAlternative() any {
	return seenTool{}
}
