package memory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/writers"
)

// The store is keyed by the project's declared `repo_id`, which any checkout
// can declare. The right to write it belongs to the checkouts listed in the
// project's one writers list, which agentic-tools reads too.

// readOnly are the calls that change no store and answer anywhere.
var readOnly = []string{"help", "--help", "-h", "who", "holder"}

func (mem *memory) writersList() writers.List {
	return writers.List{Home: mem.env.Home, Key: mem.key, Lookup: mem.env.Checkout}
}

// admission says why this checkout may not run call against the store, or
// nothing where it may: listed, claiming an unlisted project, or touching no
// store at all. A store keyed by a directory outside git has no declared key
// to forge, and only a call creating a store claims one not there yet.
func (mem *memory) admission(ctx context.Context, call string) error {
	if !mem.gitKeyed || slices.Contains(readOnly, call) {
		return nil
	}

	if !mem.storeExists() && call != "claim" && call != "trust" {
		return nil
	}

	err := mem.writersList().Admit(ctx, mem.env.Base)

	var refusal writers.NotListed

	switch {
	case err == nil:
		return nil
	case errors.As(err, &refusal):
		return fmt.Errorf("this memory belongs to other checkouts: %s; run `%s trust %s` from one of them to let this one write",
			strings.Join(refusal.Checkouts, ", "), self, refusal.Here)
	case errors.Is(err, writers.ErrUnreadable):
		return fmt.Errorf("%w; no call lands until %s is mended", err, mem.writersList().Path())
	}

	return err
}

func (mem *memory) trust(ctx context.Context, req request) result {
	at := req.Path
	if strings.TrimSpace(at) == "" {
		return refused("trust needs the path of a checkout")
	}

	if !filepath.IsAbs(at) {
		at = filepath.Join(mem.env.Base, at)
	}

	checkout, err := mem.writersList().Trust(ctx, mem.env.Base, filepath.Clean(at))
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("trusted %s: that checkout writes this project's memory and tool library now", checkout))
}
