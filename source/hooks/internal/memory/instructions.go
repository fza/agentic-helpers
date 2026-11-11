package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// loadedInstructions is the instruction files a session loaded, each path
// with the content hash it was last told of; an empty hash is a file gone.
type loadedInstructions struct {
	Files map[string]string `json:"files"`
}

// ownEditTools change a file as the session itself, which needs no telling.
var ownEditTools = []string{"Edit", "Write", "NotebookEdit"}

func (mem *memory) instructionsKey(session string) string {
	return mem.sessionKey("instructions/", session)
}

// instructions records a file Claude Code reports through InstructionsLoaded,
// with its hash, for the session that loaded it. No seat is needed.
func (mem *memory) instructions(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	if payload.SessionID == "" || payload.FilePath == "" {
		return 0
	}

	path := mem.absolute(payload.FilePath)
	hash, _ := fileHash(path)

	mem.updateInstructions(ctx, payload.SessionID, func(loaded *loadedInstructions, _ bool) bool {
		held, known := loaded.Files[path]
		if known && held == hash {
			return false
		}

		loaded.Files[path] = hash

		return true
	})

	return 0
}

// instructionsChanged hashes every instruction file the session loaded again
// and answers one line per file that changed since it was last told; a file
// the call itself edited (ownEdit) is taken in silently, and one that cannot
// be read waits until it reads, since that says nothing of a change. A session no
// InstructionsLoaded event reached, from a Claude Code firing none, watches
// the fixed set seedInstructions names.
func (mem *memory) instructionsChanged(ctx context.Context, payload hookio.Payload) []string {
	if payload.SessionID == "" {
		return nil
	}

	ownEdit := ""
	if slices.Contains(ownEditTools, payload.ToolName) {
		edited := payload.ToolInput.FilePath
		if edited == "" {
			edited = payload.ToolInput.NotebookPath
		}

		if edited != "" {
			ownEdit = mem.absolute(edited)
		}
	}

	var lines []string

	mem.updateInstructions(ctx, payload.SessionID, func(loaded *loadedInstructions, recorded bool) bool {
		changed := false

		if !recorded {
			for _, path := range mem.seedInstructions() {
				loaded.Files[path], _ = fileHash(path)
			}

			changed = true
		}

		paths := make([]string, 0, len(loaded.Files))
		for path := range loaded.Files {
			paths = append(paths, path)
		}

		slices.Sort(paths)

		for _, path := range paths {
			hash, known := tree{}.contentHash(path)
			if !known || hash == loaded.Files[path] {
				continue
			}

			loaded.Files[path] = hash
			changed = true

			if path != ownEdit {
				lines = append(lines, fmt.Sprintf("%s: %s changed since loaded; re-read it", self, mem.shownPath(path)))
			}
		}

		return changed
	})

	return lines
}

// updateInstructions changes the session's record under the slot file's
// lock, since Claude Code may report several loaded files at once; change
// learns whether a record was there and answers whether it changed anything.
func (mem *memory) updateInstructions(ctx context.Context, session string, change func(*loadedInstructions, bool) bool) {
	mem.updateSlot(ctx, mem.instructionsKey(session), func(value []byte, found bool) (slots.Fitted, error) {
		loaded := loadedInstructions{}

		if found {
			_ = json.Unmarshal(value, &loaded)
		}

		if loaded.Files == nil {
			loaded.Files = map[string]string{}
		}

		if !change(&loaded, found) && found {
			return nil, slots.ErrUnchanged
		}

		return &loaded, nil
	})
}

// seedInstructions is the fixed set a session watches where Claude Code
// reports no loaded file: the project's CLAUDE.md and CLAUDE.local.md, the
// files they import with `@`, and the user's ~/.claude/CLAUDE.md. Only files
// there count, as only those were loaded.
func (mem *memory) seedInstructions() []string {
	var seeded []string

	add := func(path string) bool {
		if slices.Contains(seeded, path) {
			return false
		}

		_, found := fileHash(path)
		if found {
			seeded = append(seeded, path)
		}

		return found
	}

	for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md"} {
		path := filepath.Join(mem.env.Base, name)
		if !add(path) {
			continue
		}

		for _, imported := range mem.imports(path) {
			add(imported)
		}
	}

	add(filepath.Join(mem.env.Home, ".claude", "CLAUDE.md"))

	return seeded
}

// imports is every `@path` the instruction file at path names outside code,
// resolved against the file's folder, `@~/` against the home.
func (mem *memory) imports(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()

	var found []string

	fenced := false
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced

			continue
		}

		if fenced {
			continue
		}

		for _, token := range strings.Fields(outsideCodeSpans(line)) {
			named, isImport := strings.CutPrefix(token, "@")
			named = strings.TrimRight(named, ".,;:!?)")

			if !isImport || named == "" {
				continue
			}

			found = append(found, mem.importPath(filepath.Dir(path), named))
		}
	}

	return found
}

func (mem *memory) importPath(dir string, named string) string {
	inHome, fromHome := strings.CutPrefix(named, "~/")
	if fromHome {
		return filepath.Join(mem.env.Home, inHome)
	}

	if filepath.IsAbs(named) {
		return filepath.Clean(named)
	}

	return filepath.Join(dir, named)
}

// outsideCodeSpans blanks every backtick span of line, where an `@` names
// nothing to import.
func outsideCodeSpans(line string) string {
	parts := strings.Split(line, "`")

	for index := 1; index < len(parts); index += 2 {
		parts[index] = " "
	}

	return strings.Join(parts, " ")
}

func (mem *memory) absolute(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(mem.env.Base, path)
}

// shownPath names path from the project's root where it lies inside it, from
// the home as `~/` where it lies there, else as recorded.
func (mem *memory) shownPath(path string) string {
	for _, root := range []struct{ dir, prefix string }{{mem.env.Base, ""}, {mem.env.Home, "~/"}} {
		if root.dir == "" {
			continue
		}

		inside, err := filepath.Rel(root.dir, path)
		if err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return root.prefix + inside
		}
	}

	return path
}
