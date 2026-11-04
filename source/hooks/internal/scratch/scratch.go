// Package scratch keeps a project's scratch directory from becoming a
// graveyard.
//
// `.tmp/seats/` belongs to agentic-helpers alone. A session holding no seat
// writes its throwaway under `.tmp/seats/<session-id>/`, and that directory
// goes when the session ends. A directory abandoned by a session that crashed
// is reaped at a later session start once it is a week old. A seated session's
// `.tmp/seats/<seat>/` is agentic-memory's to make and sweep, through seats.go.
// Work a later session reads lives under `.tmp/keep/`, which nothing sweeps.
//
// Only a directory named for a session, or a seat folder carrying its marker,
// is ever removed. Anything else found under `.tmp/seats/` or the older
// `.tmp/claude/` is somebody's keep: relocate.go moves it to `.tmp/keep/`,
// never removing it.
package scratch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

// Matching this shape is the whole safety of the sweep.
var sessionPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var ErrUsage = errors.New("usage: agentic-scratch end|start")

// Long enough that a session resumed the next morning still finds its work.
const orphanAge = 7 * 24 * time.Hour

func isSession(name string) bool {
	return sessionPattern.MatchString(name)
}

// Main runs one hook invocation against the checkout at root. A project opts
// in by carrying `.tmp/`; without it nothing is read, created or said.
// Env is the project the sweep works in, and where the plugin's options are
// read: the sweep's own switch.
// Git, run in the project, and Home find git's global excludes file, where
// `.tmp/` is added unless git ignores it already; a nil Git adds nothing.
type Env struct {
	Root     string
	Settings []string
	Getenv   func(string) string
	Git      gitignore.Git
	Home     string
}

func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) != 1 || (args[0] != "end" && args[0] != "start") {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	options, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		options = pluginconfig.Defaults()
	}

	root := env.Root
	if !pieces.Scratch.On(options) || !isDir(filepath.Join(root, ".tmp")) {
		return 0
	}

	// A payload the client garbled names no session, and removes nothing.
	payload, err := hookio.ReadPayload(streams.In)
	if err != nil {
		payload = hookio.Payload{}
	}

	if args[0] == "end" {
		err = end(ctx, root, payload.SessionID, streams.Out)
	} else {
		err = start(ctx, env, options, streams.Out)
	}

	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, err)

		return 1
	}

	return 0
}

func seatsDir(root string) string {
	return filepath.Join(root, ".tmp", "seats")
}

func end(ctx context.Context, root string, session string, out io.Writer) error {
	if !isSession(session) {
		return nil
	}

	held := filepath.Join(seatsDir(root), session)
	if !remove(ctx, root, held) {
		return nil
	}

	_, err := fmt.Fprintf(out, "swept %s\n", held)
	if err != nil {
		return fmt.Errorf("reporting the sweep: %w", err)
	}

	return nil
}

func start(ctx context.Context, env Env, options pluginconfig.Options, out io.Writer) error {
	var said []string

	// With memory on, agentic-memory moves the marked seat folders, since only
	// its store says whose a marker names; with memory off any seat id counts.
	if !pieces.Memory.On(options) {
		said = append(said, RelocateSeats(env.Root, seat.IsID)...)
	}

	said = append(said, Relocate(ctx, env.Root, time.Now())...)

	gone := reap(ctx, env.Root, time.Now())
	if gone > 0 {
		said = append(said, fmt.Sprintf("Scratch: reaped %d session directory(s) nobody came back to.", gone))
	}

	// With memory off nothing else reaps a seat's folder; any marker naming a
	// seat id then counts, since no store is read to say whose it is.
	if !pieces.Memory.On(options) {
		seats := ReapSeats(ctx, env.Root, seat.IsID, time.Now())
		if seats > 0 {
			said = append(said, fmt.Sprintf("Scratch: reaped %d seat folder(s) nobody came back to.", seats))
		}
	}

	// The scratch directory holds a session's throwaway and drafts, wrong in
	// any commit of any repository.
	if env.Git != nil {
		ignored, err := gitignore.Global{Git: env.Git, Home: env.Home, Getenv: env.Getenv}.
			Ensure(ctx, "agentic-scratch", env.Root, ".tmp", ".tmp/")

		switch {
		case err != nil:
			said = append(said, fmt.Sprintf("agentic-scratch: .tmp/ stays unignored by git: %v", err))
		case ignored != "":
			said = append(said, ignored)
		}
	}

	if len(said) == 0 {
		return nil
	}

	_, err := fmt.Fprintln(out, strings.Join(said, "\n"))
	if err != nil {
		return fmt.Errorf("reporting the start sweep: %w", err)
	}

	return nil
}

func reap(ctx context.Context, root string, now time.Time) int {
	seats, entries := listSeats(root)
	if seats == nil {
		return 0
	}
	defer seats.Close()

	gone := 0
	stale := now.Add(-orphanAge)

	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}

		if !entry.IsDir() || !isSession(entry.Name()) {
			continue
		}

		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(stale) {
			continue
		}

		if removeIn(ctx, seats, entry.Name()) {
			gone++
		}
	}

	return gone
}

// listSeats opens `.tmp/seats/` through safefs, following no link from root
// down, and lists it; nil where it is no folder of the project's own. A
// committed `.tmp` or `.tmp/seats` link would otherwise hand the sweep any
// folder on the machine, Claude Code's file-history among them, whose
// session-named folders it would remove.
func listSeats(root string) (*safefs.Dir, []fs.DirEntry) {
	seats, err := safefs.At(root, seatsDir(root))
	if err != nil {
		return nil, nil
	}

	entries, err := seats.Entries()
	if err != nil {
		seats.Close()

		return nil, nil
	}

	return seats, entries
}

// remove removes the folder below root through safefs, as every other
// remover there does, and reports only whether it went. A failure leaves
// nothing a hook's caller could act on, so the next start retries it.
func remove(ctx context.Context, root string, directory string) bool {
	held, name, err := safefs.Parent(root, directory)
	if err != nil {
		return false
	}
	defer held.Close()

	return removeIn(ctx, held, name)
}

// removeIn removes the folder name below held, refusing a link or a file at
// the name.
func removeIn(ctx context.Context, held *safefs.Dir, name string) bool {
	if ctx.Err() != nil {
		return false
	}

	folder, err := held.Sub(name)
	if err != nil {
		return false
	}

	folder.Close()

	return held.RemoveAll(name) == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
