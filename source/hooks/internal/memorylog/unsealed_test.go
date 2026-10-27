package memorylog_test

import (
	"context"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

func TestUnsealedAgreesWithTheSnapshot(t *testing.T) {
	cases := []struct {
		name     string
		appended []memorylog.Event
		sealed   bool
	}{
		{name: "a tail alone", appended: events(0, 5, 10)},
		{name: "segments and a tail", appended: events(0, 70, 2000), sealed: true},
		{name: "an empty log"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := openLog(t, t.TempDir())
			if len(tc.appended) > 0 {
				appendEvents(t, log, tc.appended...)
			}

			snapshot, err := log.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = snapshot.Close() }()

			stamps, tail, err := log.Unsealed(context.Background())
			if err != nil {
				t.Fatalf("reading the unsealed part should succeed: %v", err)
			}

			if !slices.Equal(stamps, snapshot.Segments()) || (len(stamps) > 0) != tc.sealed {
				t.Errorf("both should stamp the same sealed segments, got %v and %v", stamps, snapshot.Segments())
			}

			rows := snapshot.Rows()
			if snapshot.Sealed()+len(tail) != len(rows) {
				t.Error("the sealed rows and the tail should add up to every row")
			}

			for index, held := range tail {
				row := rows[snapshot.Sealed()+index]
				if held.Entry != row.Entry || !held.Time.Equal(row.Time) {
					t.Error("the tail should hold the rows after the sealed ones, in order")
				}
			}
		})
	}
}

func TestUnsealedSeesWhatIsAppended(t *testing.T) {
	log := openLog(t, t.TempDir())
	appendEvents(t, log, events(0, 3, 10)...)

	before, tail, err := log.Unsealed(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	appendEvents(t, log, events(3, 1, 10)...)

	after, grown, err := log.Unsealed(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(before, after) || len(grown) != len(tail)+1 || grown[len(grown)-1].Entry != "m-00003" {
		t.Error("an append should leave the sealed segments and grow the tail by what it wrote")
	}

	appendEvents(t, log, events(4, 70, 2000)...)

	sealed, _, err := log.Unsealed(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if slices.Equal(sealed, after) {
		t.Error("a seal should change the sealed segments' stamps")
	}
}
