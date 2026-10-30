package tools

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
)

const indexSchema = 3

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

const (
	nameMax    = 40
	summaryMax = 200
)

// spec is a version's `tool.json`.
type spec struct {
	Name       string      `json:"name"`
	Summary    string      `json:"summary"`
	Usage      string      `json:"usage"`
	Inputs     string      `json:"inputs,omitempty"`
	Outputs    string      `json:"outputs,omitempty"`
	Files      []string    `json:"files"`
	ForkedFrom *forkOrigin `json:"forked_from,omitempty"`
}

type forkOrigin struct {
	Project string `json:"project"`
	Tool    string `json:"tool"`
	V       int    `json:"v"`
}

type historyLine struct {
	V    int    `json:"v"`
	By   string `json:"by"`
	Role string `json:"role,omitempty"`
	At   string `json:"at"`
	Why  string `json:"why"`
}

type takeLine struct {
	V    int    `json:"v"`
	By   string `json:"by"`
	Role string `json:"role,omitempty"`
	At   string `json:"at"`
}

type fileHash struct {
	Name string `json:"name"`
	Sum  string `json:"sum"`
	Size int64  `json:"size"`
}

// prunedLine is a `pruned.jsonl` line: a version removed to make room, its
// files' hashes kept so a byte-identical add is still refused.
type prunedLine struct {
	V      int        `json:"v"`
	At     string     `json:"at"`
	Hashes []fileHash `json:"hashes"`
}

// indexed is one tool as `index.json` keeps it: enough for every read without
// opening the tool's folder.
type indexed struct {
	Version      int                   `json:"version"`
	Name         string                `json:"name"`
	Summary      string                `json:"summary"`
	Usage        string                `json:"usage"`
	Inputs       string                `json:"inputs,omitempty"`
	Outputs      string                `json:"outputs,omitempty"`
	Files        []string              `json:"files"`
	Described    string                `json:"described"`
	Takers       []string              `json:"takers"`
	Takes        int                   `json:"takes"`
	LastTake     string                `json:"last_take,omitempty"`
	Roles        []string              `json:"roles"`
	RegisteredBy string                `json:"registered_by,omitempty"`
	ForkedFrom   *forkOrigin           `json:"forked_from,omitempty"`
	Hashes       map[string][]fileHash `json:"hashes"`
	// Written is when each stored version was written, by its folder name:
	// what orders versions for pruning.
	Written map[string]string `json:"written"`
	// Pruned is the files of every version pruned to make room, by its folder
	// name.
	Pruned map[string][]fileHash `json:"pruned,omitempty"`
}

type index struct {
	Schema int                 `json:"schema"`
	Tools  map[string]*indexed `json:"tools"`
	Merged map[string]string   `json:"merged"`
	// Skipped counts malformed lines and unreadable tools the build passed over.
	Skipped int `json:"skipped,omitempty"`
}

func emptyIndex() *index {
	return &index{Schema: indexSchema, Tools: map[string]*indexed{}, Merged: map[string]string{}}
}

// store is one project's library.
type store struct {
	key   string
	root  string
	label string
	path  string
	own   bool
	index *index
}

func (held *store) tool(name string) (*indexed, bool) {
	if held.index == nil {
		return nil, false
	}

	tool, found := held.index.Tools[name]

	return tool, found
}

func (held *store) names() []string {
	if held.index == nil {
		return nil
	}

	names := make([]string, 0, len(held.index.Tools))
	for name := range held.index.Tools {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// describe is the text a tool's vector embeds.
func describe(tool spec) string {
	return strings.Join([]string{tool.Name, tool.Summary, tool.Usage, tool.Inputs, tool.Outputs}, "\n")
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)

	return hex.EncodeToString(digest[:])
}

func describedSum(text string) string { return sum([]byte(text))[:32] }

func versionDir(v int) string { return "v" + strconv.Itoa(v) }

// parseCurrent reads a `current` file: one version number.
func parseCurrent(data []byte) (int, bool) {
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))

	return v, err == nil && v > 0
}

// jsonLines decodes every line it can, counting those it cannot.
func jsonLines[T any](data []byte) ([]T, int) {
	var (
		lines   []T
		skipped int
	)

	for _, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}

		var line T

		err := json.Unmarshal(raw, &line)
		if err != nil {
			skipped++

			continue
		}

		lines = append(lines, line)
	}

	return lines, skipped
}

// readTool builds one tool's index row from its folder.
func readTool(folder *safefs.Dir, name string) (*indexed, error) {
	raw, err := folder.Read("current", currentLimit)
	if err != nil {
		return nil, err
	}

	current, valid := parseCurrent(raw)
	if !valid {
		return nil, fmt.Errorf("%s/current names no version", folder.Path())
	}

	row := &indexed{Version: current, Name: name, Hashes: map[string][]fileHash{}, Written: map[string]string{},
		Takers: []string{}, Roles: []string{}}

	listed, err := folder.Entries()
	if err != nil {
		return nil, err
	}

	for _, entry := range listed {
		v, isVersion := strings.CutPrefix(entry.Name(), "v")
		number, err := strconv.Atoi(v)

		if !isVersion || err != nil || number <= 0 || !entry.IsDir() {
			continue
		}

		hashes, held, err := readVersion(folder, number)
		if err != nil {
			if number == current {
				return nil, err
			}

			continue
		}

		row.Hashes[versionDir(number)] = hashes

		if number == current {
			row.Summary, row.Usage, row.Inputs, row.Outputs = held.Summary, held.Usage, held.Inputs, held.Outputs
			row.Files, row.ForkedFrom = held.Files, held.ForkedFrom
		}
	}

	_, found := row.Hashes[versionDir(current)]
	if !found {
		return nil, fmt.Errorf("%s has no %s", folder.Path(), versionDir(current))
	}

	err = row.readLines(folder)
	if err != nil {
		return nil, err
	}

	err = row.readPruned(folder)
	if err != nil {
		return nil, err
	}

	row.Described = describedSum(describe(row.spec()))

	return row, nil
}

func (row *indexed) spec() spec {
	return spec{Name: row.Name, Summary: row.Summary, Usage: row.Usage, Inputs: row.Inputs, Outputs: row.Outputs,
		Files: row.Files, ForkedFrom: row.ForkedFrom}
}

func (row *indexed) readLines(folder *safefs.Dir) error {
	lines, err := readJournal[historyLine](folder, historyJournal)
	if err != nil {
		return err
	}

	for _, line := range lines {
		if line.V == 1 && row.RegisteredBy == "" {
			row.RegisteredBy = line.By
		}

		_, stored := row.Hashes[versionDir(line.V)]
		if stored {
			row.Written[versionDir(line.V)] = line.At
		}

		row.addRole(line.Role)
	}

	taken, err := readJournal[takeLine](folder, takesJournal)
	if err != nil {
		return err
	}

	for _, line := range taken {
		row.Takes++
		row.LastTake = max(row.LastTake, line.At)
		row.addRole(line.Role)

		if line.By != "" && line.By != row.RegisteredBy && !slices.Contains(row.Takers, line.By) {
			row.Takers = append(row.Takers, line.By)
		}
	}

	return nil
}

func (row *indexed) readPruned(folder *safefs.Dir) error {
	lines, err := readJournal[prunedLine](folder, prunedJournal)
	if err != nil {
		return err
	}

	for _, line := range lines {
		_, stored := row.Hashes[versionDir(line.V)]
		if line.V <= 0 || stored {
			continue
		}

		if row.Pruned == nil {
			row.Pruned = map[string][]fileHash{}
		}

		row.Pruned[versionDir(line.V)] = line.Hashes
	}

	return nil
}

func (row *indexed) addRole(role string) {
	if role != "" && !slices.Contains(row.Roles, role) {
		row.Roles = append(row.Roles, role)
	}
}

// readVersion reads a version's `tool.json` and hashes its files.
func readVersion(folder *safefs.Dir, v int) ([]fileHash, spec, error) {
	version, err := folder.Sub(versionDir(v))
	if err != nil {
		return nil, spec{}, err
	}

	defer version.Close()

	files, err := version.Walk(versionLimit, nil)
	if err != nil {
		return nil, spec{}, err
	}

	var (
		held   spec
		hashes []fileHash
		found  bool
	)

	for _, file := range files {
		if file.Name == "tool.json" {
			err = json.Unmarshal(file.Data, &held)
			found = err == nil

			continue
		}

		hashes = append(hashes, fileHash{Name: file.Name, Sum: sum(file.Data), Size: int64(len(file.Data))})
	}

	if !found {
		return nil, spec{}, fmt.Errorf("%s/%s/tool.json is missing or unreadable", folder.Path(), versionDir(v))
	}

	if len(held.Files) == 0 {
		for _, hash := range hashes {
			held.Files = append(held.Files, hash.Name)
		}
	}

	held.Summary, held.Usage = clean(held.Summary, false), clean(held.Usage, true)
	held.Inputs, held.Outputs = clean(held.Inputs, true), clean(held.Outputs, true)

	return hashes, held, nil
}

// buildIndex reads every tool folder of a store. A tool that cannot be read is
// skipped and counted; the rest still serve.
func buildIndex(at string) (*index, error) {
	built := emptyIndex()

	root, err := safefs.OpenDir(at)
	if errors.Is(err, fs.ErrNotExist) {
		return built, nil
	}

	if err != nil {
		return nil, err
	}

	defer root.Close()

	listed, err := root.Entries()
	if err != nil {
		return nil, err
	}

	for _, entry := range listed {
		name := entry.Name()
		if !entry.IsDir() || !namePattern.MatchString(name) || len(name) > nameMax {
			continue
		}

		folder, err := root.Sub(name)
		if err != nil {
			built.Skipped++

			continue
		}

		survivor, err := folder.Read("merged", currentLimit+nameMax)
		if err == nil {
			built.Merged[name] = strings.TrimSpace(string(survivor))
			folder.Close()

			continue
		}

		row, err := readTool(folder, name)
		folder.Close()

		if err != nil {
			built.Skipped++

			continue
		}

		built.Tools[name] = row
	}

	return built, nil
}

// loadIndex reads a store's `index.json`, or nothing where it is missing,
// corrupt or of another schema.
func loadIndex(at string) (*index, bool) {
	root, err := safefs.OpenDir(at)
	if err != nil {
		return nil, false
	}

	defer root.Close()

	data, err := root.Read("index.json", indexLimit)
	if err != nil {
		return nil, false
	}

	var held index

	err = json.Unmarshal(data, &held)
	if err != nil || held.Schema != indexSchema || held.Tools == nil {
		return nil, false
	}

	if held.Merged == nil {
		held.Merged = map[string]string{}
	}

	for name, row := range held.Tools {
		if row == nil || row.Name != name || !namePattern.MatchString(name) {
			delete(held.Tools, name)

			continue
		}

		// A size no version could have makes the whole index suspect: it is
		// rebuilt from the folders rather than trusted for space or pruning.
		if !validSizes(row) {
			return nil, false
		}

		row.Summary, row.Usage = clean(row.Summary, false), clean(row.Usage, true)
		row.Inputs, row.Outputs = clean(row.Inputs, true), clean(row.Outputs, true)
	}

	return &held, true
}

// isStore says whether a folder holds a library: an index, a lock, or a tool
// folder with a `current`.
func isStore(folder *safefs.Dir) bool {
	for _, marker := range []string{"index.json", ".lock", space.LedgerName} {
		_, err := folder.FileInfo(marker)
		if err == nil {
			return true
		}
	}

	listed, err := folder.Entries()
	if err != nil {
		return false
	}

	for _, entry := range listed {
		if !entry.IsDir() || !namePattern.MatchString(entry.Name()) {
			continue
		}

		child, err := folder.Sub(entry.Name())
		if err != nil {
			continue
		}

		_, err = child.FileInfo("current")
		child.Close()

		if err == nil {
			return true
		}
	}

	return false
}

// storeIDs lists the id of every store in the home holding a tool library.
// A link or an unreadable folder is passed over.
func storeIDs(roots home.Roots) []string {
	top, err := safefs.OpenDir(roots.Stores())
	if err != nil {
		return nil
	}

	defer top.Close()

	listed, err := top.Entries()
	if err != nil {
		return nil
	}

	var ids []string

	for _, entry := range listed {
		if len(ids) >= safefs.WalkFiles || !entry.IsDir() {
			continue
		}

		held, err := top.Sub(entry.Name())
		if err != nil {
			continue
		}

		library, err := held.Sub(home.Tools)
		held.Close()

		if err != nil {
			continue
		}

		if isStore(library) {
			ids = append(ids, entry.Name())
		}

		library.Close()
	}

	slices.Sort(ids)

	return ids
}

// storeNames is each store's name as a person reads it, its repo_id where it
// has one; the own store, which may not exist yet, by its checkout.
func (lib *library) storeNames(ids []string) map[string]string {
	identities := lib.env.Roots.Identities()
	names := map[string]string{}

	for _, id := range ids {
		identity, found := identities[id]

		switch {
		case found:
			names[id] = identity.Name()
		case id == lib.key:
			names[id] = cmp.Or(lib.facts.RepoID, lib.facts.Origin, filepath.Base(lib.facts.Root))
		default:
			names[id] = id
		}
	}

	return names
}

// storeName is how the store id reads to a person, its id where it has no
// identity.
func (lib *library) storeName(id string) string {
	return lib.storeNames([]string{id})[id]
}

// labels names each store by the last segment of its name, adding leading
// segments only where two names would otherwise share a label.
func labels(names map[string]string) map[string]string {
	named := map[string]string{}
	depth := map[string]int{}

	for id := range names {
		depth[id] = 1
	}

	for range safefs.WalkDepth + 1 {
		named = map[string]string{}
		count := map[string]int{}

		for id, name := range names {
			label := lastSegments(name, depth[id])
			named[id] = label
			count[label]++
		}

		clash := false

		for id, name := range names {
			if count[named[id]] > 1 && depth[id] < len(strings.Split(name, "/")) {
				depth[id]++
				clash = true
			}
		}

		if !clash {
			break
		}
	}

	return named
}

func lastSegments(key string, n int) string {
	segments := strings.Split(key, "/")
	n = min(n, len(segments))

	return strings.Join(segments[len(segments)-n:], "/")
}

// clean strips control characters from a stored field: tabs and newlines stay
// where multiline is set, and go otherwise.
func clean(text string, multiline bool) string {
	return strings.Map(func(char rune) rune {
		switch {
		case multiline && (char == '\n' || char == '\t'):
			return char
		case char < 0x20 || char == 0x7f || (char >= 0x80 && char < 0xa0):
			return -1
		}

		return char
	}, text)
}
