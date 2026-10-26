package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func (lib *library) merge(ctx context.Context, req request) result {
	if strings.TrimSpace(req.Why) == "" || clean(req.Why, false) != req.Why {
		return refused("--why is required: one line saying why these tools are one")
	}

	own := lib.own(ctx)

	survivor, present := own.tool(req.Survivor)
	if !present {
		return refused("no tool %s in this project; merge folds tools of the own project only", req.Survivor)
	}

	var failures []string

	for _, member := range req.Members {
		_, held := own.tool(member)

		switch {
		case member == req.Survivor:
			failures = append(failures, member+" is the survivor")
		case !held:
			failures = append(failures, "no tool "+member+" in this project")
		}
	}

	if len(failures) > 0 {
		return refused("nothing merged:\n  %s", strings.Join(failures, "\n  "))
	}

	from, err := lib.readSource(ctx, req.From)
	if err != nil {
		return refused("%v", err)
	}

	next := survivor.spec()

	credential := credentialIn(ctx, from.files, append(describedFields(next), labelled{label: "--why", text: req.Why}))
	if credential != "" {
		return refused("%s", credential)
	}

	vector, _ := lib.embedOne(ctx, next)
	members := slices.Compact(slices.Clone(req.Members))

	err = lib.locked(ctx, func() error {
		err := lib.writeVersion(ctx, version{name: survivor.Name, number: survivor.Version + 1, files: from.files,
			spec: next, why: req.Why, vector: vector})
		if err != nil {
			return err
		}

		for _, member := range members {
			err = lib.fold(member, survivor.Name)
			if err != nil {
				return err
			}
		}

		return lib.reindexTool(survivor.Name, vector)
	})
	if err != nil {
		return refused("%v", err)
	}

	lib.forget()

	return done(fmt.Sprintf("merged %d into %s, now current", len(members), survivor.Name))
}

// fold moves a member's take lines to the survivor, removes the member's
// folder and leaves a tombstone naming the survivor. The caller holds the lock.
func (lib *library) fold(member string, survivor string) error {
	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.close()

	folder, err := store.sub(member)
	if err != nil {
		return err
	}

	takes, err := folder.readOptional("takes.jsonl", lineFileLimit)
	folder.close()

	if err != nil {
		return fmt.Errorf("reading %s's takes: %w", member, err)
	}

	if len(takes) > 0 {
		kept, err := store.sub(survivor)
		if err != nil {
			return err
		}

		lines, _ := jsonLines[takeLine](takes)
		for _, line := range lines {
			err = appendLine(kept, "takes.jsonl", line)
			if err != nil {
				kept.close()

				return err
			}
		}

		kept.close()
	}

	err = store.root.RemoveAll(member)
	if err != nil {
		return fmt.Errorf("removing %s: %w", member, err)
	}

	tombstone, err := store.ensureSub(member)
	if err != nil {
		return fmt.Errorf("creating %s's tombstone: %w", member, err)
	}

	defer tombstone.close()

	err = tombstone.create("merged", []byte(survivor+"\n"))
	if err != nil {
		return fmt.Errorf("writing %s's tombstone: %w", member, err)
	}

	held, found := loadIndex(lib.ownDir())
	if !found {
		return nil
	}

	delete(held.Tools, member)
	held.Merged[member] = survivor

	return lib.writeIndex(held)
}

func (lib *library) reindex(ctx context.Context, _ request) result {
	var built *index

	err := lib.locked(ctx, func() error {
		rebuilt, err := lib.rebuild(ctx, lib.ownDir())
		if err != nil {
			return err
		}

		built = rebuilt

		return lib.writeIndex(rebuilt)
	})
	if err != nil {
		return refused("%v", err)
	}

	lib.forget()

	text := fmt.Sprintf("rebuilt index.json: %d tools", len(built.Tools))
	if built.Skipped > 0 {
		text += fmt.Sprintf(", %d unreadable skipped", built.Skipped)
	}

	return done(text)
}
