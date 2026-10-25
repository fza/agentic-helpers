package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// A store's key is the project's declared `repo_id`, which any checkout can
// declare. So the right to write a store belongs to checkouts, by their git
// common directory, which a cloned repository cannot declare for itself:
// `writers.json` lists them, the first write claims an unlisted store, and a
// listed checkout trusts another with `trust`.

const writersLimit = 64 << 10

var ErrNotAWriter = errors.New("this library belongs to other checkouts")

// writers is the store's list, whether it has one, or why it cannot be read.
func (lib *library) writers() ([]string, bool, error) {
	store, err := openDir(lib.ownDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, err
	}

	defer store.close()

	data, err := store.read("writers.json", writersLimit)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, true, err
	}

	var listed []string

	err = json.Unmarshal(data, &listed)
	if err != nil {
		return nil, true, fmt.Errorf("writers.json is no JSON array of paths: %w", err)
	}

	for _, at := range listed {
		if !filepath.IsAbs(at) || filepath.Clean(at) != at {
			return nil, true, fmt.Errorf("writers.json names %q, which is no clean absolute path", at)
		}
	}

	return listed, true, nil
}

// commonDir is the git common directory of the checkout holding at.
func (lib *library) commonDir(ctx context.Context, at string) (string, error) {
	if lib.env.Checkout == nil {
		return "", errors.New("no git to ask")
	}

	common, err := lib.env.Checkout.CommonDir(ctx, at)
	if err != nil || common == "" {
		return "", fmt.Errorf("%s is no git checkout", at)
	}

	if !filepath.IsAbs(common) {
		common = filepath.Join(at, common)
	}

	return filepath.Clean(common), nil
}

// writer says whether this checkout may write the own store: listed, or the
// store unclaimed. A list that cannot be read lets no checkout write.
func (lib *library) writer(ctx context.Context) error {
	listed, claimed, err := lib.writers()
	if err != nil {
		return fmt.Errorf("%w; no write lands until writers.json is mended", err)
	}

	if !claimed {
		return nil
	}

	own, err := lib.commonDir(ctx, lib.env.Base)
	if err != nil {
		return err
	}

	if slices.Contains(listed, own) {
		return nil
	}

	checkouts := make([]string, 0, len(listed))
	for _, common := range listed {
		checkouts = append(checkouts, filepath.Dir(common))
	}

	return fmt.Errorf("%w: %s; run `%s trust %s` from one of them to let this one write", ErrNotAWriter,
		strings.Join(checkouts, ", "), self, lib.checkout(ctx))
}

// claim lists this checkout where the store has no list yet. The caller holds
// the lock.
func (lib *library) claim(ctx context.Context, store *dir) error {
	_, claimed, err := lib.writers()
	if err != nil || claimed {
		return err
	}

	own, err := lib.commonDir(ctx, lib.env.Base)
	if err != nil {
		return err
	}

	return writeWriters(store, []string{own})
}

func writeWriters(store *dir, listed []string) error {
	data, err := json.Marshal(listed)
	if err != nil {
		return fmt.Errorf("encoding writers.json: %w", err)
	}

	return store.replace("writers.json", append(data, '\n'))
}

func (lib *library) trust(ctx context.Context, req request) result {
	at := req.Path
	if strings.TrimSpace(at) == "" {
		return refused("trust needs the path of a checkout")
	}

	if !filepath.IsAbs(at) {
		at = filepath.Join(lib.env.Base, at)
	}

	common, err := lib.commonDir(ctx, filepath.Clean(at))
	if err != nil {
		return refused("%v", err)
	}

	err = lib.locked(ctx, func() error {
		store, err := lib.ownStore()
		if err != nil {
			return err
		}

		defer store.close()

		listed, _, err := lib.writers()
		if err != nil {
			return err
		}

		if slices.Contains(listed, common) {
			return nil
		}

		return writeWriters(store, append(listed, common))
	})
	if err != nil {
		return refused("%v", err)
	}

	return done(fmt.Sprintf("trusted %s: that checkout writes this library now", filepath.Dir(common)))
}
