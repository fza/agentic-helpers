package tools

import (
	"context"
	"encoding/json"
	"errors"
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
	file    *slots.File
	key     string
	set     seenSet
	again   bool
	changed bool
}

const (
	// seenFormat is the layout of the library's shared cache: seen sets in
	// one slot file; 1 kept a JSON file per owning process.
	seenFormat    = 2
	seenSlotWidth = 128 << 10
)

// seenSlots is the slot file of every owning process's seen set, in the
// library's cache shared by every store, once the seen files an earlier
// release kept there are taken in.
func (lib *library) seenSlots(ctx context.Context) (*slots.File, error) {
	dir := lib.env.Roots.SharedCache(home.Tools)
	file := slots.Open(filepath.Join(dir, "sessions.slots"), seenSlotWidth)
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

	seen := &presented{ctx: ctx, file: file, key: key, again: again}
	file.GetJSON(ctx, key, &seen.set)

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

// save writes the seen set back; one too large for its slot gives up its
// oldest lines until it fits.
func (seen *presented) save() {
	if seen == nil || !seen.changed {
		return
	}

	for {
		err := seen.file.PutJSON(seen.ctx, seen.key, seen.set)
		if !errors.Is(err, slots.ErrTooLarge) || len(seen.set.Lines)+len(seen.set.Offered) == 0 {
			return
		}

		seen.set.Lines = seen.set.Lines[len(seen.set.Lines)/2+len(seen.set.Lines)%2:]
		seen.set.Offered = seen.set.Offered[len(seen.set.Offered)/2+len(seen.set.Offered)%2:]
	}
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
		_ = file.Delete(ctx, key)
	}
}
