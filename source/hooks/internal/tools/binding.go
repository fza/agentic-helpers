package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The own store is the one the home maps the checkout to (internal/home), as
// agentic-memory's is. A checkout matching none gets a store of its own,
// silently, at its first write; where the mapping is the user's call, every
// write is refused with the question, which `agentic-memory bind` answers.

// memoryBinary answers a store question: the mapping is shared, and
// agentic-memory carries `bind` and `merge`.
const memoryBinary = "agentic-memory"

var ErrUnbound = errors.New("this checkout is bound to no store yet")

// writer makes sure the own store exists to write, creating it where no store
// matched the checkout, or says why it cannot.
func (lib *library) writer(ctx context.Context) error {
	if lib.key != "" && lib.question == nil && !lib.unbound {
		return nil
	}

	if lib.key == "" && lib.question != nil {
		return fmt.Errorf("%w: %s", ErrUnbound, lib.question.Text(memoryBinary))
	}

	if lib.unbound {
		err := lib.locateStore(ctx, true)
		if err != nil {
			return err
		}
	}

	if lib.key == "" && lib.question != nil {
		return fmt.Errorf("%w: %s", ErrUnbound, lib.question.Text(memoryBinary))
	}

	return nil
}

// recorder is writer for a record, a take line or a session's file.
func (lib *library) recorder(ctx context.Context) error {
	return lib.writer(ctx)
}

// Join moves every tool folder of one store's library into another's, the
// folders being the libraries'. A session's state is the cache's, and stays
// with the store it was kept for. A tool name both hold refuses
// the join before anything moves; the target's index is rebuilt from its
// folders at its next read.
func Join(_ context.Context, from string, into string) error {
	listed, err := os.ReadDir(from)
	if err != nil {
		return fmt.Errorf("reading %s: %w", from, err)
	}

	var tools, shared []string

	for _, entry := range listed {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "sessions" || name == "copies" {
			continue
		}

		_, statErr := os.Lstat(filepath.Join(into, name))
		if statErr == nil {
			shared = append(shared, name)
		}

		tools = append(tools, name)
	}

	if len(shared) > 0 {
		slices.Sort(shared)

		return fmt.Errorf("both libraries hold the tools %s; merge them in one library first, nothing was joined",
			strings.Join(shared, ", "))
	}

	for _, name := range tools {
		err = os.Rename(filepath.Join(from, name), filepath.Join(into, name))
		if err != nil {
			return fmt.Errorf("moving the tool %s: %w", name, err)
		}
	}

	err = os.Remove(filepath.Join(into, "index.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("dropping the index to rebuild: %w", err)
	}

	return nil
}
