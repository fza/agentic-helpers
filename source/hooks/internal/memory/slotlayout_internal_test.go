package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/formatsteps"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// TestEveryStoreKeepsItsSessionSlotsThroughTheLayoutStep brings every
// committed release's store, and the hand-laid one, to the format before the
// layout step, then migrates it: every key of the session slot file keeps its
// value, generation and write time, and the file is a slot directory.
func TestEveryStoreKeepsItsSessionSlotsThroughTheLayoutStep(t *testing.T) {
	ctx := context.Background()

	for _, start := range append(killStarts, releaseStarts(t)...) {
		// A release from the layout step on wrote its store past it, so the
		// step never runs on that store.
		laid := cutMemory(t.TempDir())
		start.lay(t, laid)

		version, err := formatsteps.Read(laid.storeComponent().Dir)
		if err != nil {
			t.Fatal(err)
		}

		if version >= layoutFormat {
			continue
		}

		t.Run(start.name, func(t *testing.T) {
			root := t.TempDir()
			start.lay(t, cutMemory(root))

			mem := cutMemory(root)
			component := mem.storeComponent()
			component.Steps = slices.DeleteFunc(mem.formatSteps(), func(step formatsteps.Step) bool { return step.To > namesFormat })

			err := formatsteps.Run(ctx, component)
			if err != nil {
				t.Fatalf("bringing the store to the layout step: %v", err)
			}

			before, err := mem.slotFile().All(ctx)
			if err != nil || len(before) == 0 {
				t.Fatalf("the store should hold session slots before the step, got %d %v", len(before), err)
			}

			// A release wrote one fixed-width file; the hand-laid store takes
			// its session files in after the layout this release writes.
			if format := slotFormat(t, mem.slotFile().Path); strings.HasPrefix(start.name, "v") && format != 1 {
				t.Fatalf("a release's session slots should be a fixed-width file, format %d", format)
			}

			err = cutMemory(root).migrate(ctx)
			if err != nil {
				t.Fatalf("migrating: %v", err)
			}

			after, err := mem.slotFile().All(ctx)
			if err != nil {
				t.Fatal(err)
			}

			held := map[string]slots.Slot{}
			for _, slot := range after {
				held[slot.Key] = slot
			}

			for _, slot := range before {
				got, found := held[slot.Key]
				if !found || !bytes.Equal(got.Value, slot.Value) || got.Generation != slot.Generation || !got.Written.Equal(slot.Written) {
					t.Errorf("%s should keep its value, generation and time through the step", slot.Key)
				}
			}

			if len(after) != len(before) {
				t.Errorf("the step should keep every key and add none, %d from %d", len(after), len(before))
			}

			if format := slotFormat(t, mem.slotFile().Path); format != 2 {
				t.Errorf("the session slots should be a slot directory, format %d", format)
			}
		})
	}
}

// TestTheLayoutStepFitsAValuePastItsCeiling lays out a fixed-width file an
// earlier release wrote with values past their kinds' ceilings: each is
// fitted, a table of names keeps a part's names, and a value within its
// ceiling stays as it was.
func TestTheLayoutStepFitsAValuePastItsCeiling(t *testing.T) {
	ctx := context.Background()
	mem := cutMemory(t.TempDir())
	file := mem.slotFile()
	fixed := slots.OpenFixed(file.Root, file.Path, 128<<10)

	var shown shownSet
	for index := range 4_000 {
		shown.IDs = append(shown.IDs, fmt.Sprintf("m-%06d", index))
	}

	oversized, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}

	var names strings.Builder
	for index := range namesPerPart + 500 {
		fmt.Fprintf(&names, "n%05d\n", index)
	}

	for key, value := range map[string][]byte{
		"shown/s1": oversized, "graph-names/2026/11": []byte(names.String()), "ladder/s1": []byte(`{"nudges":1}`),
	} {
		err = fixed.Put(ctx, key, value)
		if err != nil {
			t.Fatal(err)
		}
	}

	listed, err := mem.layoutSteps(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, operation := range listed {
		err = operation.Apply(ctx)
		if err != nil {
			t.Fatalf("%s: %v", operation.Name, err)
		}
	}

	var fitted shownSet
	if !file.GetJSON(ctx, "shown/s1", &fitted) || len(fitted.IDs) == 0 || slots.EncodedLen(fitted) > shownCeiling ||
		fitted.IDs[len(fitted.IDs)-1] != shown.IDs[len(shown.IDs)-1] {
		t.Errorf("a shown set past its ceiling should keep its newest ids within it, kept %d", len(fitted.IDs))
	}

	table, _, _ := file.Get(ctx, "graph-names/2026/11")
	if strings.Count(string(table), "\n") != namesPerPart || !strings.HasPrefix(string(table), "n00000\n") {
		t.Errorf("a table of names should keep the names of its first part, kept %d", strings.Count(string(table), "\n"))
	}

	if ladder, _, _ := file.Get(ctx, "ladder/s1"); string(ladder) != `{"nudges":1}` {
		t.Errorf("a value within its ceiling should stay as it was, got %s", ladder)
	}
}

func slotFormat(t *testing.T, path string) int {
	t.Helper()

	opened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = opened.Close() }()

	header, _, err := slots.Read(opened)
	if err != nil {
		t.Fatalf("reading the session slots: %v", err)
	}

	return header.Format
}
