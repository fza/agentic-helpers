package slots_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

const width = 4096

func openFile(t *testing.T) *slots.File {
	t.Helper()

	return slots.Open(filepath.Join(t.TempDir(), "sessions.slots"), width)
}

func put(t *testing.T, file *slots.File, key string, value string) {
	t.Helper()

	err := file.Put(context.Background(), key, []byte(value))
	if err != nil {
		t.Fatalf("putting %s: %v", key, err)
	}
}

func get(t *testing.T, file *slots.File, key string) (string, bool) {
	t.Helper()

	value, found, err := file.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("getting %s: %v", key, err)
	}

	return string(value), found
}

func header(t *testing.T, file *slots.File) slots.Header {
	t.Helper()

	opened, err := os.Open(file.Path)
	if err != nil {
		t.Fatalf("opening the slot file: %v", err)
	}

	defer func() { _ = opened.Close() }()

	read, _, err := slots.Read(opened)
	if err != nil {
		t.Fatalf("reading the slot file: %v", err)
	}

	return read
}

func size(t *testing.T, file *slots.File) int64 {
	t.Helper()

	info, err := os.Stat(file.Path)
	if err != nil {
		t.Fatalf("measuring the slot file: %v", err)
	}

	return info.Size()
}

func TestASlotReadsBackItsNewestValue(t *testing.T) {
	file := openFile(t)

	_, found := get(t, file, "shown/one")
	if found {
		t.Fatal("a file not yet written should hold no key")
	}

	put(t, file, "shown/one", "first")
	put(t, file, "shown/two", "other")
	put(t, file, "shown/one", "second")
	put(t, file, "shown/one", "third")

	value, found := get(t, file, "shown/one")
	if !found || value != "third" {
		t.Errorf("a key should read back its newest value, got %q", value)
	}

	value, _ = get(t, file, "shown/two")
	if value != "other" {
		t.Errorf("writing one key should leave another's value as it was, got %q", value)
	}

	if size(t, file) != int64(header(t, file).Count*header(t, file).Width) {
		t.Error("the file should be exactly its header's count of slots of its width")
	}
}

func TestAValuePastTheSlotWidthIsRefused(t *testing.T) {
	file := openFile(t)

	err := file.Put(context.Background(), "big", bytes.Repeat([]byte("x"), width))
	if !errors.Is(err, slots.ErrTooLarge) {
		t.Errorf("a value wider than a slot should be refused, got %v", err)
	}

	err = file.Put(context.Background(), "", []byte("x"))
	if !errors.Is(err, slots.ErrBadKey) {
		t.Errorf("an empty key should be refused, got %v", err)
	}
}

func TestTwoSessionsWritingConcurrentlyKeepTheirOwnSlots(t *testing.T) {
	file := openFile(t)

	const (
		sessions = 12
		writes   = 40
	)

	var group sync.WaitGroup

	for session := range sessions {
		group.Go(func() {
			own := slots.Open(file.Path, width)

			for write := range writes {
				value := bytes.Repeat(fmt.Appendf(nil, "%02d-%03d;", session, write), 20)

				err := own.Put(context.Background(), fmt.Sprintf("shown/%02d", session), value)
				if err != nil {
					t.Errorf("writing: %v", err)

					return
				}

				read, found, err := own.Get(context.Background(), fmt.Sprintf("shown/%02d", session))
				if err != nil || !found || !bytes.Equal(read, value) {
					t.Errorf("a session should read back its own last write while others write theirs")

					return
				}
			}
		})
	}

	group.Wait()

	for session := range sessions {
		value, found := get(t, file, fmt.Sprintf("shown/%02d", session))
		want := string(bytes.Repeat(fmt.Appendf(nil, "%02d-%03d;", session, writes-1), 20))

		if !found || value != want {
			t.Errorf("session %d's slot should hold its own last write", session)
		}
	}

	if size(t, file) != int64(header(t, file).Count*header(t, file).Width) {
		t.Error("the file should be exactly its header's count of slots of its width")
	}
}

func TestWritersUpdatingOneSlotInTurnKeepEveryChange(t *testing.T) {
	file := openFile(t)

	const (
		writers = 8
		appends = 20
	)

	var group sync.WaitGroup

	for writer := range writers {
		group.Go(func() {
			own := slots.Open(file.Path, width)

			for added := range appends {
				err := own.Update(context.Background(), "graph-names/2026/10", func(value []byte, _ bool) ([]byte, error) {
					return fmt.Appendf(value, "%d-%d\n", writer, added), nil
				})
				if err != nil {
					t.Errorf("updating: %v", err)

					return
				}
			}
		})
	}

	group.Wait()

	value, _ := get(t, file, "graph-names/2026/10")
	if strings.Count(value, "\n") != writers*appends {
		t.Errorf("no writer's change should be lost to another's, got %d of them", strings.Count(value, "\n"))
	}
}

func TestAnUnchangedUpdateWritesNothing(t *testing.T) {
	file := openFile(t)
	put(t, file, "graph-names/2026/10", "a\n")

	before := generation(t, file, "graph-names/2026/10")

	err := file.Update(context.Background(), "graph-names/2026/10", func([]byte, bool) ([]byte, error) {
		return nil, slots.ErrUnchanged
	})
	if err != nil {
		t.Fatalf("updating: %v", err)
	}

	value, _ := get(t, file, "graph-names/2026/10")
	if value != "a\n" || generation(t, file, "graph-names/2026/10") != before {
		t.Errorf("an update answering unchanged should leave the slot as it was, got %q", value)
	}
}

func generation(t *testing.T, file *slots.File, key string) uint64 {
	t.Helper()

	held, err := file.All(context.Background())
	if err != nil {
		t.Fatalf("listing the slots: %v", err)
	}

	for _, slot := range held {
		if slot.Key == key {
			return slot.Generation
		}
	}

	return 0
}

func TestAReclaimedSlotIsReused(t *testing.T) {
	file := openFile(t)

	for key := range 8 {
		put(t, file, fmt.Sprintf("session/%d", key), "held")
	}

	before := size(t, file)

	for round := range 50 {
		err := file.Delete(context.Background(), fmt.Sprintf("session/%d", round%8))
		if err != nil {
			t.Fatalf("clearing: %v", err)
		}

		put(t, file, fmt.Sprintf("next/%d", round), "held")

		err = file.Delete(context.Background(), fmt.Sprintf("next/%d", round))
		if err != nil {
			t.Fatalf("clearing: %v", err)
		}

		put(t, file, fmt.Sprintf("session/%d", round%8), "back")
	}

	if size(t, file) != before {
		t.Error("keys written into reclaimed slots should never grow the file")
	}

	_, found := get(t, file, "next/3")
	if found {
		t.Error("a cleared key should read as absent")
	}

	value, found := get(t, file, "session/5")
	if !found || value != "back" {
		t.Errorf("a key written into a reclaimed slot should read back, got %q", value)
	}
}

func TestAFullFileGrowsWithinItsHeader(t *testing.T) {
	file := openFile(t)
	first := int64(0)

	for key := range 40 {
		put(t, file, fmt.Sprintf("session/%02d", key), fmt.Sprintf("value %d", key))

		if key == 0 {
			first = size(t, file)
		}

		read := header(t, file)
		if size(t, file) != int64(read.Count*read.Width) {
			t.Fatal("the file should never pass its header's count of slots of its width")
		}
	}

	if size(t, file) <= first {
		t.Error("a file out of free slots should grow")
	}

	for key := range 40 {
		value, found := get(t, file, fmt.Sprintf("session/%02d", key))
		if !found || value != fmt.Sprintf("value %d", key) {
			t.Errorf("growing should keep every key's value, lost %d", key)
		}
	}
}

func TestReclaimClearsTheKeysItIsAskedTo(t *testing.T) {
	file := openFile(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	file.Now = func() time.Time { return now }

	put(t, file, "shown/old", "a")

	now = now.Add(48 * time.Hour)
	put(t, file, "shown/new", "b")

	cutoff := now.Add(-24 * time.Hour)

	err := file.Reclaim(context.Background(), func(slot slots.Slot) bool { return slot.Written.Before(cutoff) })
	if err != nil {
		t.Fatalf("reclaiming: %v", err)
	}

	_, oldFound := get(t, file, "shown/old")
	_, newFound := get(t, file, "shown/new")

	if oldFound || !newFound {
		t.Error("a reclaim should clear the slots its test names and keep the rest")
	}
}

func TestANonSlotFileIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.slots")

	err := os.WriteFile(path, []byte(`{"ids":[]}`), 0o600)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	_, _, err = slots.Open(path, width).Get(context.Background(), "shown/one")
	if !errors.Is(err, slots.ErrNotSlotFile) {
		t.Errorf("a file of another kind should not read as slots, got %v", err)
	}
}
