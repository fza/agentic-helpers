package memory

import (
	"bytes"
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

// graphShape is a graph the capture notice is weighed on: its entry paths,
// `<yyyy>/<mm>/<name>.md`, and the time its tool calls run at.
type graphShape struct {
	name  string
	paths func(testing.TB) []string
	now   time.Time
}

// graphShapes are a graph shaped like this repository's, and a larger one of
// 5,000 entries spread over 36 months.
func graphShapes() []graphShape {
	return []graphShape{
		{name: "repo", now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local), paths: func(tb testing.TB) []string {
			return strings.Fields(test.Fixture(tb, "memory/capture-graph.txt"))
		}},
		{name: "synthetic", now: time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local), paths: func(testing.TB) []string {
			const entries, months = 5_000, 36

			first := time.Date(2023, 12, 1, 0, 0, 0, 0, time.Local)
			paths := make([]string, 0, entries)

			for index := range entries {
				month := first.AddDate(0, index*months/entries, 0)
				paths = append(paths, fmt.Sprintf("%s/%02d-%06d-%s-tac-%03d.md",
					month.Format("2006/01"), 1+index%28, index, []string{"d", "s"}[index%2], index%1000))
			}

			return paths
		}},
	}
}

// shapedGraph is a store whose graph holds the shape's entries.
func shapedGraph(b *testing.B, shape graphShape) (*memory, string) {
	b.Helper()

	root := b.TempDir()
	entries := filepath.Join(root, "graph")

	for _, path := range shape.paths(b) {
		err := os.MkdirAll(filepath.Join(entries, filepath.Dir(path)), 0o700)
		if err == nil {
			err = os.WriteFile(filepath.Join(entries, path), nil, 0o600)
		}

		if err != nil {
			b.Fatal(err)
		}
	}

	return &memory{
		env: Env{
			Roots: home.Roots{Data: filepath.Join(root, "data"), Cache: filepath.Join(root, "cache")},
			Now:   func() time.Time { return shape.now },
		},
		key:         "bench",
		graphConfig: &resolvedGraph{entries: entries},
	}, entries
}

// BenchmarkCaptureNoticeOnGraphs weighs the capture notice on each graph
// shape: a tool call finding the graph unchanged, one finding an entry added
// to the current month, one finding an entry pulled into the oldest month,
// and a session's first look.
func BenchmarkCaptureNoticeOnGraphs(b *testing.B) {
	for _, shape := range graphShapes() {
		notice := func(mem *memory, session string) {
			mem.captureNotice(context.Background(), "main", session)
		}

		b.Run(shape.name+"/unchanged", func(b *testing.B) {
			mem, _ := shapedGraph(b, shape)
			notice(mem, test.Session)

			for b.Loop() {
				notice(mem, test.Session)
			}
		})

		added := func(b *testing.B, month func(paths []string) string) {
			mem, entries := shapedGraph(b, shape)
			folder := filepath.Join(entries, filepath.FromSlash(month(shape.paths(b))))
			notice(mem, test.Session)

			for count := 0; b.Loop(); count++ {
				b.StopTimer()

				path := filepath.Join(folder, fmt.Sprintf("28-%06d-d-tac-zzz.md", count))
				_ = os.WriteFile(path, nil, 0o600)

				b.StartTimer()
				notice(mem, test.Session)
				b.StopTimer()

				_ = os.Remove(path)
				notice(mem, test.Session)

				b.StartTimer()
			}
		}

		b.Run(shape.name+"/one-added", func(b *testing.B) {
			added(b, func(paths []string) string { return filepath.ToSlash(filepath.Dir(paths[len(paths)-1])) })
		})

		b.Run(shape.name+"/one-pulled-into-oldest-month", func(b *testing.B) {
			added(b, func(paths []string) string { return filepath.ToSlash(filepath.Dir(paths[0])) })
		})

		b.Run(shape.name+"/first-look", func(b *testing.B) {
			mem, _ := shapedGraph(b, shape)
			notice(mem, test.Session)

			for count := 0; b.Loop(); count++ {
				notice(mem, fmt.Sprintf("session-%d", count))
			}
		})
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

func TestAnEntryArrivingInAnyMonthIsGained(t *testing.T) {
	for _, arrival := range []struct{ name, path, id string }{
		{name: "the current month", path: "2026/10/29-235959-d-tac-pul.md", id: "20261029-235959-d-tac-pul"},
		{name: "an earlier month", path: "2026/09/30-120000-d-tac-pul.md", id: "20260930-120000-d-tac-pul"},
		{name: "a month folder new to its year", path: "2026/08/30-120000-s-tac-pul.md", id: "20260830-120000-s-tac-pul"},
		{name: "a year folder new to the graph", path: "2025/12/30-120000-d-cpt-pul.md", id: "20251230-120000-d-cpt-pul"},
	} {
		t.Run(arrival.name, func(t *testing.T) {
			mem, month := captureStore(t)
			test.WriteFile(t, filepath.Join(filepath.Dir(filepath.Dir(month)), "2026", "09", "29-120000-d-tac-old.md"), "")

			_, before := mem.captureNotice(context.Background(), "main", test.Session)

			test.WriteFile(t, filepath.Join(filepath.Dir(filepath.Dir(month)), filepath.FromSlash(arrival.path)), "")

			_, after := mem.captureNotice(context.Background(), "main", test.Session)
			_, again := mem.captureNotice(context.Background(), "main", test.Session)

			if len(before) != 0 || !slices.Equal(after, []string{arrival.id}) || len(again) != 0 {
				t.Errorf("an entry arriving in any month folder should be gained once, got %v then %v then %v", before, after, again)
			}
		})
	}
}

func TestACheckoutOfAnOlderRefGainsNothing(t *testing.T) {
	mem, month := captureStore(t)
	graph := filepath.Dir(filepath.Dir(month))
	earlier := filepath.Join(graph, "2026", "09")
	kept := filepath.Join(month, "29-100000-d-tac-ren.md")
	renamed := filepath.Join(month, "29-100001-d-tac-ren.md")
	pulled := filepath.Join(month, "29-110000-s-tac-new.md")

	test.WriteFile(t, kept, "")
	test.WriteFile(t, filepath.Join(earlier, "30-120000-d-tac-old.md"), "")

	look := func() []string {
		_, gained := mem.captureNotice(context.Background(), "main", test.Session)

		return gained
	}

	move := func(from string, to string) {
		err := os.Rename(from, to)
		if err != nil {
			t.Fatalf("moving %s: %v", from, err)
		}
	}

	look()

	move(kept, renamed)
	test.WriteFile(t, pulled, "")

	newer := look()

	move(renamed, kept)

	err := os.Remove(pulled)
	if err == nil {
		err = os.RemoveAll(earlier)
	}

	if err != nil {
		t.Fatalf("checking out the older ref: %v", err)
	}

	older := look()

	move(kept, renamed)
	test.WriteFile(t, pulled, "")
	test.WriteFile(t, filepath.Join(earlier, "30-120000-d-tac-old.md"), "")

	back := look()

	if !slices.Equal(newer, []string{"20261029-100001-d-tac-ren", "20261029-110000-s-tac-new"}) {
		t.Errorf("a pull renaming one entry and adding another should gain both names, got %v", newer)
	}

	if len(older) != 0 || len(back) != 0 {
		t.Errorf("an entry deleted, renamed back or brought back by a checkout should never count as gained, got %v then %v", older, back)
	}
}

func TestAFolderNotShapedAsAYearOrAMonthIsNeverLookedInto(t *testing.T) {
	mem, month := captureStore(t)
	graph := filepath.Dir(filepath.Dir(month))

	_, before := mem.captureNotice(context.Background(), "main", test.Session)

	for _, path := range []string{"notes/10/30-120000-d-tac-pul.md", "2026/drafts/30-120000-d-tac-pul.md", "20266/10/30-120000-d-tac-pul.md"} {
		test.WriteFile(t, filepath.Join(graph, filepath.FromSlash(path)), "")
	}

	_, after := mem.captureNotice(context.Background(), "main", test.Session)

	if len(before) != 0 || len(after) != 0 {
		t.Errorf("a file in a folder not named as a year or a month should never count as gained, got %v", after)
	}
}

func TestAFullLookLostOrStaleReadsAsAFirstLook(t *testing.T) {
	for _, damage := range []struct {
		name  string
		apply func(mem *memory)
	}{
		{name: "lost", apply: func(mem *memory) { mem.clearSlot(mem.graphFoldersKey(test.Session)) }},
		{name: "stale", apply: func(mem *memory) {
			mem.writeSlot(mem.graphFoldersKey(test.Session), &graphFolders{Digest: "stale", Folders: map[string]folderMark{}})
		}},
	} {
		t.Run(damage.name, func(t *testing.T) {
			mem, month := captureStore(t)

			_, before := mem.captureNotice(context.Background(), "main", test.Session)

			damage.apply(mem)
			test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

			_, damaged := mem.captureNotice(context.Background(), "main", test.Session)

			test.WriteFile(t, filepath.Join(month, "29-235958-d-tac-yyy.md"), "")

			_, after := mem.captureNotice(context.Background(), "main", test.Session)

			if len(before) != 0 || len(damaged) != 0 || !slices.Equal(after, []string{"20261029-235958-d-tac-yyy"}) {
				t.Errorf("a full look not matching its brief should read as a first look, then count again, got %v then %v", damaged, after)
			}
		})
	}
}

func TestARefusedSessionLooksAtNoFolder(t *testing.T) {
	mem, month := captureStore(t)
	mem.sessionless = true

	_, before := mem.captureNotice(context.Background(), "main", test.Session)

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, after := mem.captureNotice(context.Background(), "main", test.Session)

	if len(before) != 0 || len(after) != 0 || mem.hasSlot(namesKey("2026/10", 0)) {
		t.Errorf("a session keeping no mark should list no month and tell nothing, got %v", after)
	}
}

func TestAGraphElsewhereAtTheMonthsTurnIsLookedAtAfresh(t *testing.T) {
	mem, _ := captureStore(t)

	_, before := mem.captureNotice(context.Background(), "main", test.Session)

	elsewhere := filepath.Join(t.TempDir(), "graph")
	test.WriteFile(t, filepath.Join(elsewhere, "2026", "10", "29-235959-d-tac-zzz.md"), "")
	test.WriteFile(t, filepath.Join(elsewhere, "2026", "11", "01-000001-d-tac-zzz.md"), "")

	mem.graphConfig = &resolvedGraph{entries: elsewhere}
	mem.env.Now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local) }

	_, after := mem.captureNotice(context.Background(), "main", test.Session)

	if len(before) != 0 || len(after) != 0 {
		t.Errorf("a graph resolved elsewhere should be marked afresh, telling none of its entries, got %v", after)
	}
}

func TestSessionsSharingTheNameTableEachCountWhatTheyMissed(t *testing.T) {
	mem, month := captureStore(t)

	_, early := mem.captureNotice(context.Background(), "main", "early")

	test.WriteFile(t, filepath.Join(month, "29-235958-d-tac-yyy.md"), "")

	_, late := mem.captureNotice(context.Background(), "main", "late")

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, earlyAfter := mem.captureNotice(context.Background(), "main", "early")
	_, lateAfter := mem.captureNotice(context.Background(), "main", "late")

	if len(early) != 0 || len(late) != 0 || len(earlyAfter) != 2 || len(lateAfter) != 1 {
		t.Errorf("each session should count the entries added since its own last call, got %d %d %d %d",
			len(early), len(late), len(earlyAfter), len(lateAfter))
	}
}

func TestAFullPartOfTheNameTableGrowsItByAPart(t *testing.T) {
	mem, month := captureStore(t)
	key := namesKey("2026/10", 0)

	_, before := mem.captureNotice(context.Background(), "main", test.Session)

	err := mem.slotFile().Update(context.Background(), key, func(table []byte, _ bool) ([]byte, error) {
		for filler := 0; bytes.Count(table, []byte("\n")) < namesPerPart; filler++ {
			table = fmt.Appendf(table, "00-%06d-s-tac-fil\n", filler)
		}

		return table, nil
	})
	if err != nil {
		t.Fatalf("filling the name table: %v", err)
	}

	test.WriteFile(t, filepath.Join(month, "29-235959-d-tac-zzz.md"), "")

	_, after := mem.captureNotice(context.Background(), "main", test.Session)

	next, found, err := mem.slotFile().Get(context.Background(), namesKey("2026/10", 1))
	if len(before) != 0 || len(after) != 1 || !found || err != nil || string(next) != "29-235959-d-tac-zzz\n" {
		t.Errorf("an entry a full part has no room for should go into the next part and count as gained, "+
			"got %d gained, next part %q", len(after), next)
	}

	_, again := mem.captureNotice(context.Background(), "main", test.Session)
	if len(again) != 0 {
		t.Errorf("an entry in the next part should be told once, told again: %v", again)
	}
}

func TestOnlyAnEarlierReleasesNameTablesOfMonthsBeforeTheLastAreReclaimed(t *testing.T) {
	mem, _ := captureStore(t)

	for _, key := range []string{
		"graph-names/2026/10", "graph-names/2026/10/1", "graph-names/2026/09", "graph-names/2026/08", "graph-names/2026/08/1",
		"graph-names/2025/10", "graph-table/2026/10", "graph-table/2025/10", "graph-table/2025/10/1",
	} {
		err := mem.slotFile().Put(context.Background(), key, []byte("29-235959-d-tac-zzz\n"))
		if err != nil {
			t.Fatalf("writing %s: %v", key, err)
		}
	}

	mem.reclaimSessions(context.Background(), func(slots.Slot) bool { return false })

	for key, kept := range map[string]bool{
		"graph-names/2026/10": true, "graph-names/2026/10/1": true, "graph-names/2026/09": true,
		"graph-names/2026/08": false, "graph-names/2026/08/1": false, "graph-names/2025/10": false, "graph-table/2026/10": true, "graph-table/2025/10": true, "graph-table/2025/10/1": true,
	} {
		if mem.hasSlot(key) != kept {
			t.Errorf("the capture notice should keep its tables of every month and an earlier release's of this month and the last only, %s kept: %v", key, mem.hasSlot(key))
		}
	}
}
