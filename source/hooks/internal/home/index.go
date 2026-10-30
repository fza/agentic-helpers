package home

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
)

const indexFormat = 1

// index is `<data>/index.json`: every identifier of every store, and the
// legacy key of every unclaimed store, each naming the stores holding it.
// It is a cache of the identities and nothing else: whenever it is missing,
// unreadable or names an identifier its store does not hold, it is rebuilt
// from them.
type index struct {
	Format      int                 `json:"format"`
	Identifiers map[string][]string `json:"identifiers"`
	Unclaimed   map[string][]string `json:"unclaimed"`
}

func pathKey(commonDir string, root string) string {
	if commonDir != "" {
		return "path:" + commonDir
	}

	return "dir:" + root
}

func inodeKey(volume string, inode uint64) string {
	if volume == "" {
		return ""
	}

	return "inode:" + volume + ":" + strconv.FormatUint(inode, 10)
}

func repoKey(repoID string) string { return "repo:" + repoID }

func originKey(origin string) string { return "origin:" + origin }

func aliasKey(id string) string { return "alias:" + id }

// identifiers is every key the identity is matched by.
func (identity Identity) identifiers() []string {
	var keys []string

	for _, admitted := range identity.Checkouts {
		keys = append(keys, pathKey(admitted.CommonDir, admitted.Path))
		if inode := inodeKey(admitted.Volume, admitted.Inode); inode != "" {
			keys = append(keys, inode)
		}
	}

	for _, repoID := range identity.RepoIDs {
		keys = append(keys, repoKey(repoID))
	}

	for _, origin := range identity.Origins {
		keys = append(keys, originKey(origin))
	}

	for _, merged := range identity.Merged {
		keys = append(keys, aliasKey(merged))
	}

	return keys
}

func (identity Identity) holds(key string) bool {
	return slices.Contains(identity.identifiers(), key)
}

func buildIndex(stores map[string]Identity, unclaimed map[string]Identity) index {
	built := index{Format: indexFormat, Identifiers: map[string][]string{}, Unclaimed: map[string][]string{}}

	for _, id := range sortedIDs(stores) {
		for _, key := range stores[id].identifiers() {
			if !slices.Contains(built.Identifiers[key], id) {
				built.Identifiers[key] = append(built.Identifiers[key], id)
			}
		}
	}

	for _, id := range sortedIDs(unclaimed) {
		for _, legacy := range unclaimed[id].LegacyKeys {
			built.Unclaimed[legacy] = append(built.Unclaimed[legacy], id)
		}
	}

	return built
}

func sortedIDs(held map[string]Identity) []string {
	ids := make([]string, 0, len(held))
	for id := range held {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	return ids
}

func (roots Roots) readIndex() (index, bool) {
	data, err := os.ReadFile(roots.index())
	if err != nil {
		return index{}, false
	}

	var held index

	err = json.Unmarshal(data, &held)
	if err != nil || held.Format != indexFormat || held.Identifiers == nil || held.Unclaimed == nil {
		return index{}, false
	}

	return held, true
}

func (roots Roots) writeIndex(held index) error {
	data, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return err
	}

	return atomicfile.Write(roots.Data, roots.index(), append(data, '\n'), 0o600)
}

// RebuildIndex rebuilds `<data>/index.json` from every identity.
func (roots Roots) RebuildIndex() error {
	return roots.writeIndex(buildIndex(roots.Identities(), readAll(roots.unclaimed())))
}
