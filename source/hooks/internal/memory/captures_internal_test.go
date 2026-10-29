package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// namesMark is the seen list as the JSON names of the month's entry files: the
// alternative BenchmarkCaptureNotice weighs against the bitset graphMark keeps.
type namesMark struct {
	Month string   `json:"month"`
	Dir   string   `json:"dir"`
	Stamp string   `json:"stamp"`
	Names []string `json:"names,omitempty"`
}

// captureNoticeNames is captureNotice with the seen list kept as namesMark,
// up to where both load the store.
func (mem *memory) captureNoticeNames(ctx context.Context, session string) int {
	now := mem.now().Local()
	monthKey := now.Format("2006/01")

	var mark namesMark

	had := mem.readSlot("graph-names-json/"+session, &mark)

	month := mark.Dir
	if !had || mark.Month != monthKey || month == "" {
		month = filepath.Join(mem.graphProject(ctx).entries, now.Format("2006"), now.Format("01"))
	}

	stamp := "-"

	info, err := os.Stat(month)
	if err == nil {
		stamp = fmt.Sprintf("%d@%d", info.ModTime().UnixNano(), info.Size())
	}

	if had && mark.Month == monthKey && mark.Dir == month && mark.Stamp == stamp {
		return 0
	}

	files, _ := os.ReadDir(month)

	var names, gained []string

	for _, file := range files {
		id := now.Format("200601") + strings.TrimSuffix(file.Name(), ".md")
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") || !isGraphID(id) {
			continue
		}

		names = append(names, file.Name())

		if had && (mark.Month != monthKey || !slices.Contains(mark.Names, file.Name())) {
			gained = append(gained, id)
		}
	}

	_ = mem.writeSlot("graph-names-json/"+session, namesMark{Month: monthKey, Dir: month, Stamp: stamp, Names: names})

	if len(gained) > 0 {
		_, _ = mem.load(ctx)
	}

	return len(gained)
}

// captureMonth is a store whose graph's month holds the fixture's entries,
// about as many as a busy project files in a month.
func captureMonth(b *testing.B) (*memory, string) {
	b.Helper()

	root := b.TempDir()
	filed := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	entries := filepath.Join(root, "graph")
	month := filepath.Join(entries, "2026", "10")

	err := os.MkdirAll(month, 0o700)
	if err != nil {
		b.Fatal(err)
	}

	for _, name := range strings.Fields(test.Fixture(b, "memory/capture-month.txt")) {
		err = os.WriteFile(filepath.Join(month, name), nil, 0o600)
		if err != nil {
			b.Fatal(err)
		}
	}

	mem := &memory{
		env: Env{
			Roots: home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:   func() time.Time { return filed },
		},
		key:         "bench",
		graphConfig: &resolvedGraph{entries: entries},
	}

	return mem, month
}

// BenchmarkCaptureNotice weighs the seen list of the capture notice as a
// bitset over the store's table of names against JSON names, on a tool call
// that finds the month unchanged and on one that finds an entry added.
func BenchmarkCaptureNotice(b *testing.B) {
	variants := []struct {
		name   string
		notice func(mem *memory, session string)
	}{
		{name: "bitset", notice: func(mem *memory, session string) {
			mem.captureNotice(context.Background(), "main", session)
		}},
		{name: "json", notice: func(mem *memory, session string) {
			mem.captureNoticeNames(context.Background(), session)
		}},
	}

	for _, variant := range variants {
		b.Run(variant.name+"/unchanged", func(b *testing.B) {
			mem, _ := captureMonth(b)
			variant.notice(mem, test.Session)

			for b.Loop() {
				variant.notice(mem, test.Session)
			}
		})

		b.Run(variant.name+"/one-added", func(b *testing.B) {
			mem, month := captureMonth(b)
			variant.notice(mem, test.Session)

			added := filepath.Join(month, "29-235959-d-tac-zzz.md")
			present := false

			for b.Loop() {
				b.StopTimer()

				if present {
					_ = os.Remove(added)
				} else {
					_ = os.WriteFile(added, nil, 0o600)
				}

				present = !present

				b.StartTimer()
				variant.notice(mem, test.Session)
			}
		})
	}
}

func TestBothSeenListsTellTheSameGains(t *testing.T) {
	root := t.TempDir()
	filed := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	month := filepath.Join(root, "graph", "2026", "10")

	names := strings.Fields(test.Fixture(t, "memory/capture-month.txt"))[:50]
	for _, name := range names {
		test.WriteFile(t, filepath.Join(month, name), "")
	}

	mem := &memory{
		env: Env{
			Roots: home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:   func() time.Time { return filed },
		},
		key:         "bench",
		graphConfig: &resolvedGraph{entries: filepath.Join(root, "graph")},
	}

	_, before := mem.captureNotice(context.Background(), "main", "bits")
	namesBefore := mem.captureNoticeNames(context.Background(), "names")

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, after := mem.captureNotice(context.Background(), "main", "bits")
	namesAfter := mem.captureNoticeNames(context.Background(), "names")

	if before != 0 || namesBefore != 0 || after != 1 || namesAfter != after {
		t.Errorf("both seen lists should count the one entry added, bitset %d then %d, names %d then %d",
			before, after, namesBefore, namesAfter)
	}
}

func captureStore(t *testing.T) (*memory, string) {
	t.Helper()

	root := t.TempDir()
	month := filepath.Join(root, "graph", "2026", "10")

	for _, name := range strings.Fields(test.Fixture(t, "memory/capture-month.txt"))[:20] {
		test.WriteFile(t, filepath.Join(month, name), "")
	}

	return &memory{
		env: Env{
			Roots: home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:   func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local) },
		},
		key:         "bench",
		graphConfig: &resolvedGraph{entries: filepath.Join(root, "graph")},
	}, month
}

func TestSessionsSharingTheNameTableEachCountWhatTheyMissed(t *testing.T) {
	mem, month := captureStore(t)

	_, early := mem.captureNotice(context.Background(), "main", "early")

	test.WriteFile(t, filepath.Join(month, "29-235958-d-tac-yyy.md"), "")

	_, late := mem.captureNotice(context.Background(), "main", "late")

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, earlyAfter := mem.captureNotice(context.Background(), "main", "early")
	_, lateAfter := mem.captureNotice(context.Background(), "main", "late")

	if early != 0 || late != 0 || earlyAfter != 2 || lateAfter != 1 {
		t.Errorf("each session should count the entries added since its own last call, got %d %d %d %d",
			early, late, earlyAfter, lateAfter)
	}
}

func TestANameTableWithoutRoomLeavesNewEntriesUnnamed(t *testing.T) {
	mem, month := captureStore(t)
	key := graphNamesPrefix + "2026/10"

	_, before := mem.captureNotice(context.Background(), "main", test.Session)

	err := mem.slotFile().Update(context.Background(), key, func(table []byte, _ bool) ([]byte, error) {
		for filler := 0; sessionSlotWidth-(32+len(key)+len(table)) >= len("00-000000-s-tac-filler\n"); filler++ {
			table = fmt.Appendf(table, "00-%06d-s-tac-filler\n", filler)
		}

		if room := sessionSlotWidth - (32 + len(key) + len(table)); room > 0 {
			table = append(table, strings.Repeat("x", room-1)+"\n"...)
		}

		return table, nil
	})
	if err != nil {
		t.Fatalf("filling the name table: %v", err)
	}

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, after := mem.captureNotice(context.Background(), "main", test.Session)

	if before != 0 || after != 0 {
		t.Errorf("an entry the table has no room for should count as nothing gained, got %d", after)
	}
}

func TestTheNameTablesOfMonthsBeforeTheLastAreReclaimed(t *testing.T) {
	mem, _ := captureStore(t)

	for _, key := range []string{"graph-names/2026/10", "graph-names/2026/09", "graph-names/2026/08", "graph-names/2025/10"} {
		err := mem.slotFile().Put(context.Background(), key, []byte("29-235959-d-tac-zzz\n"))
		if err != nil {
			t.Fatalf("writing %s: %v", key, err)
		}
	}

	mem.reclaimSessions(context.Background(), func(slots.Slot) bool { return false })

	for key, kept := range map[string]bool{
		"graph-names/2026/10": true, "graph-names/2026/09": true, "graph-names/2026/08": false, "graph-names/2025/10": false,
	} {
		if mem.hasSlot(key) != kept {
			t.Errorf("only the tables of this month and the last should stay, %s kept: %v", key, mem.hasSlot(key))
		}
	}
}
