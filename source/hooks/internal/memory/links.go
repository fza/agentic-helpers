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

// A link names a full graph id, a full graph id qualified by the repo_id of
// one of sdd's connected repos (`<repo_id>:<id>`, as sdd's own cross-repo
// references read), or a memory id. A short graph id (`d-tac-427`) names no
// entry on its own and is never a link. A repo_id is URL-shaped, host/path,
// which keeps a word before a colon in prose from reading as one.
var (
	graphIDPattern     = regexp.MustCompile(`\b(\d{4})(\d{2})(\d{2})-(\d{6})-([sd]-[a-z]{3}-[a-z0-9]{3})\b`)
	memoryIDPattern    = regexp.MustCompile(`\bm-[a-z0-9]{4,16}\b`)
	linkTarget         = regexp.MustCompile(`^(?:\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}|m-[a-z0-9]{4,16})$`)
	qualifiedIDPattern = regexp.MustCompile(
		`\b[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9._~-]+)+:\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}\b`)
	wholeQualifiedID = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9._~-]+)+:\d{8}-\d{6}-[sd]-[a-z]{3}-[a-z0-9]{3}$`)
)

const noExport = "resolving another repository's entry needs sdd export, which this sdd lacks"

const notConnected = "not in the local graph or any connected repo (sdd repo list)"

// splitQualified is a qualified id's repo_id and graph id.
func splitQualified(id string) (string, string, bool) {
	if !wholeQualifiedID.MatchString(id) {
		return "", "", false
	}

	index := strings.LastIndex(id, ":")

	return id[:index], id[index+1:], true
}

// foreignName is how a line names a target in another repo: the last path
// segment of its repo_id, then the id.
func foreignName(target string) string {
	repo, bare, qualified := splitQualified(target)
	if !qualified {
		return target
	}

	return repo[strings.LastIndex(repo, "/")+1:] + ":" + bare
}

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

// exports is whether the installed sdd carries `export`, nil taking it as
// given.
func (mem *memory) exports(ctx context.Context, root string) bool {
	return mem.env.Sdd == nil || mem.env.Sdd.Of(ctx, root).Export
}

// resolveTarget is the form a link to id is stored in, or why id names no
// entry: a graph entry whose file the checkout holds, an entry a connected
// repo holds as `sdd export` reports it, or a memory entry the folded log
// holds. A bare graph id the local graph lacks is looked up in the connected
// repos and stored qualified where exactly one holds it; a qualified id naming
// the local repo is stored bare, as the local graph is keyed.
func (mem *memory) resolveTarget(ctx context.Context, folded *state, id string) (string, error) {
	if strings.HasPrefix(id, "m-") {
		if _, found := folded.entries[id]; found && linkTarget.MatchString(id) {
			return id, nil
		}

		return "", fmt.Errorf("%s: %w", id, ErrNoEntry)
	}

	return mem.resolveGraphTarget(ctx, id)
}

// resolveGraphTarget is resolveTarget for anything but a memory id, which
// needs no folded log.
func (mem *memory) resolveGraphTarget(ctx context.Context, id string) (string, error) {
	root := project.Root(ctx, mem.env.Base, mem.env.Checkout)

	repo, bare, qualified := splitQualified(id)
	if qualified && repo == graphconfig.ResolveAt(root).RepoID() {
		id, qualified = bare, false
	}

	switch {
	case qualified:
		if !mem.exports(ctx, root) {
			return "", fmt.Errorf("%s: %s", id, noExport)
		}

		statuses, _ := mem.graphStatuses(ctx)
		if _, found := statuses[id]; found {
			return id, nil
		}

		return "", fmt.Errorf("%s: %s", id, notConnected)
	case isGraphID(id):
		_, err := os.Stat(mem.graphPath(ctx, id))
		if err == nil {
			return id, nil
		}

		holders := mem.foreignHolders(ctx, root, id)

		switch len(holders) {
		case 0:
			return "", fmt.Errorf("%s: %s", id, notConnected)
		case 1:
			return holders[0], nil
		}

		return "", fmt.Errorf("%s: in several connected repos (%s); link one of them", id, strings.Join(holders, ", "))
	}

	return "", fmt.Errorf("%s: %w", id, ErrNoEntry)
}

// foreignHolders is every qualified id a connected repo holds the graph id
// under, sorted; none where the installed sdd has no export.
func (mem *memory) foreignHolders(ctx context.Context, root string, id string) []string {
	if !mem.exports(ctx, root) {
		return nil
	}

	statuses, _ := mem.graphStatuses(ctx)

	var holders []string

	for key := range statuses {
		if strings.HasSuffix(key, ":"+id) {
			holders = append(holders, key)
		}
	}

	slices.Sort(holders)

	return holders
}

// textLinks is every full id the text names, split into those that resolve,
// which link the entry in the form they are stored in, and those that do not,
// which stay plain text.
func (mem *memory) textLinks(ctx context.Context, folded *state, text string, self string) ([]string, []string) {
	var linked, unlinked []string

	rest := qualifiedIDPattern.ReplaceAllString(text, " ")

	found := qualifiedIDPattern.FindAllString(text, -1)
	found = append(found, graphIDPattern.FindAllString(rest, -1)...)
	found = append(found, memoryIDPattern.FindAllString(rest, -1)...)

	for _, id := range found {
		if id == self || slices.Contains(linked, id) || slices.Contains(unlinked, id) {
			continue
		}

		target, err := mem.resolveTarget(ctx, folded, id)

		switch {
		case err != nil:
			unlinked = append(unlinked, id)
		case !slices.Contains(linked, target):
			linked = append(linked, target)
		}
	}

	return linked, unlinked
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

	// A graph target is stored in the form it resolves to, qualified where a
	// connected repo holds it; the check below refuses one that resolves to
	// nothing.
	target := req.Target
	if kind == memorylog.KindLink && !strings.HasPrefix(target, "m-") {
		resolved, err := mem.resolveGraphTarget(ctx, target)
		if err == nil {
			target = resolved
		}
	}

	_, _, failure, ok := mem.change(ctx, req.ID, false, func(folded *state, held *entry) error {
		loaded = folded

		switch {
		case kind == memorylog.KindLink:
			_, err := mem.resolveTarget(ctx, folded, req.Target)

			return err
		case slices.Contains(held.textLinks, req.Target):
			return fmt.Errorf("%s is named in %s's text; supersede the entry instead", req.Target, held.id)
		case !slices.ContainsFunc(held.explicit, func(link explicitLink) bool { return link.target == req.Target }):
			return fmt.Errorf("%s does not link to %s", held.id, req.Target)
		}

		return nil
	}, kind, target, nil)
	if !ok {
		return failure
	}

	if kind == memorylog.KindLink {
		if !req.NoHeat {
			mem.warm(loaded, heatLink, target)
		}

		return done(fmt.Sprintf("%s linked to %s", req.ID, target))
	}

	return done(fmt.Sprintf("%s unlinked from %s", req.ID, req.Target))
}

// graphStatus is what became of each graph entry, as `sdd export` says: its
// status and the entry that gave it, where one did, by bare id for the local
// graph and by `<repo_id>:<id>` for a connected repo's. Repos are the
// connected repos the export carried, whose caches the key covers.
type graphStatus struct {
	Key      string                `json:"key"`
	Repos    []string              `json:"repos,omitempty"`
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
	local := graphconfig.ResolveAt(root).Entries
	cachePath := filepath.Join(mem.cacheDir(), "graph-status.json")

	var cached graphStatus
	if readJSON(cachePath, &cached) && cached.Key == mem.statusKey(local, cached.Repos) && cached.Statuses != nil {
		mem.graphKnown = cached.Statuses

		return cached.Statuses, true
	}

	if !mem.exports(ctx, root) {
		return nil, false
	}

	statuses, repos, err := mem.exportStatuses(ctx, root)
	if err != nil {
		if !mem.graphWarned {
			mem.graphWarned = true
			_, _ = fmt.Fprintf(mem.streams.Err, "%s: graph status unavailable (sdd export: %v)\n", self, err)
		}

		return nil, false
	}

	_ = writeJSON(cachePath, graphStatus{Key: mem.statusKey(local, repos), Repos: repos, Statuses: statuses})
	mem.graphKnown = statuses

	return statuses, true
}

// statusKey changes whenever the local graph or the cache of one of the
// connected repos changes: a `sdd repo sync` pulling new entries or statuses
// into a cache refreshes the flags. sdd keeps a connected repo's cache, a
// clone, under `sdd/<repo_id>` in the cache directory.
func (mem *memory) statusKey(local string, repos []string) string {
	parts := []string{graphKey(local)}

	for _, repo := range repos {
		cached := filepath.Join(mem.env.CacheRoot, "sdd", filepath.FromSlash(repo))
		parts = append(parts, repo+"="+graphKey(graphconfig.ResolveAt(cached).Entries))
	}

	return strings.Join(parts, " ")
}

// exportStatuses is every status `sdd export --dependencies all` reports, and
// the connected repos it carried, sorted.
func (mem *memory) exportStatuses(ctx context.Context, root string) (map[string]graphState, []string, error) {
	if mem.env.Graph == nil {
		return nil, nil, fmt.Errorf("no sdd to ask")
	}

	raw, err := mem.env.Graph.Export(ctx, root)
	if err != nil {
		return nil, nil, err
	}

	var exported struct {
		Repos []struct {
			RepoID  string `json:"repo_id"`
			Local   bool   `json:"local"`
			Entries []struct {
				ID       string `json:"id"`
				Status   string `json:"status"`
				StatusBy string `json:"status_by"`
			} `json:"entries"`
		} `json:"repos"`
	}

	err = json.Unmarshal(raw, &exported)
	if err != nil {
		return nil, nil, fmt.Errorf("reading its output: %w", err)
	}

	statuses := map[string]graphState{}
	local := 0

	var repos []string

	for _, repo := range exported.Repos {
		prefix := ""

		if !repo.Local {
			if repo.RepoID == "" {
				continue
			}

			prefix = repo.RepoID + ":"
			repos = append(repos, repo.RepoID)
		}

		for _, held := range repo.Entries {
			if held.ID == "" {
				continue
			}

			statuses[prefix+held.ID] = graphState{Status: held.Status, By: held.StatusBy}

			if repo.Local {
				local++
			}
		}
	}

	if local == 0 {
		return nil, nil, fmt.Errorf("its output carries no local entries with a status")
	}

	slices.Sort(repos)

	return statuses, slices.Compact(repos), nil
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
			if _, _, qualified := splitQualified(target); qualified {
				if !asked {
					statuses, _ = mem.graphStatuses(ctx)
					asked = true
				}

				// Without the export nothing says what became of it, so no flag.
				foreign, found := statuses[target]

				state := graphLinkState(foreign)
				if statuses != nil && !found {
					state = "gone"
				}

				if state != "" {
					held.linkFlags = append(held.linkFlags, "→ "+foreignName(target)+" "+state)
				}

				continue
			}

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
