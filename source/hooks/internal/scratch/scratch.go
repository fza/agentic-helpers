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

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
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
func Main(ctx context.Context, args []string, root string, streams hookio.Streams) int {
	if len(args) != 1 || (args[0] != "end" && args[0] != "start") {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	if !isDir(filepath.Join(root, ".tmp")) {
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
		err = start(ctx, root, streams.Out)
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

func start(ctx context.Context, root string, out io.Writer) error {
	var said []string

	gone := reap(ctx, root, time.Now())
	if gone > 0 {
		said = append(said, fmt.Sprintf("Scratch: reaped %d session directory(s) nobody came back to.", gone))
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
