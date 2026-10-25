package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/writers"
)

// A store's key is the project's declared `repo_id`, which any checkout can
// declare. The right to write it belongs to the checkouts listed in the
// project's one writers list, which agentic-memory reads too; a library's own
// `writers.json`, as an earlier release kept it, moves into that list.

var ErrNotAWriter = errors.New("this library belongs to other checkouts")

func (lib *library) writersList() writers.List {
	return writers.List{Home: lib.env.Home, Key: lib.key, Lookup: lib.env.Checkout,
		Legacy: filepath.Join(lib.ownDir(), "writers.json")}
}

// writer admits this checkout to write the own store, claiming an unlisted
// project for it, or says why not.
func (lib *library) writer(ctx context.Context) error {
	err := lib.writersList().Admit(ctx, lib.env.Base)

	var refusal writers.NotListed

	switch {
	case err == nil:
		return nil
	case errors.As(err, &refusal):
		return fmt.Errorf("%w: %s; run `%s trust %s` from one of them to let this one write", ErrNotAWriter,
			strings.Join(refusal.Checkouts, ", "), self, refusal.Here)
	case errors.Is(err, writers.ErrUnreadable):
		return fmt.Errorf("%w; no write lands until %s is mended", err, lib.writersList().Path())
	}

	return err
}

func (lib *library) trust(ctx context.Context, req request) result {
	at := req.Path
	if strings.TrimSpace(at) == "" {
		return refused("trust needs the path of a checkout")
	}

	if !filepath.IsAbs(at) {
		at = filepath.Join(lib.env.Base, at)
	}

	_, err := lib.writersList().CommonDir(ctx, filepath.Clean(at))
	if err != nil {
		return refused("%v", err)
	}

	err = lib.writer(ctx)
	if err != nil {
		return refused("%v", err)
	}

	checkout, err := lib.writersList().Trust(ctx, lib.env.Base, filepath.Clean(at))
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("trusted %s: that checkout writes this project's tool library and memory now", checkout))
}
