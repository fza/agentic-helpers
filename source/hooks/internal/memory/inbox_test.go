package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// inboxStore is two seats, idea having looked once so its seen mark stands,
// then main filing one entry for every seat, one for idea's shelf, one under
// a topic idea's shelf follows, one for no one, and idea one of its own.
func inboxStore(t *testing.T) (*harness, *harness, []string) {
	t.Helper()

	h, other := twoSeats(t)
	other.must("", "follow", "deploy")
	other.must(hookPayload("", "", ""), "tool")
	h.clock.Advance(time.Minute)

	addressed := []string{
		h.file("Staging DB moved to db2", "a changed fact", "--for", "all"),
		h.file("Review owes the API diff", "a handover", "--for", "idea"),
		h.file("Deploy runs at noon daily", "a schedule", "--topic", "deploy"),
	}

	h.file("Main keeps this one to itself", "no one else's")
	other.file("Idea files its own finding", "its own shelf")

	h.must("", "release")
	h.must("", "claim", "idea-scout", "--shelf", "idea")
	addressed = append(addressed, h.file("Scout found the flaky lock test", "a shelf mate's finding"))
	h.must("", "release")
	h.must("", "claim", "main")

	return h, other, addressed
}

func viewIDs(t *testing.T, h *harness, text string) []string {
	t.Helper()

	var answer viewAnswer

	err := json.Unmarshal([]byte(h.must("", "view", "--layout", text, "--json")), &answer)
	if err != nil {
		t.Fatalf("view --json should answer one object: %v", err)
	}

	ids := []string{}

	for _, section := range answer.Sections {
		for _, listed := range section.Entries {
			ids = append(ids, listed.ID)
		}
	}

	slices.Sort(ids)

	return ids
}

func TestInboxAgreesWithTheArrivalNotice(t *testing.T) {
	h, other, addressed := inboxStore(t)

	cache := filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID))
	marks, _ := filepath.Glob(filepath.Join(cache, "briefs", "*.seen.json"))
	heat, _ := os.ReadFile(filepath.Join(cache, "heat.json"))

	before := map[string]string{}
	for _, mark := range marks {
		raw, _ := os.ReadFile(mark)
		before[mark] = string(raw)
	}

	unread := viewIDs(t, h, "active:inbox(idea):unread:as-list")
	inbox := viewIDs(t, h, "inbox(idea):as-list")

	for mark, raw := range before {
		now, _ := os.ReadFile(mark)
		if string(now) != raw {
			t.Error("a view should leave the seen mark where it was")
		}
	}

	after, _ := os.ReadFile(filepath.Join(cache, "heat.json"))
	if string(after) != string(heat) {
		t.Error("a view should leave heat.json as it was")
	}

	told := idPattern.FindAllString(other.must(hookPayload("", "", ""), "tool"), -1)
	slices.Sort(told)

	want := slices.Sorted(slices.Values(addressed))

	if !slices.Equal(unread, want) || !slices.Equal(told, want) {
		t.Errorf("inbox:unread and the arrival notice should name the same entries: for all, for the shelf and "+
			"under a followed topic, never the own shelf's, got view %q and notice %q", unread, told)
	}

	if !slices.Equal(inbox, want) {
		t.Errorf("inbox should hold every entry addressed to the seat, got %q", inbox)
	}

	if got := viewIDs(t, h, "active:inbox(idea):unread:as-list"); len(got) != 0 {
		t.Errorf("once the notice moved the mark, nothing should read as unread, got %q", got)
	}
}

func TestUnreadWithoutAMarkIsEmpty(t *testing.T) {
	h, other := twoSeats(t)
	h.file("Staging DB moved to db2", "a changed fact", "--for", "all")

	if got := viewIDs(t, other, "inbox(idea):unread:as-list"); len(got) != 0 {
		t.Errorf("a seat without a seen mark has seen everything filed so far, got %q", got)
	}

	if got := viewIDs(t, other, "inbox(idea):as-list"); len(got) != 1 {
		t.Errorf("inbox alone should hold what was filed for every seat, got %q", got)
	}
}

func TestInboxAndUnreadRefuse(t *testing.T) {
	h, _, _ := inboxStore(t)

	cases := []struct {
		layout string
		want   string
	}{
		{layout: "unread:as-list", want: `layout: unread needs inbox(<seat>) in its section: "unread" at position 1; nearest valid word: inbox`},
		{layout: "inbox(idae):as-list", want: `layout: inbox takes a seat of this project: "idae" at position 7; nearest valid word: idea`},
		{layout: "inbox(idea):as-list,unread:as-list", want: `layout: unread needs inbox(<seat>) in its section: "unread" at position 21; nearest valid word: inbox`},
	}

	for _, tc := range cases {
		t.Run(tc.layout, func(t *testing.T) {
			got := h.run("", "view", "--layout", tc.layout)
			if got.code != 1 || !strings.HasPrefix(got.err, "agentic-memory: "+tc.want) {
				t.Errorf("the layout should be refused naming token, position and nearest word, got %d %q", got.code, got.err)
			}
		})
	}
}
