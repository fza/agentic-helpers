package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

// migrationPass reads or rewrites every event of a store being migrated, before
// the migrated log is built.
type migrationPass func(events []memorylog.Event) error

// kindPasses are the passes the move to entry kinds runs over the whole log,
// in order: the one point every event of a store is read once, so the one
// scan of every body still held for credentials runs here too.
func (mem *memory) kindPasses(scan *credentialScan) []migrationPass {
	return []migrationPass{assignKinds, scan.pass}
}

// kindSteps move a format-3 store to format 4, every entry filed with a kind:
// the log is copied to backup/format-3-<UTC stamp>/, what the credential pass
// found is kept, and a log the passes changed is rebuilt beside the old one
// and swapped in. A log no pass changed stays as it is, byte for byte, and is
// never set aside. Both passes give the same answer on a log they already ran
// over, so a run carrying the step on reads whichever log the store holds.
func (mem *memory) kindSteps(ctx context.Context) ([]formatsteps.Operation, error) {
	source := mem.liveLog("log.format-3")
	if source == "" {
		return nil, nil
	}

	events, err := mem.eventsIn(ctx, source)
	if err != nil {
		return nil, err
	}

	original := make([][]byte, len(events))
	for index, event := range events {
		original[index] = event.Payload
	}

	scan := &credentialScan{ctx: ctx}

	for _, pass := range mem.kindPasses(scan) {
		err = pass(events)
		if err != nil {
			return nil, err
		}
	}

	changed := false
	for index, event := range events {
		changed = changed || !bytes.Equal(event.Payload, original[index])
	}

	operations := []formatsteps.Operation{
		mem.backupStep(3, "log"),
		{Name: "keeping what the credential scan found", Apply: func(context.Context) error {
			return scan.save(mem.env.Roots.Data, mem.store())
		}},
	}

	// A run killed after the migrated log took its place finds nothing left to
	// change, and only the set-aside log to remove.
	if !changed {
		return append(operations, mem.removeSetAside("log.format-3")), nil
	}

	return append(operations, mem.rebuildSteps(events, "log.format-3")...), nil
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
