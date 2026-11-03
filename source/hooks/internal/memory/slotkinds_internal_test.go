package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// worst is text of size bytes JSON spends the most on: every `<` takes six,
// and so does every control character.
func worst(size int) string {
	return strings.Repeat("<\x01", size/2+1)[:size]
}

func worstList(count int, size int) []string {
	list := make([]string, count)
	for index := range list {
		list[index] = fmt.Sprintf("%d", index) + worst(size)
	}

	return list
}

func worstShown() shownSet {
	return shownSet{
		IDs: worstList(5_000, 64), Bodies: worstList(5_000, 64), Notified: true, Traps: worstList(5_000, 64),
		Notes: worstList(5_000, 64), Landed: worstList(5_000, 64),
	}
}

// worstItems are do-now items past every cap, the ones whose id or trigger
// passes its cap first.
func worstItems() []doNowItem {
	items := []doNowItem{
		{ID: "m-" + worst(100), Title: "t", Trigger: "stop", Since: fullTime},
		{ID: "m-bad-trigger", Title: "t", Trigger: worst(100), Since: fullTime},
	}

	for index := range 500 {
		items = append(items, doNowItem{
			ID: fmt.Sprintf("m-%06d", index), Title: worst(5_000), Trigger: "stop",
			Proof: append(worstList(5, 1_000), worstList(10, 10)...), Since: fullTime,
		})
	}

	return items
}

var fullTime = time.Date(2026, 11, 3, 23, 59, 59, 999_999_999, time.FixedZone("far", -11*3600))

func encodedWithin(t *testing.T, what string, list []string, limit int) {
	t.Helper()

	for _, item := range list {
		if slots.EncodedLen(item) > limit {
			t.Errorf("every %s kept should stay within its cap, one takes %d bytes", what, slots.EncodedLen(item))

			return
		}
	}
}

func newestKept(t *testing.T, what string, kept []string, original []string) {
	t.Helper()

	if len(kept) == 0 || kept[len(kept)-1] != original[len(original)-1] {
		t.Errorf("a %s past its ceiling should keep its newest items, kept %d", what, len(kept))
	}
}

// worstCases builds each kind of value the slot file holds at its worst: every
// list past its count, every string past its cap in the bytes JSON spends the
// most on, under the longest key the kind takes. check says what the fitted
// value keeps.
var worstCases = []struct {
	kind  string
	key   string
	value func() slots.Fitted
	check func(t *testing.T, fitted slots.Fitted)
}{
	{
		kind: "ladder", key: "ladder/" + strings.Repeat("s", 128),
		value: func() slots.Fitted {
			return &ladder{
				Nudges: 1 << 62, Rung: 1 << 62, TranscriptBase: 1 << 62, NudgedAt: fullTime, ReplayNudgedFor: fullTime,
				ReplayCheckedSize: 1 << 62, Wrote: append(worstList(500, 4_096), worstList(30, 100)...),
				LastPrompt: worst(200_000), Owed: strings.Join(worstList(2_000, 400), "\n"), ToolCalls: 1 << 62,
			}
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*ladder)
			if held.LastPrompt == "" || slots.EncodedLen(held.LastPrompt) > promptBytes {
				t.Errorf("the prompt should be cut to its bytes, %d", slots.EncodedLen(held.LastPrompt))
			}

			if len(held.Wrote) == 0 || slots.EncodedLen(held.Wrote) > wroteBytes {
				t.Errorf("the written files should keep the paths fitting their bytes, %d", len(held.Wrote))
			}

			if !strings.HasSuffix(held.Owed, "more") || slots.EncodedLen(held.Owed) > owedBytes {
				t.Errorf("the owed line should keep its first lines and count the rest")
			}

			if held.Nudges != 1<<62 || held.NudgedAt != fullTime {
				t.Errorf("the ladder should keep its fixed fields")
			}
		},
	},
	{
		kind: "shown", key: "shown/" + strings.Repeat("s", 128) + "/agent/" + strings.Repeat("a", 64),
		value: func() slots.Fitted {
			shown := worstShown()

			return &shown
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			newestKept(t, "shown set", fitted.(*shownSet).IDs, worstShown().IDs)
		},
	},
	{
		kind: "spawn", key: "spawn/" + strings.Repeat("s", 128) + "/" + strings.Repeat("t", 64),
		value: func() slots.Fitted {
			return &forkSpawn{Level: worst(100), Seat: worst(1_000), Shown: worstShown()}
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*forkSpawn)
			if held.Seat != "" || held.Level != levelNone.String() {
				t.Errorf("a spawn should keep no level or seat past its cap")
			}

			newestKept(t, "spawn's shown set", held.Shown.IDs, worstShown().IDs)
		},
	},
	{
		kind: "agents", key: "agents/" + strings.Repeat("s", 128),
		value: func() slots.Fitted {
			table := subagentTable{Agents: map[string]subagentRecord{}}
			for index := range 3_000 {
				table.Agents[fmt.Sprintf("0%04d", index)+worst(100)] = subagentRecord{Level: "full", Seat: "s-1"}
				table.Agents[fmt.Sprintf("1%04d", index)] = subagentRecord{Level: "full", Seat: worst(100)}
				table.Agents[fmt.Sprintf("2%04d", index)] = subagentRecord{Level: worst(100)}
				table.Agents[fmt.Sprintf("a%04d", index)+worst(9)] = subagentRecord{Level: "full", Seat: "s-" + worst(8)}
			}

			return &table
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*subagentTable)
			for id := range held.Agents {
				if !strings.HasPrefix(id, "a") {
					t.Errorf("a record whose id, level or seat passes its cap should be left out")

					break
				}
			}

			if len(held.Agents) != agentRecords/2 {
				t.Errorf("a table past its count and ceiling should keep half its count, kept %d", len(held.Agents))
			}
		},
	},
	{
		kind: "instructions", key: "instructions/" + strings.Repeat("s", 128),
		value: func() slots.Fitted {
			loaded := loadedInstructions{Files: map[string]string{}}
			for index := range 3_000 {
				loaded.Files[fmt.Sprintf("0%d", index)+worst(4_096)] = strings.Repeat("f", 64)
				loaded.Files[fmt.Sprintf("a%d", index)+worst(160)] = strings.Repeat("f", 64)
			}

			return &loaded
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			paths := slices.Collect(maps.Keys(fitted.(*loadedInstructions).Files))
			if len(paths) == 0 {
				t.Errorf("a record past its ceiling should keep watching some files")
			}

			encodedWithin(t, "watched path", paths, pathBytes)
		},
	},
	{
		kind: "graph-seen", key: "graph-seen/" + strings.Repeat("s", 128),
		value: func() slots.Fitted {
			return &graphMark{Month: "2026/11", Dir: worst(4_096), Stamp: worst(400), Seen: bytes.Repeat([]byte{0xff}, 100_000)}
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*graphMark)
			if held.Dir != noGraph || held.Month != "2026/11" {
				t.Errorf("a graph mark past its ceiling should mark its month as holding no graph")
			}
		},
	},
	{
		kind: "traps", key: "traps/s-abcdef",
		value: func() slots.Fitted {
			index := trapIndex{}
			for position := range 2_000 {
				index.Traps = append(index.Traps, indexedTrap{
					ID: fmt.Sprintf("m-%06d", position), Anchors: worstList(10, 160), When: worstList(10, 80),
				})
			}

			return &index
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*trapIndex)
			if !held.FromLog || len(held.Traps)+len(held.Notes) != 0 {
				t.Errorf("a trap index past its ceiling should leave the marker matching from the log")
			}
		},
	},
	{
		kind: "traps at their caps", key: "traps/s-abcdef",
		value: func() slots.Fitted {
			index := trapIndex{}
			for position := range 4 {
				index.Traps = append(index.Traps, indexedTrap{
					ID: fmt.Sprintf("m-%06d", position), Anchors: append(worstList(5, 2_000), worstList(5, 160)...),
					When: append(worstList(5, 1_000), worstList(5, 80)...),
				})
				index.Notes = append(index.Notes, indexedNote{
					ID: fmt.Sprintf("m-%06d", position), About: append(worstList(5, 2_000), worstList(5, 160)...),
				})
			}

			return &index
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*trapIndex)
			if held.FromLog || len(held.Traps) != 4 || len(held.Notes) != 4 {
				t.Fatalf("an index whose anchors and triggers fit their caps should stay an index")
			}

			for position := range held.Traps {
				encodedWithin(t, "anchor", held.Traps[position].Anchors, pathBytes)
				encodedWithin(t, "trigger", held.Traps[position].When, triggerBytes)
				encodedWithin(t, "path a note is about", held.Notes[position].About, pathBytes)
			}
		},
	},
	{
		kind: "do-now", key: "do-now/s-abcdef",
		value: func() slots.Fitted {
			return &doNowState{Session: worst(1_000), Items: worstItems(), Stops: map[string]int{"stop": 1 << 62, worst(1_000): 1}}
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*doNowState)
			if held.Session != "" || len(held.Stops) != 1 {
				t.Errorf("a do-now state should keep no session past its cap, nor stops of triggers it holds no item for")
			}

			checkItems(t, held.Items)
		},
	},
	{
		kind: "skipped", key: "skipped/s-abcdef",
		value: func() slots.Fitted {
			var skipped skippedItems
			for _, item := range worstItems() {
				skipped = append(skipped, item, item)
			}

			return &skipped
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := *fitted.(*skippedItems)
			checkItems(t, held)

			seen := map[string]bool{}
			for _, item := range held {
				if seen[item.ID] {
					t.Errorf("skipped items should name each rule once")

					break
				}

				seen[item.ID] = true
			}
		},
	},
	{
		kind: "seen", key: "seen/agent/" + strings.Repeat("a", 64),
		value: func() slots.Fitted {
			return &seenMark{
				At: fullTime, Changed: fullTime, Log: strings.Repeat("l", 100), Traps: 1 << 62, Due: worstList(20_000, 64),
				NextDue: fullTime,
			}
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			newestKept(t, "seen mark", fitted.(*seenMark).Due, worstList(20_000, 64))
		},
	},
	{
		kind: "ledger-ended", key: "ledger-ended/s-abcdef",
		value: func() slots.Fitted { return &ledgerEnds{Shown: worstList(20_000, 40)} },
		check: func(t *testing.T, fitted slots.Fitted) {
			newestKept(t, "ended lines' mark", fitted.(*ledgerEnds).Shown, worstList(20_000, 40))
		},
	},
	{
		kind: "landed", key: landedKey,
		value: func() slots.Fitted {
			held := landedSet{}
			for index := range 2_000 {
				held.Mappings = append(held.Mappings, landedMapping{
					Target: fmt.Sprintf("m-%06d#", index) + worst(1_000), Commit: strings.Repeat("c", 40),
					Subject: "s", Test: "t",
				})
			}

			for index := range 2_000 {
				held.Mappings = append(held.Mappings, landedMapping{
					Target: fmt.Sprintf("m-%06d#a%08d", index, index), Commit: strings.Repeat("c", 40),
					Subject: worst(10_000), Test: worst(10_000), Merge: true, Score: -1.2345678901234567e-300,
				})
			}

			return &held
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := fitted.(*landedSet)
			if len(held.Mappings) != landedMappings/2 {
				t.Errorf("a set past its count and ceiling should keep half its count, kept %d", len(held.Mappings))
			}

			for _, mapping := range held.Mappings {
				if slots.EncodedLen(mapping.Target) > mappingTarget || slots.EncodedLen(mapping.Subject) > mappingText ||
					slots.EncodedLen(mapping.Test) > mappingText {
					t.Errorf("every mapping kept should stay within its caps")

					break
				}
			}
		},
	},
	{
		kind: "branch-tips", key: branchTipsKey,
		value: func() slots.Fitted {
			tips := branchTips{}
			for index := range 5_000 {
				tips[fmt.Sprintf("0%05d", index)+worst(300)] = strings.Repeat("c", 40)
				tips[fmt.Sprintf("b%05d", index)+worst(40)] = strings.Repeat("c", 40)
			}

			return &tips
		},
		check: func(t *testing.T, fitted slots.Fitted) {
			held := *fitted.(*branchTips)
			if len(held) != branchTipCount {
				t.Errorf("the tips past their count should keep their count, kept %d", len(held))
			}

			encodedWithin(t, "branch", slices.Collect(maps.Keys(held)), branchBytes)
		},
	},
	{kind: "brief", key: "brief/s-abcdef", value: func() slots.Fitted { return fixedMark(briefMark{At: fullTime}) }},
	{
		kind: "landed-scan", key: "landed-scan/" + strings.Repeat("f", 64),
		value: func() slots.Fitted {
			return fixedMark(scanMark{At: fullTime, Head: strings.Repeat("c", 40), Date: fullTime})
		},
	},
	{kind: "fork", key: "fork/" + strings.Repeat("s", 128), value: func() slots.Fitted { return fixedMark(true) }},
	{
		kind: "migration-failed", key: "migration-failed/" + strings.Repeat("s", 128),
		value: func() slots.Fitted { return fixedMark(true) },
	},
	{
		kind: "waiter", key: "waiter/" + strings.Repeat("s", 128),
		value: func() slots.Fitted { return fixedMark(fmt.Sprintf("%d %d", 1<<62, 1<<62)) },
	},
}

func checkItems(t *testing.T, items []doNowItem) {
	t.Helper()

	if len(items) == 0 {
		t.Fatalf("items past the ceiling should keep some")
	}

	for _, item := range items {
		if item.ID == "m-bad-trigger" || strings.Contains(item.ID, "<") {
			t.Errorf("an item whose id or trigger passes its cap should be left out")
		}

		if item.Title == "" || slots.EncodedLen(item.Title) > itemTitleBytes {
			t.Errorf("an item's title should be cut to its bytes")
		}

		if len(item.Proof) != proofTriggers {
			t.Errorf("an item should keep its first proof triggers within their cap, kept %d", len(item.Proof))
		}

		encodedWithin(t, "proof trigger", item.Proof, proofBytes)
	}
}

// TestEveryKindStoresItsWorstCase builds each kind's worst case, fits it and
// stores it: no value a hook builds is ever refused by the slot file.
func TestEveryKindStoresItsWorstCase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := slots.Open(root, filepath.Join(root, "sessions.slots"))

	for _, tc := range worstCases {
		t.Run(tc.kind, func(t *testing.T) {
			value := tc.value()
			if value.Ceiling() > slots.MaxValue {
				t.Fatalf("a kind's ceiling should stay within what a slot holds, %d is past it", value.Ceiling())
			}

			data, err := slots.Encode(value)
			if err != nil {
				t.Fatalf("the worst case should fit its kind's ceiling: %v", err)
			}

			if len(data) > value.Ceiling() || !json.Valid(data) {
				t.Errorf("a fitted value should stay JSON within its ceiling, %d bytes", len(data))
			}

			if tc.check != nil {
				tc.check(t, value)
			}

			err = file.Put(ctx, tc.key, data)
			if err != nil {
				t.Fatalf("the slot file should store a fitted worst case: %v", err)
			}

			held, found, err := file.Get(ctx, tc.key)
			if err != nil || !found || !bytes.Equal(held, data) {
				t.Errorf("the stored worst case should read back as written, found %v %v", found, err)
			}
		})
	}
}

// TestAFullPartOfNamesIsStored fills one part of a month's table of names,
// every name a graph id's: the part is stored whole.
func TestAFullPartOfNamesIsStored(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := slots.Open(root, filepath.Join(root, "sessions.slots"))

	var part []byte
	for index := range namesPerPart {
		part = append(part, fmt.Sprintf("30-%06d-s-tac-%03d\n", index, index%1000)...)
	}

	err := file.Put(ctx, namesKey("2026/11", 9_999), part)
	if err != nil {
		t.Fatalf("a full part of names should be stored: %v", err)
	}
}

// TestAMarkOfAFullPartKeepsItsGraph builds the graph mark of a session that
// has seen a part's every name in the longest directory kept: it fits whole,
// without falling back to the no-graph mark.
func TestAMarkOfAFullPartKeepsItsGraph(t *testing.T) {
	dir := strings.Repeat("d", pathBytes-2)
	mark := graphMark{
		Month: "2026/11", Dir: dir, Stamp: fmt.Sprintf("%d@%d", int64(1<<62), int64(1<<62)),
		Seen: bytes.Repeat([]byte{0xff}, namesPerPart/8),
	}

	_, err := slots.Encode(&mark)
	if err != nil || mark.Dir != dir || len(mark.Seen) != namesPerPart/8 {
		t.Errorf("a mark over one full part should keep its directory and its bits: %v", err)
	}
}

// TestAValueWithinItsCapsIsKeptWhole fits values at their caps: nothing is
// cut or dropped.
func TestAValueWithinItsCapsIsKeptWhole(t *testing.T) {
	cases := []struct {
		name  string
		value func() slots.Fitted
	}{
		{"a ladder with every field at its cap", func() slots.Fitted {
			return &ladder{
				Nudges: 1 << 62, NudgedAt: fullTime, ReplayNudgedFor: fullTime, Wrote: worstList(writtenFiles, 20),
				LastPrompt: strings.Repeat("a", promptBytes-2), Owed: strings.Repeat("o", owedBytes-2),
			}
		}},
		{"a shown set at its ceiling", func() slots.Fitted {
			return &shownSet{IDs: worstList(1_000, 3)}
		}},
		{"a landed set at its count", func() slots.Fitted {
			held := landedSet{}
			for index := range landedMappings {
				held.Mappings = append(held.Mappings, landedMapping{
					Target: fmt.Sprintf("m-%06d", index), Commit: strings.Repeat("c", 40),
					Subject: strings.Repeat("s", 72), Test: "path internal/memory/landed.go", Score: 0.75,
				})
			}

			return &held
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.value())

			after, err := slots.Encode(tc.value())
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("a value within its caps should be kept as it is: %v", err)
			}
		})
	}
}

// TestTheScanStandsAtTheLastCommitWhoseMappingsWereKept moves a round's scan
// mark by how many of its commits' mappings it kept.
func TestTheScanStandsAtTheLastCommitWhoseMappingsWereKept(t *testing.T) {
	commits := []landedCommit{{sha: "a", at: fullTime}, {sha: "b", at: fullTime.Add(time.Hour)}, {sha: "c"}}
	reached := scanMark{Head: "head"}
	mark := scanMark{Head: "mark"}

	cases := []struct {
		name   string
		stored int
		want   string
	}{
		{"every commit's mappings kept", 2, "head"},
		{"some commits' mappings kept", 1, "b"},
		{"no commit's mappings kept", -1, "mark"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reachedAfter(tc.stored, commits, reached, mark); got.Head != tc.want {
				t.Errorf("the scan should stand after the last commit whose mappings were kept, stands at %s", got.Head)
			}
		})
	}
}

// TestALandedRoundMeetingTheCapLeavesTheRestForTheNext stores the mappings of
// a round whose commits together pass the count of mappings kept: the set
// takes the commits up to the cap, and the round answers the last of them,
// where the next round carries on.
func TestALandedRoundMeetingTheCapLeavesTheRestForTheNext(t *testing.T) {
	mem, _ := captureStore(t)
	folded := &state{entries: map[string]*entry{}}

	commits := make([]landedCommit, 4)
	for index := range commits {
		commits[index] = landedCommit{sha: fmt.Sprintf("%040d", index)}
	}

	var found []landedMapping

	total := landedMappings + landedMappings/2
	for index := range total {
		id := fmt.Sprintf("m-%06d", index)
		folded.entries[id] = &entry{id: id, status: statusLive, kind: kindTodo}
		found = append(found, landedMapping{Target: id, Commit: commits[index*len(commits)/total].sha})
	}

	stored := mem.storeMappings(folded, found, commits)

	var held landedSet
	mem.readSlot(landedKey, &held)

	if stored != 1 || len(held.Mappings) != total/2 {
		t.Errorf("a round should keep the commits whose mappings fit, and stop at the last of them: "+
			"stopped at %d, kept %d mappings", stored, len(held.Mappings))
	}

	for _, mapping := range held.Mappings {
		delete(folded.entries, mapping.Target)
	}

	rest := commits[stored+1:]
	if again := mem.storeMappings(folded, found[len(held.Mappings):], rest); again != len(rest)-1 {
		t.Errorf("the next round should carry on from where the cap stopped the last, stopped at %d", again)
	}
}
