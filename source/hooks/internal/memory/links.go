package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
)

// A link names a full graph id or a memory id. A short graph id (`d-tac-427`)
// names no entry on its own and is never a link.
var (
	graphIDPattern  = regexp.MustCompile(`\b(\d{4})(\d{2})(\d{2})-(\d{6})-([sd]-[a-z]{3}-[a-z0-9]{3})\b`)
	memoryIDPattern = regexp.MustCompile(`\bm-[a-z0-9]{4,16}\b`)
	linkTarget      = regexp.MustCompile(`^(?:\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}|m-[a-z0-9]{4,16})$`)
)

// GraphExporter answers `sdd export` for the checkout in dir.
type GraphExporter interface {
	Export(ctx context.Context, dir string) ([]byte, error)
}

var wholeGraphID = regexp.MustCompile(`^\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}$`)

func isGraphID(id string) bool {
	return wholeGraphID.MatchString(id)
}

// graphPath is where the checkout keeps the graph entry id.
func (mem *memory) graphPath(ctx context.Context, id string) string {
	parts := graphIDPattern.FindStringSubmatch(id)
	if parts == nil {
		return ""
	}

	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)

	return filepath.Join(graphconfig.ResolveAt(root).Entries, parts[1], parts[2], parts[3]+"-"+parts[4]+"-"+parts[5]+".md")
}

// resolves is whether id names an entry here: a graph entry whose file the
// checkout holds, or a memory entry the folded log holds.
func (mem *memory) resolves(ctx context.Context, folded *state, id string) bool {
	if isGraphID(id) {
		_, err := os.Stat(mem.graphPath(ctx, id))

		return err == nil
	}

	_, found := folded.entries[id]

	return linkTarget.MatchString(id) && found
}

// textLinks is every full id the text names, split into those that resolve,
// which link the entry, and those that do not, which stay plain text.
func (mem *memory) textLinks(ctx context.Context, folded *state, text string, self string) ([]string, []string) {
	var linked, unlinked []string

	found := append(graphIDPattern.FindAllString(text, -1), memoryIDPattern.FindAllString(text, -1)...)

	for _, id := range found {
		if id == self || slices.Contains(linked, id) || slices.Contains(unlinked, id) {
			continue
		}

		if mem.resolves(ctx, folded, id) {
			linked = append(linked, id)
		} else {
			unlinked = append(unlinked, id)
		}
	}

	return linked, unlinked
}

// requireTarget refuses a link target that names no entry here.
func (mem *memory) requireTarget(ctx context.Context, folded *state, id string) error {
	if !linkTarget.MatchString(id) || !mem.resolves(ctx, folded, id) {
		return fmt.Errorf("%s: no such entry", id)
	}

	return nil
}

func (mem *memory) link(ctx context.Context, req request) result {
	return mem.changeLink(ctx, req, memorylog.KindLink)
}

func (mem *memory) unlink(ctx context.Context, req request) result {
	return mem.changeLink(ctx, req, memorylog.KindUnlink)
}

// changeLink links or unlinks an entry. Any seat may, as with supersede; the
// owner's brief lists it. A link from the entry's text holds as long as the
// text does.
func (mem *memory) changeLink(ctx context.Context, req request, kind memorylog.Kind) result {
	var loaded *state

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(folded *state, held *entry) error {
		loaded = folded

		switch {
		case kind == memorylog.KindLink:
			return mem.requireTarget(ctx, folded, req.Target)
		case slices.Contains(held.textLinks, req.Target):
			return fmt.Errorf("%s is named in %s's text; supersede the entry instead", req.Target, held.id)
		case !slices.ContainsFunc(held.explicit, func(link explicitLink) bool { return link.target == req.Target }):
			return fmt.Errorf("%s does not link to %s", held.id, req.Target)
		}

		return nil
	}, kind, req.Target, nil)
	if !ok {
		return failure
	}

	if kind == memorylog.KindLink {
		if !req.NoHeat {
			mem.warm(loaded, heatLink, req.Target)
		}

		return done(fmt.Sprintf("%s linked to %s", req.ID, req.Target))
	}

	return done(fmt.Sprintf("%s unlinked from %s", req.ID, req.Target))
}

// graphStatus is what became of each graph entry, by id, as `sdd export`
// says: its status and the entry that gave it, where one did.
type graphStatus struct {
	Key      string                `json:"key"`
	Statuses map[string]graphState `json:"statuses"`
}

type graphState struct {
	Status string `json:"status,omitempty"`
	By     string `json:"by,omitempty"`
}

// graphStatuses is every graph entry's status, from the cache while the graph
// is unchanged, else from `sdd export`. Without one, flags fall back to whether
// a graph entry's file exists, and the read says so once. An sdd without the
// command falls back silently: no stock release carries `export`, so its
// absence says nothing about the graph.
func (mem *memory) graphStatuses(ctx context.Context) (map[string]graphState, bool) {
	if mem.graphKnown != nil {
		return mem.graphKnown, true
	}

	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)
	key := graphKey(graphconfig.ResolveAt(root).Entries)
	cachePath := filepath.Join(mem.cacheDir(), "graph-status.json")

	var cached graphStatus
	if readJSON(cachePath, &cached) && cached.Key == key && cached.Statuses != nil {
		mem.graphKnown = cached.Statuses

		return cached.Statuses, true
	}

	if mem.env.Sdd != nil && !mem.env.Sdd.Of(ctx, root).Export {
		return nil, false
	}

	statuses, err := mem.exportStatuses(ctx, root)
	if err != nil {
		if !mem.graphWarned {
			mem.graphWarned = true
			_, _ = fmt.Fprintf(mem.streams.Err, "%s: graph status unavailable (sdd export: %v)\n", self, err)
		}

		return nil, false
	}

	_ = writeJSON(cachePath, graphStatus{Key: key, Statuses: statuses})
	mem.graphKnown = statuses

	return statuses, true
}

func (mem *memory) exportStatuses(ctx context.Context, root string) (map[string]graphState, error) {
	if mem.env.Graph == nil {
		return nil, fmt.Errorf("no sdd to ask")
	}

	raw, err := mem.env.Graph.Export(ctx, root)
	if err != nil {
		return nil, err
	}

	var exported struct {
		Repos []struct {
			Local   bool `json:"local"`
			Entries []struct {
				ID       string `json:"id"`
				Status   string `json:"status"`
				StatusBy string `json:"status_by"`
			} `json:"entries"`
		} `json:"repos"`
	}

	err = json.Unmarshal(raw, &exported)
	if err != nil {
		return nil, fmt.Errorf("reading its output: %w", err)
	}

	statuses := map[string]graphState{}

	for _, repo := range exported.Repos {
		if !repo.Local {
			continue
		}

		for _, held := range repo.Entries {
			if held.ID != "" {
				statuses[held.ID] = graphState{Status: held.Status, By: held.StatusBy}
			}
		}
	}

	if len(statuses) == 0 {
		return nil, fmt.Errorf("its output carries no local entries with a status")
	}

	return statuses, nil
}

// graphKey changes whenever a graph file is added, removed or written: the
// count of entry files and the newest modification time among them. A
// directory's own time does not move when a nested file changes.
func graphKey(dir string) string {
	count := 0

	var newest int64

	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		info, err := entry.Info()
		if err == nil {
			count++
			newest = max(newest, info.ModTime().UnixNano())
		}

		return nil
	})

	return fmt.Sprintf("%d@%d", count, newest)
}

// annotateLinks works out how every entry's line reads its links: a flag per
// target that is no longer live, active or open, and who links in.
func (mem *memory) annotateLinks(ctx context.Context, folded *state) {
	var statuses map[string]graphState

	asked := false

	for _, held := range folded.entries {
		held.linkFlags, held.inbound = nil, nil
	}

	for _, held := range folded.entryList() {
		for _, target := range held.links() {
			if !isGraphID(target) {
				linked, found := folded.entries[target]
				if found {
					linked.inbound = append(linked.inbound, held)
				}

				state := memoryLinkState(linked, found)
				if state != "" {
					held.linkFlags = append(held.linkFlags, "→ "+target+" "+state)
				}

				continue
			}

			_, err := os.Stat(mem.graphPath(ctx, target))
			if err != nil {
				held.linkFlags = append(held.linkFlags, "→ "+target+" gone")

				continue
			}

			if !asked {
				statuses, _ = mem.graphStatuses(ctx)
				asked = true
			}

			state := graphLinkState(statuses[target])
			if state != "" {
				held.linkFlags = append(held.linkFlags, "→ "+target+" "+state)
			}
		}
	}
}

func memoryLinkState(linked *entry, found bool) string {
	if !found {
		return "gone"
	}

	switch linked.status {
	case statusRetired:
		return "retired"
	case statusSuperseded:
		last, _ := linked.latest()
		if last.by != "" {
			return "superseded by " + last.by
		}

		return "superseded"
	}

	return ""
}

func graphLinkState(held graphState) string {
	switch held.Status {
	case "closed-by":
		return strings.TrimSpace("closed by " + held.By)
	case "superseded-by":
		return strings.TrimSpace("superseded by " + held.By)
	case "settled":
		return "settled"
	}

	return ""
}

// citing is up to two entries linking to held that were filed after it, newest
// first, leaving out every entry in shown.
func citing(held *entry, shown map[string]bool) []*entry {
	var newer []*entry

	for _, other := range held.inbound {
		if other.filed.After(held.filed) && !shown[other.id] {
			newer = append(newer, other)
		}
	}

	slices.SortStableFunc(newer, func(a, b *entry) int { return b.filed.Compare(a.filed) })

	return newer[:min(2, len(newer))]
}

// citingLines are a hit's inbound lines in recall and the brief: title only.
func citingLines(held *entry, shown map[string]bool, indent string) []string {
	var lines []string

	for _, other := range citing(held, shown) {
		lines = append(lines, fmt.Sprintf("%s← %s %s [%s, %s]", indent, other.id, other.title, other.seat, stamp(other.filed)))
		shown[other.id] = true
	}

	return lines
}
