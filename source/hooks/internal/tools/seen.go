package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
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
	ctx     context.Context
	lib     *library
	file    *slots.File
	key     string
	set     seenSet
	again   bool
	changed bool
}

// seenFormat is the layout of the library's shared cache: seen sets in a
// slot directory with its classes; 2 kept them in one fixed-width slot file,
// 1 a JSON file per owning process.
const seenFormat = 3

// seenSlots is the slot file of every owning process's seen set, in the
// library's cache shared by every store, once the seen files an earlier
// release kept there are taken in.
func (lib *library) seenSlots(ctx context.Context) (*slots.File, error) {
	dir := lib.env.Roots.SharedCache(home.Tools)
	file := slots.Open(lib.env.Roots.Cache, filepath.Join(dir, "sessions.slots"))
	file.Now = lib.now

	err := file.Adopt(ctx, dir, seenFormat, func() []slots.Legacy {
		paths, _ := filepath.Glob(filepath.Join(dir, "seen", "*.json"))

		legacy := make([]slots.Legacy, 0, len(paths))
		for _, path := range paths {
			legacy = append(legacy, slots.Legacy{Path: path, Key: "seen/" + strings.TrimSuffix(filepath.Base(path), ".json")})
		}

		return legacy
	})
	if err != nil {
		return nil, err
	}

	return file, nil
}

// seenKey is the owning process's seen slot, empty where no owner is found.
func (lib *library) seenKey(ctx context.Context) string {
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

	return "seen/" + strconv.Itoa(owner) + "-" + strconv.FormatInt(started.Unix(), 10)
}

// presenting opens the seen set for one read; nil where no owner is found,
// which prints everything in full.
func (lib *library) presenting(ctx context.Context, again bool) *presented {
	key := lib.seenKey(ctx)
	if key == "" {
		return nil
	}

	file, err := lib.seenSlots(ctx)
	if err != nil {
		return nil
	}

	seen := &presented{ctx: ctx, lib: lib, file: file, key: key, again: again}
	file.GetJSON(ctx, key, &seen.set)

	return seen
}

func versionKey(hit found) string {
	return fmt.Sprintf("%s v%d", hit.shown(), hit.tool.Version)
}

// printed is whether hit's version already stands in the context; it marks
// it as standing from now on.
func (seen *presented) printed(hit found) bool {
	return seen.marked(versionKey(hit))
}

// marked is whether key already stands in the context; it marks it as
// standing from now on.
func (seen *presented) marked(key string) bool {
	if seen == nil {
		return false
	}

	if !seen.again && slices.Contains(seen.set.Lines, key) {
		return true
	}

	if !slices.Contains(seen.set.Lines, key) {
		seen.set.Lines = append(seen.set.Lines, key)
		seen.changed = true
	}

	return false
}

// printedUsage is printed for a version's usage, which a find hit no longer
// carries: only agentic-memory's `show` marks it.
func (seen *presented) printedUsage(hit found) bool {
	return seen.marked(versionKey(hit) + " usage")
}

// save writes the seen set back, fitted to its ceiling; a write that fails is
// reported.
func (seen *presented) save() {
	if seen == nil || !seen.changed {
		return
	}

	data, err := slots.Encode(&seen.set)
	if err == nil {
		err = seen.file.Put(seen.ctx, seen.key, data)
	}

	seen.lib.reportSlot("keeping", seen.key, err)
}

const seenCeiling = 32_768

func (set *seenSet) Ceiling() int { return seenCeiling }

func (set *seenSet) Limit() {}

// Shrink gives up the older half of the lines and of the tools offered: those
// print in full again.
func (set *seenSet) Shrink() bool {
	if len(set.Lines)+len(set.Offered) == 0 {
		return false
	}

	set.Lines = slots.NewerHalf(set.Lines)
	set.Offered = slots.NewerHalf(set.Offered)

	return true
}

func seenPointer(hit found) string {
	return versionKey(hit) + " (seen)"
}

// seenTool is a tool version the calling agent's context already holds: its
// name and version alone, and a find hit's score, as the text answers with
// its pointer.
type seenTool struct {
	Name    string  `json:"name"`
	Score   *int    `json:"score,omitempty"`
	Label   *string `json:"label,omitempty"`
	Version int     `json:"version"`
	Seen    bool    `json:"seen"`
}

func (held listedTool) MarshalJSON() ([]byte, error) {
	if held.Seen {
		return json.Marshal(seenTool{Name: held.Name, Score: held.Score, Label: held.Label, Version: held.Version, Seen: true})
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
	key := lib.seenKey(ctx)
	if key == "" {
		return
	}

	file, err := lib.seenSlots(ctx)
	if err == nil {
		lib.reportSlot("clearing", key, file.Delete(ctx, key))
	}
}
