package vectorseg_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/vectorseg"
)

func writeStamps(t *testing.T, dir string, held map[string]time.Time) {
	t.Helper()

	encoded := map[string]string{}
	for format, at := range held {
		encoded[format] = at.UTC().Format(time.RFC3339)
	}

	data, err := json.Marshal(encoded)
	if err != nil {
		t.Fatalf("encoding the stamps: %v", err)
	}

	err = os.WriteFile(filepath.Join(dir, "used.json"), data, 0o600)
	if err != nil {
		t.Fatalf("writing the stamps: %v", err)
	}
}

func readStamp(t *testing.T, dir string) time.Time {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, "used.json"))
	if err != nil {
		t.Fatalf("reading the stamps: %v", err)
	}

	var held map[string]string

	err = json.Unmarshal(data, &held)
	if err != nil {
		t.Fatalf("decoding the stamps: %v", err)
	}

	stamped, err := time.Parse(time.RFC3339, held[vectorseg.Format])
	if err != nil {
		t.Fatalf("the stamp of this format should read as a time: %v", err)
	}

	return stamped
}

func filledStore(t *testing.T) *vectorseg.Store {
	t.Helper()

	store := openStore(t)

	err := store.Write([]vectorseg.Row{{ID: "a", Hash: "h", Chunks: [][]float32{unit(4, 0)}}}, everything)
	if err != nil {
		t.Fatalf("writing a row: %v", err)
	}

	return store
}

func TestAWriteStampsTheStoreWithItsFormat(t *testing.T) {
	store := filledStore(t)

	stamped := readStamp(t, store.Dir())
	if time.Since(stamped) > time.Minute {
		t.Error("a write should stamp the store as used now")
	}
}

func TestAReadRefreshesTheStampAtMostOnceADay(t *testing.T) {
	cases := []struct {
		name    string
		age     time.Duration
		refresh bool
	}{
		{name: "a stamp from an hour ago stays", age: time.Hour, refresh: false},
		{name: "a stamp from two days ago is refreshed", age: 48 * time.Hour, refresh: true},
		{name: "a stamp from the future is refreshed", age: -48 * time.Hour, refresh: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := filledStore(t)
			before := time.Now().Add(-tc.age).Truncate(time.Second)
			writeStamps(t, store.Dir(), map[string]time.Time{vectorseg.Format: before})

			snapshot, err := store.Snapshot()
			if err != nil {
				t.Fatalf("reading the store: %v", err)
			}

			_ = snapshot.Close()

			refreshed := !readStamp(t, store.Dir()).Equal(before)
			if refreshed != tc.refresh {
				t.Errorf("a read should refresh the stamp only where it is a day old or more, or ahead of now")
			}
		})
	}
}

func TestAReadOfAStoreNotYetFilledCreatesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "absent")

	snapshot, err := vectorseg.Open(root, dir).Snapshot()
	if err != nil {
		t.Fatalf("reading an absent store: %v", err)
	}

	_ = snapshot.Close()

	_, err = os.Stat(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Error("a read should leave an absent store absent")
	}
}

func TestLastUse(t *testing.T) {
	old := time.Now().Add(-400 * 24 * time.Hour).Truncate(time.Second)
	recent := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	cases := []struct {
		name   string
		stamps map[string]time.Time
		files  time.Time
		want   time.Time
	}{
		{name: "the newest format's stamp counts", stamps: map[string]time.Time{vectorseg.Format: old, "vectorseg/0": recent}, files: old, want: recent},
		{name: "a write newer than every stamp counts", stamps: map[string]time.Time{vectorseg.Format: old}, files: recent, want: recent},
		{name: "a store without a stamp is judged by its last write", files: old, want: old},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := filledStore(t)
			_ = os.Remove(filepath.Join(store.Dir(), "used.json"))

			if tc.stamps != nil {
				writeStamps(t, store.Dir(), tc.stamps)
			}

			listed, err := os.ReadDir(store.Dir())
			if err != nil {
				t.Fatalf("listing the store: %v", err)
			}

			for _, entry := range listed {
				err = os.Chtimes(filepath.Join(store.Dir(), entry.Name()), tc.files, tc.files)
				if err != nil {
					t.Fatalf("aging the store's files: %v", err)
				}
			}

			last, known := store.LastUse()
			if !known || !last.Equal(tc.want) {
				t.Errorf("the last use should be the newest of the stamps and the files' times")
			}
		})
	}
}

func TestTryHoldRefusesAStoreBeingFilled(t *testing.T) {
	store := filledStore(t)

	release, taken, err := store.TryFill()
	if err != nil || !taken {
		t.Fatalf("taking the fill lock: %v", err)
	}

	_, err = store.TryHold()
	if !errors.Is(err, vectorseg.ErrHeld) {
		t.Error("a hold should be refused while another holder fills the store")
	}

	release()

	unhold, err := store.TryHold()
	if err != nil {
		t.Fatalf("a hold should be taken once the fill ends: %v", err)
	}

	unhold()
}
