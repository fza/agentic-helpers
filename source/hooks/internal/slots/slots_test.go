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

	dir := t.TempDir()

	return slots.Open(dir, filepath.Join(dir, "sessions.slots"))
}

func openFixed(t *testing.T) *slots.File {
	t.Helper()

	dir := t.TempDir()

	return slots.OpenFixed(dir, filepath.Join(dir, "sessions.slots"), width)
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
	file := openFixed(t)

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
			own := slots.Open(file.Root, file.Path)

			for write := range writes {
				// Values grow past the directory's twin and through the
				// classes, so each moves while the others are written.
				value := bytes.Repeat(fmt.Appendf(nil, "%02d-%03d;", session, write), 20+write*30)

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
		want := string(bytes.Repeat(fmt.Appendf(nil, "%02d-%03d;", session, writes-1), 20+(writes-1)*30))

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
			own := slots.Open(file.Root, file.Path)

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

func TestPuttingTheValueAKeyHoldsWritesNothing(t *testing.T) {
	file := openFile(t)
	put(t, file, "ladder/session", `{"nudges":1}`)

	before := generation(t, file, "ladder/session")

	put(t, file, "ladder/session", `{"nudges":1}`)

	if generation(t, file, "ladder/session") != before {
		t.Error("putting back the value a key holds should write nothing")
	}

	put(t, file, "ladder/session", `{"nudges":2}`)

	if value, _ := get(t, file, "ladder/session"); value != `{"nudges":2}` || generation(t, file, "ladder/session") != before+1 {
		t.Errorf("a changed value should still be written, got %q", value)
	}

	dayOn := time.Now().Add(25 * time.Hour)
	file.Now = func() time.Time { return dayOn }

	put(t, file, "ladder/session", `{"nudges":2}`)

	if generation(t, file, "ladder/session") != before+2 {
		t.Error("putting back a value written over a day ago should write it again, so readers dropping keys by age keep it")
	}

	put(t, file, "ladder/empty", "")

	if _, found := get(t, file, "ladder/empty"); !found {
		t.Error("an empty value for a new key should still be written")
	}

	failed := errors.New("no value")

	err := file.Update(context.Background(), "ladder/empty", func([]byte, bool) ([]byte, error) { return nil, failed })
	if !errors.Is(err, failed) {
		t.Errorf("a change that fails should answer its error, even where nothing would be written, got %v", err)
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

func TestAReclaimLeavingFewKeysShrinksTheFile(t *testing.T) {
	cases := []struct {
		name   string
		kept   int
		pairs  int
		shrunk bool
	}{
		{name: "a quarter of the pairs left", kept: 32, pairs: 64, shrunk: true},
		{name: "two keys left", kept: 2, pairs: 8, shrunk: true},
		{name: "more than a quarter left", kept: 33, shrunk: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := openFixed(t)

			for key := range 64 {
				put(t, file, fmt.Sprintf("session/%02d", key), fmt.Sprintf("value %d", key))
			}

			grown := header(t, file)
			if grown.Count != 1+2*128 {
				t.Fatalf("64 keys should grow the file to 128 pairs, got %d slots", grown.Count)
			}

			before, err := os.Stat(file.Path)
			if err != nil {
				t.Fatalf("measuring the slot file: %v", err)
			}

			err = file.Reclaim(context.Background(), func(slot slots.Slot) bool {
				var key int

				_, _ = fmt.Sscanf(slot.Key, "session/%d", &key)

				return key >= tc.kept
			})
			if err != nil {
				t.Fatalf("reclaiming: %v", err)
			}

			read := header(t, file)
			if (read.Count < grown.Count) != tc.shrunk || (tc.shrunk && read.Count != 1+2*tc.pairs) {
				t.Errorf("a file left holding a quarter of its pairs or fewer should shrink to twice its keys, "+
					"one holding more should keep its size; got %d slots from %d", read.Count, grown.Count)
			}

			if size(t, file) != int64(read.Count*read.Width) || read.Width != grown.Width {
				t.Error("a shrunk file should keep its width and match its header")
			}

			after, err := os.Stat(file.Path)
			if err != nil {
				t.Fatalf("measuring the slot file: %v", err)
			}

			if os.SameFile(before, after) == tc.shrunk {
				t.Error("only a file that shrinks should be written anew; one keeping its size stays the file it was")
			}

			for key := range 64 {
				value, found := get(t, file, fmt.Sprintf("session/%02d", key))
				if found != (key < tc.kept) || (found && value != fmt.Sprintf("value %d", key)) {
					t.Errorf("a reclaim should keep exactly the kept keys and their values, wrong at %d", key)
				}
			}
		})
	}
}

func TestAnEmptyFileReclaimedKeepsItsFirstSize(t *testing.T) {
	file := openFile(t)
	put(t, file, "session/a", "held")

	before := size(t, file)

	err := file.Reclaim(context.Background(), func(slots.Slot) bool { return true })
	if err != nil {
		t.Fatalf("reclaiming: %v", err)
	}

	if size(t, file) != before {
		t.Error("a file at its first size should not be written anew")
	}
}

// TestAWriteOpenRefusedNamesTheFileOnce takes a permission from a file of a
// store: the refused write names the file it could not open, once.
func TestAWriteOpenRefusedNamesTheFileOnce(t *testing.T) {
	putChanged := func(file *slots.File, value string) error {
		return file.Put(context.Background(), "shown/one", []byte(value+"changed"))
	}

	cases := []struct {
		name    string
		open    func(t *testing.T) *slots.File
		value   string
		refused string
		mode    os.FileMode
		write   func(file *slots.File, value string) error
	}{
		{name: "a fixed-width file", open: openFixed, value: "small", refused: "sessions.slots", mode: 0o400, write: putChanged},
		{name: "a directory", open: openFile, value: "small", refused: "sessions.slots", mode: 0o400, write: putChanged},
		{name: "a class file", open: openFile, value: strings.Repeat("x", 1_000), refused: "sessions-*.slots", mode: 0o400, write: putChanged},
		{name: "a fixed-width file laid out", open: openFixed, value: "small", refused: "sessions.slots", mode: 0o200,
			write: func(file *slots.File, _ string) error {
				return slots.Open(filepath.Dir(file.Path), file.Path).Relayout(context.Background(), nil)
			}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := tc.open(t)
			put(t, file, "shown/one", tc.value)

			paths, err := filepath.Glob(filepath.Join(filepath.Dir(file.Path), tc.refused))
			if err != nil {
				t.Fatal(err)
			}

			for _, path := range paths {
				err = os.Chmod(path, tc.mode)
				if err != nil {
					t.Fatal(err)
				}
			}

			err = tc.write(file, tc.value)
			if err == nil {
				t.Fatal("a write the store's permissions refuse should fail")
			}

			named := 0

			for _, path := range paths {
				if strings.Count(err.Error(), path) > 1 {
					t.Errorf("a refused write should name the file it could not open once, got %v", err)
				}

				named += strings.Count(err.Error(), path)
			}

			if named == 0 {
				t.Errorf("a refused write should name the file it could not open, got %v", err)
			}
		})
	}
}

func TestANonSlotFileIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.slots")

	err := os.WriteFile(path, []byte(`{"ids":[]}`), 0o600)
	if err != nil {
		t.Fatalf("writing: %v", err)
	}

	_, _, err = slots.Open(filepath.Dir(path), path).Get(context.Background(), "shown/one")
	if !errors.Is(err, slots.ErrNotSlotFile) {
		t.Errorf("a file of another kind should not read as slots, got %v", err)
	}
}
