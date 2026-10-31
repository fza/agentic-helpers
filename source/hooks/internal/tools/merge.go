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

	return done(fmt.Sprintf("merged %d into %s, now current", len(members), survivor.Name)).
		with(mergedFields{Survivor: survivor.Name, Members: members})
}

// fold moves a member's take lines to the survivor, removes the member's
// folder and leaves a tombstone naming the survivor. The caller holds the lock.
func (lib *library) fold(member string, survivor string) error {
	store, err := lib.ownStore()
	if err != nil {
		return err
	}

	defer store.Close()

	folder, err := store.Sub(member)
	if err != nil {
		return err
	}

	takes, err := readJournal[takeLine](folder, takesJournal)
	folder.Close()

	if err != nil {
		return fmt.Errorf("reading %s's takes: %w", member, err)
	}

	if len(takes) > 0 {
		kept, err := store.Sub(survivor)
		if err != nil {
			return err
		}

		err = appendJournal(kept, takesJournal, takes...)
		kept.Close()

		if err != nil {
			return err
		}
	}

	err = store.RemoveAll(member)
	if err != nil {
		return fmt.Errorf("removing %s: %w", member, err)
	}

	tombstone, err := store.EnsureSub(member, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s's tombstone: %w", member, err)
	}

	defer tombstone.Close()

	err = tombstone.Create("merged", []byte(survivor+"\n"), 0o600)
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
		rebuilt, err := buildIndex(lib.ownDir())
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

	// No embedder set is no failure: the tools stay ranked by their words.
	embedder, err := lib.embedder(ctx)
	if err == nil {
		_, err = lib.fillVectors(ctx, embedder, indexTexts(built))
		if err != nil {
			return refused("%s; embedding the descriptions failed: %v", text, err)
		}
	}

	return done(text).with(reindexedFields{Tools: len(built.Tools), Skipped: built.Skipped})
}
