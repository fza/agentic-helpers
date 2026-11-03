package slots_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

func reclaimStale(t *testing.T, file *slots.File) {
	t.Helper()

	err := file.ReclaimStale(context.Background())
	if err != nil {
		t.Fatalf("reclaiming the stale keys: %v", err)
	}
}

func TestAStaleReclaimClearsTheKeysUnwrittenForAWeek(t *testing.T) {
	file := openFile(t)
	started := time.Now()
	clock := started

	file.Now = func() time.Time { return clock }

	clock = started.Add(-slots.StaleAge)
	put(t, file, "session/a-week", "a")

	clock = started.Add(-slots.StaleAge + time.Minute)
	put(t, file, "session/within", "b")

	clock = started
	reclaimStale(t, file)

	_, weekFound := get(t, file, "session/a-week")
	_, withinFound := get(t, file, "session/within")

	if weekFound || !withinFound {
		t.Error("a stale reclaim should clear a key written a week ago or more and keep one written within the week")
	}
}

func TestAStaleReclaimRunsAtMostOnceADay(t *testing.T) {
	file := openFile(t)
	started := time.Now()
	clock := started

	file.Now = func() time.Time { return clock }

	put(t, file, "session/kept", "a")
	reclaimStale(t, file)

	clock = started.Add(-2 * slots.StaleAge)
	put(t, file, "session/stale", "b")

	clock = started.Add(23 * time.Hour)
	reclaimStale(t, file)

	if _, found := get(t, file, "session/stale"); !found {
		t.Error("a second reclaim within the day should leave the file alone")
	}

	clock = started.Add(25 * time.Hour)
	reclaimStale(t, file)

	if _, found := get(t, file, "session/stale"); found {
		t.Error("the first reclaim of the next day should clear the stale key")
	}
}

func TestAReclaimRecordDatedAfterNowCountsAsNoRun(t *testing.T) {
	file := openFile(t)
	started := time.Now()
	clock := started

	file.Now = func() time.Time { return clock }

	put(t, file, "session/kept", "a")
	reclaimStale(t, file)

	ahead := started.Add(48 * time.Hour)

	err := os.Chtimes(file.Path+".reclaimed", ahead, ahead)
	if err != nil {
		t.Fatalf("dating the record ahead: %v", err)
	}

	clock = started.Add(-2 * slots.StaleAge)
	put(t, file, "session/stale", "b")

	clock = started.Add(time.Hour)
	reclaimStale(t, file)

	if _, found := get(t, file, "session/stale"); found {
		t.Error("a record dated after now, as after a clock change, should not hold the reclaim off")
	}
}

func TestAStaleReclaimLeavingFewKeysShrinksTheFile(t *testing.T) {
	file := openFile(t)
	started := time.Now()
	clock := started.Add(-2 * slots.StaleAge)

	file.Now = func() time.Time { return clock }

	for key := range 64 {
		put(t, file, fmt.Sprintf("session/%02d", key), "stale")
	}

	clock = started
	put(t, file, "session/fresh", "kept")

	grown := header(t, file)

	reclaimStale(t, file)

	read := header(t, file)
	if read.Count >= grown.Count || read.Count != 1+2*8 || size(t, file) != int64(read.Count*read.Width) {
		t.Errorf("a file left a quarter full or less should shrink to the size its kept keys need, got %d slots from %d",
			read.Count, grown.Count)
	}

	if value, found := get(t, file, "session/fresh"); !found || value != "kept" {
		t.Error("the shrunk file should keep the fresh key and its value")
	}
}

func TestAStaleReclaimOfAMissingFileWritesNothing(t *testing.T) {
	file := openFile(t)

	reclaimStale(t, file)

	for _, path := range []string{file.Path, file.Path + ".reclaimed"} {
		_, err := os.Lstat(path)
		if err == nil {
			t.Errorf("a reclaim of a file never written should create nothing, found %s", path)
		}
	}
}
