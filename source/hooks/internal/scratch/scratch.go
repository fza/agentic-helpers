// Package scratch keeps a project's scratch directory from becoming a
// graveyard.
//
// A session writes its throwaway under `.tmp/claude/<session-id>/`, and that
// directory goes when the session ends. A directory abandoned by a session that
// crashed is reaped at a later session start once it is a week old.
//
// Only a directory named for a session is ever removed. A name that does not
// parse as a session identifier is somebody's deliberate keep, and survives
// whatever its age.
package scratch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/gitignore"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
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
		err = start(ctx, env, streams.Out)
	}

	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, err)

		return 1
	}

	return 0
}

func sessionsDir(root string) string {
	return filepath.Join(root, ".tmp", "claude")
}

func end(ctx context.Context, root string, session string, out io.Writer) error {
	if !isSession(session) {
		return nil
	}

	held := filepath.Join(sessionsDir(root), session)
	if !isDir(held) || !remove(ctx, held) {
		return nil
	}

	_, err := fmt.Fprintf(out, "swept %s\n", held)
	if err != nil {
		return fmt.Errorf("reporting the sweep: %w", err)
	}

	return nil
}

func start(ctx context.Context, env Env, out io.Writer) error {
	var said []string

	gone := reap(ctx, env.Root, time.Now())
	if gone > 0 {
		said = append(said, fmt.Sprintf("Scratch: reaped %d session directory(s) nobody came back to.", gone))
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
	entries, err := os.ReadDir(sessionsDir(root))
	if err != nil {
		return 0
	}

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

		if remove(ctx, filepath.Join(sessionsDir(root), entry.Name())) {
			gone++
		}
	}

	return gone
}

// remove reports only whether the directory went. A failure leaves nothing a
// hook's caller could act on, so the next start retries it.
func remove(ctx context.Context, directory string) bool {
	if ctx.Err() != nil {
		return false
	}

	return os.RemoveAll(directory) == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
