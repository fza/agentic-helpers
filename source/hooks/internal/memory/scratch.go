package memory

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// transcriptReadLimit bounds what the migration reads of one transcript to
// find the seat its session held.
const transcriptReadLimit = 64 << 20

// seatInTranscript is a line naming the seat a session held: the session
// start's `agentic-memory: seat <name>,`, a claim at a shell, or a claim
// through the MCP tool.
var seatInTranscript = regexp.MustCompile(
	`agentic-memory: seat ([^\s,"\\]+),|agentic-memory claim ([^\s"\\-][^\s"\\]*)|memory__claim","input":\{"name":"([^"\\]+)"`)

// scratchRoot is the main checkout, where agentic-scratch keeps
// `.tmp/claude/`, even for a session in a worktree.
func (mem *memory) scratchRoot() string {
	if mem.facts.CommonDir != "" {
		return filepath.Dir(mem.facts.CommonDir)
	}

	return cmp.Or(mem.facts.Root, mem.env.Base)
}

// seatScratch makes the seat's scratch folder and answers the line naming it,
// or nothing where scratch is off or the project carries no `.tmp/`.
func (mem *memory) seatScratch(name string) string {
	if !pieces.Scratch.On(mem.options()) {
		return ""
	}

	return scratch.Seat(mem.scratchRoot(), name, mem.seatKey(name))
}

// claimScratch is seatScratch at a claim, which first gives a git checkout
// lacking `.tmp/` one: claiming a seat opts the project in, and agentic-scratch
// itself never creates the folder. `.tmp/` then joins git's global excludes,
// before anything lands in it.
func (mem *memory) claimScratch(ctx context.Context, name string) []string {
	if !pieces.Scratch.On(mem.options()) || mem.facts.CommonDir == "" {
		return []string{mem.seatScratch(name)}
	}

	root := mem.scratchRoot()
	_ = safefs.Mkdir(root, filepath.Join(root, ".tmp"), 0o755)

	lines := []string{mem.seatScratch(name)}

	ignored, err := gitignore.Global{Git: mem.env.Git, Home: mem.env.Home, Getenv: mem.env.Getenv}.
		Ensure(ctx, self, root, ".tmp", ".tmp/")

	switch {
	case err != nil:
		lines = append(lines, fmt.Sprintf("%s: .tmp/ stays unignored by git: %v", self, err))
	case ignored != "":
		lines = append(lines, ignored)
	}

	return lines
}

// sweepSeat removes the seat's scratch folder where `scratch_sweep` names the
// moment that has come.
func (mem *memory) sweepSeat(ctx context.Context, name string, moment string) {
	options := mem.options()
	if !pieces.Scratch.On(options) || options.ScratchSweep != moment {
		return
	}

	scratch.RemoveSeat(ctx, mem.scratchRoot(), name, mem.seatKey(name))
}

// moveScratch carries a seat's folder to the seat a rename or a move gave it.
func (mem *memory) moveScratch(from string, fromKey string, to string, toKey string) {
	if !pieces.Scratch.On(mem.options()) {
		return
	}

	scratch.MoveSeat(mem.scratchRoot(), from, fromKey, to, toKey)
}

// tidyScratch moves each old session folder into its seat's folder and reaps
// every seat folder idle a week, at session start.
func (mem *memory) tidyScratch(ctx context.Context, transcriptPath string) {
	registry, found := mem.registry()
	if !found || !pieces.Scratch.On(mem.options()) {
		return
	}

	ids := map[string]bool{}
	for _, id := range registry.IDs() {
		ids[id] = true
	}

	root := mem.scratchRoot()

	scratch.Migrate(root, func(session string) (string, string, bool) {
		key := mem.seatOfSession(registry, session, transcriptPath)
		if key == "" {
			return "", "", false
		}

		return registry.NameOf(key), key, true
	})

	scratch.ReapSeats(ctx, root, func(id string) bool { return ids[id] }, time.Now())
}

// seatOfSession is the id of the seat session held: as a seat's claim or last
// claim records it, else as its transcript beside transcriptPath names it. A
// session neither names is remembered, so its transcript is read once.
func (mem *memory) seatOfSession(registry seat.Registry, session string, transcriptPath string) string {
	dir := mem.seatsDir()

	key, _ := seat.OfSession(dir, session)
	if key != "" {
		return key
	}

	for _, id := range registry.IDs() {
		last, found := seat.ReadLast(dir, id)
		if found && last.SessionID == session {
			return id
		}
	}

	unmapped := filepath.Join(mem.cacheDir(), "scratch-unmapped")
	if transcriptPath == "" || mem.listed(unmapped, session) {
		return ""
	}

	name := seatNamedIn(filepath.Join(filepath.Dir(transcriptPath), session+".jsonl"))

	id, known := registry.IDOf(name)
	if name != "" && known {
		return id
	}

	_ = safefs.MkdirAll(mem.env.Roots.Cache, filepath.Dir(unmapped), 0o700)

	file, err := safefs.Append(mem.env.Roots.Cache, unmapped, 0o600)
	if err == nil {
		_, _ = file.WriteString(session + "\n")
		_ = file.Close()
	}

	return ""
}

func (mem *memory) listed(path string, line string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if scanner.Text() == line {
			return true
		}
	}

	return false
}

// seatNamedIn is the last seat a transcript names its session holding, or
// nothing.
func seatNamedIn(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, transcriptReadLimit))
	if err != nil {
		return ""
	}

	matches := seatInTranscript.FindAllSubmatch(data, -1)
	if len(matches) == 0 {
		return ""
	}

	last := matches[len(matches)-1]

	for _, group := range last[1:] {
		if len(group) > 0 {
			return strings.TrimSpace(string(group))
		}
	}

	return ""
}
