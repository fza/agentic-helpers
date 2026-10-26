package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// migrationPass reads or rewrites every event of a store being migrated, before
// the migrated log is built. A pass failing undoes the migration.
type migrationPass func(events []memorylog.Event) error

// kindPasses are the passes the move to entry kinds runs over the whole log,
// in order: the one point every event of a store is read once.
func (mem *memory) kindPasses() []migrationPass {
	return []migrationPass{assignKinds}
}

// migrateFormat3 gives every entry of a format-3 store a kind. The log is
// copied to backup/format-3-<UTC stamp>/, rebuilt beside the old one through
// kindPasses and swapped in; format.json is written last, and every step
// before it is undone on failure.
func (mem *memory) migrateFormat3(ctx context.Context) error {
	backup := filepath.Join(mem.store(), "backup", "format-3-"+mem.now().Format("20060102T150405.000000000Z"))

	err := copyTree(mem.logDir(), filepath.Join(backup, "log"))
	if err != nil {
		return fmt.Errorf("backing up the store's log: %w", err)
	}

	_, err = os.Stat(mem.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return writeFormat(mem.store(), storeFormat)
	}

	events, err := mem.allEvents(ctx)
	if err != nil {
		return err
	}

	original := make([][]byte, len(events))
	for index, event := range events {
		original[index] = event.Payload
	}

	for _, pass := range mem.kindPasses() {
		err = pass(events)
		if err != nil {
			return fmt.Errorf("migrating the store failed: %w; the store is unchanged and the next call tries again", err)
		}
	}

	changed := false
	for index, event := range events {
		changed = changed || !bytes.Equal(event.Payload, original[index])
	}

	// A log no pass changed stays as it is, byte for byte.
	if !changed {
		return writeFormat(mem.store(), storeFormat)
	}

	undo := &rollback{}

	err = mem.rebuildLog(ctx, events, undo)
	if err != nil {
		undo.run()

		return fmt.Errorf("migrating the store failed: %w; the store is unchanged and the next call tries again", err)
	}

	return nil
}

func (mem *memory) rebuildLog(ctx context.Context, events []memorylog.Event, undo *rollback) error {
	building := filepath.Join(mem.store(), "log.migrating")

	err := os.RemoveAll(building)
	if err != nil {
		return fmt.Errorf("clearing an earlier migration's log: %w", err)
	}

	undo.add(func() { _ = os.RemoveAll(building) })

	err = memorylog.Build(ctx, building, events)
	if err != nil {
		return fmt.Errorf("writing the migrated log: %w", err)
	}

	err = mem.swapLog(building, "log.format-3", undo)
	if err != nil {
		return err
	}

	err = writeFormat(mem.store(), storeFormat)
	if err != nil {
		return err
	}

	_ = os.RemoveAll(filepath.Join(mem.store(), "log.format-3"))

	return nil
}

// assignKinds turns every entry whose decay in force is `standing` into a
// rule, keeping its source, and every other into a note keeping the retention
// it was filed with as a retain override, so no entry moves in any brief. The
// decay in force is the filed retention as later retain events left it.
func assignKinds(events []memorylog.Event) error {
	levels := map[string]string{}

	for _, event := range events {
		switch event.Kind {
		case memorylog.KindNew:
			var filed struct {
				Retention string `json:"retention"`
			}

			err := json.Unmarshal(event.Payload, &filed)
			if err != nil {
				return fmt.Errorf("reading the new event of %s: %w", event.Entry, err)
			}

			levels[event.Entry] = filed.Retention
		case memorylog.KindRetain:
			var retained retainPayload

			err := json.Unmarshal(event.Payload, &retained)
			if err != nil {
				return fmt.Errorf("reading a retain event of %s: %w", event.Entry, err)
			}

			levels[event.Entry] = retained.Level
		}
	}

	for index, event := range events {
		if event.Kind != memorylog.KindNew {
			continue
		}

		var fields map[string]json.RawMessage

		err := json.Unmarshal(event.Payload, &fields)
		if err != nil {
			return fmt.Errorf("reading the new event of %s: %w", event.Entry, err)
		}

		if _, kinded := fields["kind"]; kinded {
			continue
		}

		kind := kindNote
		if levels[event.Entry] == "standing" {
			kind = kindRule

			delete(fields, "retention")
		}

		fields["kind"], err = json.Marshal(kind)
		if err != nil {
			return fmt.Errorf("encoding the kind of %s: %w", event.Entry, err)
		}

		events[index].Payload, err = json.Marshal(fields)
		if err != nil {
			return fmt.Errorf("encoding the new event of %s: %w", event.Entry, err)
		}
	}

	return nil
}
