package grounding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddfork"
)

// The gate reads sdd commands by the rules of the fork at github.com/fza/sdd.
// With another sdd installed it steps aside for the session rather than refuse
// every call: the agent cannot install sdd itself, and a session kept from
// every tool gains nothing. Whether sdd is the fork is asked once, as the
// session starts, and the answer kept beside the session's ledger, which the
// session's end and the orphan reap remove with it.
func stepAsideWithoutFork(ctx context.Context, env Env, payload hookio.Payload, streams hookio.Streams) {
	path := asideMarker(env.ledgerDir(), payload)
	if path == "" {
		return
	}

	printed, fork := sddfork.Check(ctx, env.Runner, env.project())
	if fork {
		return
	}

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err == nil {
		err = os.WriteFile(path, []byte(printed+"\n"), 0o644)
	}

	if err != nil {
		return
	}

	_, _ = fmt.Fprintf(streams.Out, "agentic-helpers: the grounding gate is off for this session, since `sdd --version` printed %s "+
		"and the gate needs sdd built from %s; install it with `%s` and start a new session\n",
		printed, sddfork.Source, sddfork.Install)
}

func steppedAside(env Env, payload hookio.Payload) bool {
	path := asideMarker(env.ledgerDir(), payload)
	if path == "" {
		return false
	}

	_, err := os.Stat(path)

	return err == nil
}

func asideMarker(dir string, payload hookio.Payload) string {
	session := strings.ReplaceAll(payload.SessionID, "/", "-")
	if session == "" {
		return ""
	}

	return filepath.Join(dir, session+".unforked.json")
}
