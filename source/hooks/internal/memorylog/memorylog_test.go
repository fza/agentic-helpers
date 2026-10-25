package memorylog_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestRoundTrip(t *testing.T) {
	special := memorylog.Event{
		Kind:    memorylog.KindMerge,
		Time:    start.Add(-1),
		Seat:    "reviewer",
		Target:  "m-00002",
		Payload: nil,
	}
	unescaped := memorylog.Event{
		Kind:    memorylog.KindSupersede,
		Entry:   "m-00001",
		Time:    start,
		Seat:    "drive",
		Target:  "m-00003",
		Payload: []byte(`{"why":"<a> & <b>"}`),
	}

	cases := []struct {
		name   string
		events []memorylog.Event
		sealed bool
	}{
		{name: "tail only", events: append(events(0, 5, 10), special, unescaped)},
		{name: "sealed segments and tail", events: append(events(0, 700, 100), special, unescaped), sealed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := openLog(t, dir)

			for batch := range slices.Chunk(tc.events, 50) {
				appendEvents(t, log, batch...)
			}

			if (len(segmentFiles(t, dir)) > 0) != tc.sealed {
				t.Fatal("the log should seal exactly when its tail outgrows the seal size")
			}

			if !sameEvents(readEvents(t, log), tc.events) {
				t.Error("a snapshot should read back every appended event and payload, in order")
			}
		})
	}
}

func TestSealOnceTailOutgrowsSealBytes(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)

	var appended []memorylog.Event

	for number := 0; len(segmentFiles(t, dir)) == 0; number++ {
		if number > 200 {
			t.Fatal("the log should seal once its tail outgrows the seal size")
		}

		appended = append(appended, event(number, 1000))
		appendEvents(t, log, appended[len(appended)-1])

		info, err := os.Stat(filepath.Join(dir, "tail.jsonl"))
		if err != nil {
			t.Fatalf("measuring the tail: %v", err)
		}

		if info.Size() > memorylog.SealBytes {
			t.Fatal("no append should leave the tail past the seal size")
		}
	}

	segments := segmentFiles(t, dir)
	digest := sha256.Sum256(readFile(t, filepath.Join(dir, segments[0])))

	if len(segments) != 1 || segments[0] != hex.EncodeToString(digest[:4])+".seg" {
		t.Error("the seal should write one segment named by the hash of its bytes")
	}

	listed := readManifest(t, dir)
	if len(listed.Segments) != 1 || listed.Segments[0] != segments[0] || listed.Sealing != nil {
		t.Error("the manifest should list the sealed segment and no seal in progress")
	}

	if len(readFile(t, filepath.Join(dir, "tail.jsonl"))) != 0 {
		t.Error("the seal should leave the tail empty")
	}

	if !sameEvents(readEvents(t, log), appended) {
		t.Error("the sealed segment should hold every event the tail held")
	}
}

func TestMergeAtMaxSegments(t *testing.T) {
	dropping := func(rows []memorylog.Row, payload func(int) ([]byte, error)) ([]memorylog.Event, error) {
		var kept []memorylog.Event

		for index, row := range rows {
			if index == 1 {
				continue
			}

			body, err := payload(index)
			if err != nil {
				return nil, err
			}

			if index == 0 {
				body = nil
			}

			kept = append(kept, memorylog.Event{
				Kind: row.Kind, Entry: row.Entry, Time: row.Time, Seat: row.Seat, Target: row.Target, Payload: body,
			})
		}

		return kept, nil
	}

	appended := events(0, memorylog.MaxSegments, memorylog.SealBytes)
	trimmed := append([]memorylog.Event{appended[0]}, appended[2:]...)
	trimmed[0].Payload = nil

	cases := []struct {
		name   string
		policy memorylog.Policy
		want   []memorylog.Event
	}{
		{name: "policy drops a row and a payload", policy: dropping, want: trimmed},
		{name: "nil policy keeps every event", want: appended},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			log := openLog(t, dir)

			for index, next := range appended {
				err := log.AppendWith(context.Background(), tc.policy, next)
				if err != nil {
					t.Fatalf("appending: %v", err)
				}

				if index < len(appended)-1 && len(readManifest(t, dir).Segments) != index+1 {
					t.Fatal("each oversized append should seal one segment before the merge")
				}
			}

			listed := readManifest(t, dir)
			files := segmentFiles(t, dir)

			if len(listed.Segments) != 1 || len(files) != 1 || files[0] != listed.Segments[0] {
				t.Error("the merge should leave one listed segment and remove the others")
			}

			if !sameEvents(readEvents(t, log), tc.want) {
				t.Error("the merged segment should hold what the policy kept")
			}
		})
	}
}

func TestMergeOnDemand(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)

	appendEvents(t, log, events(0, 3, memorylog.SealBytes)...)
	appendEvents(t, log, event(3, 10))

	dropAll := func([]memorylog.Row, func(int) ([]byte, error)) ([]memorylog.Event, error) { return nil, nil }

	err := log.Merge(context.Background(), dropAll)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	if len(readManifest(t, dir).Segments) != 0 || len(segmentFiles(t, dir)) != 0 {
		t.Error("a merge keeping nothing should leave no segment")
	}

	if !sameEvents(readEvents(t, log), events(3, 1, 10)) {
		t.Error("a merge should leave the tail alone")
	}

	failing := func([]memorylog.Row, func(int) ([]byte, error)) ([]memorylog.Event, error) {
		return nil, errors.New("policy refused")
	}

	appendEvents(t, log, event(4, memorylog.SealBytes))

	err = log.Merge(context.Background(), failing)
	if err == nil || len(readManifest(t, dir).Segments) != 1 {
		t.Error("a failing policy should fail the merge and leave the segments")
	}
}

func TestFresh(t *testing.T) {
	dir := t.TempDir()
	reader := openLog(t, dir)
	writer := openLog(t, dir)

	appendEvents(t, writer, event(0, 10))

	snapshot, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}
	defer snapshot.Close()

	if !snapshot.Fresh() {
		t.Fatal("a snapshot should be fresh while nothing was written")
	}

	appendEvents(t, writer, event(1, 10))

	if snapshot.Fresh() {
		t.Error("a snapshot should go stale once another log appends")
	}

	sealing, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}
	defer sealing.Close()

	appendEvents(t, writer, event(2, memorylog.SealBytes))

	if sealing.Fresh() {
		t.Error("a snapshot should go stale once another log seals")
	}

	merging, err := reader.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}
	defer merging.Close()

	err = writer.Merge(context.Background(), nil)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	if merging.Fresh() {
		t.Error("a snapshot should go stale once another log rewrites the manifest alone")
	}
}

func TestAppendRefusesUnknownKind(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)

	unknown := event(0, 1)
	unknown.Kind = "forget"

	err := log.Append(context.Background(), event(1, 1), unknown)
	if !errors.Is(err, memorylog.ErrUnknownKind) {
		t.Error("an event of an unknown kind should be refused")
	}

	if len(readEvents(t, log)) != 0 {
		t.Error("a refused append should write none of its events")
	}
}

func TestSnapshotAfterClose(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, event(0, memorylog.SealBytes), event(1, 1))

	snapshot, err := log.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("taking a snapshot: %v", err)
	}

	for _, index := range []int{-1, len(snapshot.Rows())} {
		_, err = snapshot.Payload(index)
		if !errors.Is(err, memorylog.ErrNoRow) {
			t.Error("a payload outside the rows should be refused")
		}
	}

	err = snapshot.Close()
	if err != nil {
		t.Fatalf("closing the snapshot: %v", err)
	}

	_, err = snapshot.Payload(0)
	if !errors.Is(err, memorylog.ErrClosed) {
		t.Error("a closed snapshot should refuse to read payloads")
	}
}

func TestConcurrentAppends(t *testing.T) {
	const (
		writers = 4
		appends = 30
		batch   = 2
	)

	dir := t.TempDir()
	logs := []*memorylog.Log{openLog(t, dir), openLog(t, dir)}

	var writing sync.WaitGroup
	failures := make(chan error, len(logs)*writers*appends)

	for owner, log := range logs {
		for writer := range writers {
			writing.Go(func() {
				for round := range appends {
					var appended []memorylog.Event

					for item := range batch {
						next := event(round*batch+item, 1200)
						next.Seat = fmt.Sprintf("seat-%d-%d", owner, writer)
						appended = append(appended, next)
					}

					err := log.Append(context.Background(), appended...)
					if err != nil {
						failures <- err

						return
					}
				}
			})
		}
	}

	done := make(chan struct{})
	readerFailures := make(chan string, 1)

	go func() {
		defer close(readerFailures)

		for {
			select {
			case <-done:
				return
			default:
			}

			snapshot, err := logs[0].Snapshot(context.Background())
			if err != nil {
				readerFailures <- fmt.Sprintf("taking a snapshot while writers run: %v", err)

				return
			}

			duplicate := hasDuplicate(snapshot.Rows())
			_ = snapshot.Close()

			if duplicate {
				readerFailures <- "a snapshot taken while writers run should never hold an event twice"

				return
			}
		}
	}()

	writing.Wait()
	close(done)
	close(failures)

	for err := range failures {
		t.Errorf("appending concurrently: %v", err)
	}

	for failure := range readerFailures {
		t.Error(failure)
	}

	read := readEvents(t, logs[1])
	if len(read) != len(logs)*writers*appends*batch || hasDuplicate(rowsOf(read)) {
		t.Fatal("concurrent appends should keep every event exactly once")
	}

	order := map[string]int{}

	for _, row := range read {
		number := int(row.Time.Sub(start).Milliseconds())
		if number < order[row.Seat] {
			t.Fatal("each writer's events should read back in the order it appended them")
		}

		order[row.Seat] = number
	}

	if len(segmentFiles(t, dir)) == 0 {
		t.Error("the concurrent appends should have sealed the tail")
	}
}

func hasDuplicate(rows []memorylog.Row) bool {
	seen := map[string]bool{}

	for _, row := range rows {
		key := row.Seat + "/" + row.Entry
		if seen[key] {
			return true
		}

		seen[key] = true
	}

	return false
}

func rowsOf(read []memorylog.Event) []memorylog.Row {
	rows := make([]memorylog.Row, 0, len(read))
	for _, one := range read {
		rows = append(rows, memorylog.Row{Kind: one.Kind, Entry: one.Entry, Time: one.Time, Seat: one.Seat, Target: one.Target})
	}

	return rows
}

func TestCancelledContext(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir)
	appendEvents(t, log, event(0, memorylog.SealBytes))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name string
		run  func() error
	}{
		{name: "append", run: func() error { return log.Append(cancelled, event(1, 1)) }},
		{name: "merge", run: func() error { return log.Merge(cancelled, nil) }},
		{name: "snapshot", run: func() error {
			_, err := log.Snapshot(cancelled)

			return err
		}},
		{name: "dump", run: func() error {
			return memorylog.Dump(cancelled, &strings.Builder{}, filepath.Join(dir, "tail.jsonl"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.run(), context.Canceled) {
				t.Error("a cancelled context should stop the call")
			}
		})
	}

	if !sameEvents(readEvents(t, log), events(0, 1, memorylog.SealBytes)) {
		t.Error("cancelled calls should leave the log as it was")
	}
}
