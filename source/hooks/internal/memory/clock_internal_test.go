package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var filedAt = time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)

// setBack is a memory on a fresh store whose system clock stands a day
// before filedAt.
func setBack(t *testing.T) *memory {
	t.Helper()

	back := filedAt.Add(-24 * time.Hour)

	return &memory{
		env: Env{Roots: home.Roots{Data: t.TempDir(), Cache: t.TempDir()}, Now: func() time.Time { return back }},
		key: "s-clock",
	}
}

func TestMemoryTimeNeverFallsBehindTheStoresMark(t *testing.T) {
	cases := []struct {
		name  string
		store func(t *testing.T, mem *memory)
	}{
		{name: "the mark file", store: func(t *testing.T, mem *memory) {
			test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)
		}},
		{name: "a store without one, from its log", store: func(t *testing.T, mem *memory) {
			err := memorylog.Build(context.Background(), mem.env.Roots.Data, mem.logDir(), []memorylog.Event{
				{Kind: memorylog.KindNew, Entry: "m-newest", Time: filedAt, Seat: "main", Payload: []byte(`{}`)},
				{Kind: memorylog.KindNew, Entry: "m-older", Time: filedAt.Add(-time.Hour), Seat: "main", Payload: []byte(`{}`)},
			})
			if err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := setBack(t)
			tc.store(t, mem)

			if got := mem.now(); !got.After(filedAt) || got.After(filedAt.Add(time.Microsecond)) {
				t.Errorf("memory's time should stand just past the newest stored time while the clock trails it, got %s", got)
			}
		})
	}
}

func TestTheMarkIsTheStoresMemoryWorksOn(t *testing.T) {
	mem := setBack(t)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)
	mem.now()

	mem.key = "s-other"

	if got := mem.now(); got.After(filedAt.Add(-time.Hour)) {
		t.Errorf("a store holding no mark and no log should leave memory's time at the clock, got %s", got)
	}

	if test.Exists(t, mem.store()) {
		t.Error("reading the mark should create nothing of a store that does not exist")
	}
}

func TestTheMarkNeverFalls(t *testing.T) {
	mem := setBack(t)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)
	mem.now()

	raised := filedAt.Add(time.Hour)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+raised.Format(time.RFC3339Nano)+`"}`)
	mem.recordStore(context.Background())

	var held clockFile
	if !readJSON(mem.clockPath(), &held) || !held.Mark.Equal(raised) {
		t.Errorf("a mark another process raised further should stay, got %s", held.Mark)
	}
}

func TestTheMarkIsWrittenOnlyWhereMemoryWrites(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, mem *memory)
	}{
		{name: "no store", setup: func(*testing.T, *memory) {}},
		{name: "a store of a newer format", setup: func(t *testing.T, mem *memory) {
			test.WriteFile(t, filepath.Join(mem.store(), "format.json"), `{"format":999}`)
			mem.unreadable = "newer"
		}},
		{name: "outside git", setup: func(t *testing.T, mem *memory) {
			test.WriteFile(t, filepath.Join(mem.store(), "format.json"), `{"format":9}`)
			mem.outsideGit = true
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := setBack(t)
			tc.setup(t, mem)
			mem.now()

			if mem.keepMark() || test.Exists(t, mem.clockPath()) {
				t.Error("memory should write no mark where it writes nothing else")
			}
		})
	}
}

func TestAWarmRaisesTheMark(t *testing.T) {
	mem := setBack(t)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)

	folded := newState()
	folded.entries["m-warm"] = &entry{id: "m-warm", filed: filedAt.Add(-time.Hour), retention: "normal"}
	mem.warm(folded, heatShow, "m-warm")

	var held clockFile
	if !readJSON(mem.clockPath(), &held) || !held.Mark.After(filedAt) {
		t.Errorf("a warm should raise the mark past the time it stored, got %s", held.Mark)
	}
}

func TestHeatAgesByTheMarkWhileTheClockTrails(t *testing.T) {
	mem := setBack(t)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)

	held := &entry{id: "m-early", filed: filedAt.Add(-14 * 24 * time.Hour), retention: "normal"}

	heat := mem.heat(map[string]heatValue{}, held)
	if heat <= 0 || heat > heatNew*0.51 {
		t.Errorf("an entry filed before the rollback should decay by the age the mark gives it, got heat %v", heat)
	}
}

func TestTheMarkRisesWithAStoredTime(t *testing.T) {
	mem := setBack(t)
	test.WriteFile(t, mem.clockPath(), `{"mark":"`+filedAt.Format(time.RFC3339Nano)+`"}`)

	stamped := mem.now()
	mem.recordStore(context.Background())

	again := setBack(t)
	again.env.Roots = mem.env.Roots

	if got := again.now(); !got.After(stamped) {
		t.Errorf("a later process should stamp after the time the store just took, got %s after %s", got, stamped)
	}

	if got := mem.now(); !got.After(stamped) {
		t.Errorf("the same process should stamp after the time the store just took, got %s after %s", got, stamped)
	}

	if !test.Exists(t, filepath.Join(mem.store(), clockMarkName)) {
		t.Error("the mark should live in the store")
	}
}
