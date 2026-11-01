package collapse_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/collapse"
)

type viewEntry struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Kind         string `json:"kind"`
	Summary      string `json:"summary"`
	Body         string `json:"body"`
	ShownEarlier bool   `json:"shown_earlier"`
}

type viewed struct {
	Note     string          `json:"shown_earlier_note"`
	Items    json.RawMessage `json:"items"`
	Sections []struct {
		Render  string `json:"render"`
		Matched int    `json:"matched"`
		List    *struct {
			Items []struct {
				Entry       viewEntry         `json:"entry"`
				Score       float64           `json:"score"`
				Refs        []json.RawMessage `json:"refs"`
				RefsDropped int               `json:"refs_dropped"`
			} `json:"items"`
			Ranked bool `json:"ranked"`
		} `json:"list"`
		Grouped *struct {
			Field  string `json:"field"`
			Groups []struct {
				Key     string      `json:"key"`
				Entries []viewEntry `json:"entries"`
			} `json:"groups"`
		} `json:"grouped"`
		Counts *struct {
			Rows []struct {
				Label string  `json:"label"`
				Count int     `json:"count"`
				Heat  float64 `json:"heat"`
			} `json:"rows"`
		} `json:"counts"`
		Focus *struct {
			Focuses []struct {
				Focus   viewEntry       `json:"focus"`
				Actors  []string        `json:"actors"`
				When    json.RawMessage `json:"when"`
				Targets []struct {
					Target viewEntry `json:"target"`
					Score  float64   `json:"score"`
					State  string    `json:"state"`
					Actors []string  `json:"actors"`
				} `json:"targets"`
			} `json:"focuses"`
			Pull string `json:"pull"`
		} `json:"focus"`
		Participants *struct {
			Groups []struct {
				Actor viewEntry `json:"actor"`
				Roles []string  `json:"roles"`
			} `json:"groups"`
		} `json:"participants"`
		Wip *struct {
			Markers []struct {
				ID      string    `json:"id"`
				Entry   viewEntry `json:"entry"`
				Content string    `json:"content"`
				Branch  string    `json:"branch"`
			} `json:"markers"`
		} `json:"wip"`
		Bodies *struct {
			Entries []viewEntry `json:"entries"`
			Pull    string      `json:"pull"`
		} `json:"bodies"`
	} `json:"view_sections"`
}

func decodeView(t *testing.T, raw json.RawMessage) viewed {
	t.Helper()

	var got viewed

	err := json.Unmarshal(raw, &got)
	if err != nil {
		t.Fatalf("decoding the collapsed view: %v", err)
	}

	shapes := make([]string, 0, len(got.Sections))
	for _, section := range got.Sections {
		shapes = append(shapes, section.Render)
	}

	if strings.Join(shapes, " ") != "list grouped counts focus participants wip bodies" {
		t.Fatalf("a collapsed view should keep every section in its order, got %v", shapes)
	}

	return got
}

func pointer(t *testing.T, where string, entry viewEntry) {
	t.Helper()

	if !entry.ShownEarlier || entry.ID == "" || entry.Title == "" || entry.Kind != "" || entry.Summary != "" {
		t.Errorf("%s: an entry shown unchanged should collapse to its id and title, got %+v", where, entry)
	}
}

func whole(t *testing.T, where string, entry viewEntry) {
	t.Helper()

	if entry.ShownEarlier || entry.ID == "" || entry.Kind == "" || entry.Summary == "" {
		t.Errorf("%s: an entry should print in full, got %+v", where, entry)
	}
}

func TestAFirstViewPrintsWholeAndMarksItsEntries(t *testing.T) {
	var held collapse.Set

	raw, collapsed := collapse.Answer("view", fixture(t, "view.json"), &held)
	if collapsed {
		t.Errorf("a view nothing of which was shown before should print as sdd gave it, got %s", raw)
	}

	for _, id := range []string{
		"20261029-021515-d-tac-00q", "20261101-175343-s-tac-n4e", "20261027-222117-d-tac-yjy",
		"20261031-235540-d-tac-wxh", "20261101-201929-s-tac-wl4", "20261101-151135-d-tac-uom",
		"20261001-090000-s-stg-fza", "20261031-122251-s-tac-jgs",
	} {
		t.Run(id, func(t *testing.T) {
			if held.Lines[id] == "" {
				t.Error("an entry a view printed in full should be marked as shown")
			}
		})
	}

	if held.Lines["20261101-180000-s-ops-wpm"] != "" {
		t.Error("a wip marker's own id names no entry, so it should never be marked")
	}
}

func TestAViewIsNeverReadAsAShow(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	_, collapsed := collapse.Answer("show", fixture(t, "view.json"), &held)
	if collapsed {
		t.Error("a view section's list items should never be read as a show's items")
	}

	_, collapsed = collapse.Answer("show", fixture(t, "show.json"), &held)
	if !collapsed {
		t.Error("a show should still collapse the chains it repeats")
	}
}

func TestAViewCollapsesEntriesShownEarlier(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	raw, collapsed := collapse.Answer("view", fixture(t, "view.json"), &held)
	if !collapsed {
		t.Fatal("a view repeating entries a show printed should collapse them")
	}

	got := decodeView(t, raw)

	if !strings.Contains(got.Note, "show an entry by its id") {
		t.Errorf("a collapsed view should say how the full text comes back, got %q", got.Note)
	}

	if got.Items != nil {
		t.Error("a collapsed view should gain no show items")
	}

	if got.Sections[0].Matched == 0 {
		t.Error("a section's matched count should stay")
	}

	list := got.Sections[0].List
	pointer(t, "list", list.Items[0].Entry)
	whole(t, "list", list.Items[1].Entry)

	if list.Items[0].Score == 0 || len(list.Items[0].Refs) != 2 || list.Items[0].RefsDropped == 0 || !list.Ranked {
		t.Errorf("a list item's score and refs should stay beside its pointer, got %+v", list.Items[0])
	}

	grouped := got.Sections[1].Grouped
	pointer(t, "grouped", grouped.Groups[0].Entries[0])
	pointer(t, "grouped", grouped.Groups[0].Entries[1])
	whole(t, "grouped", grouped.Groups[1].Entries[0])

	if grouped.Field == "" || grouped.Groups[1].Key == "" {
		t.Errorf("a grouped view should keep its field and keys, got %+v", grouped)
	}

	counts := got.Sections[2].Counts
	if len(counts.Rows) != 2 || counts.Rows[0].Label == "" || counts.Rows[0].Count == 0 || counts.Rows[0].Heat == 0 {
		t.Errorf("a counts view should never change, got %+v", counts)
	}

	focus := got.Sections[3].Focus.Focuses[0]
	pointer(t, "focus", focus.Focus)
	pointer(t, "focus target", focus.Targets[0].Target)

	if len(focus.Actors) == 0 || focus.When == nil || focus.Targets[0].Score == 0 || focus.Targets[0].State == "" ||
		len(focus.Targets[0].Actors) == 0 || got.Sections[3].Focus.Pull == "" {
		t.Errorf("a focus's actors, time, score and state should stay beside its pointers, got %+v", focus)
	}

	actor := got.Sections[4].Participants.Groups[0]
	whole(t, "participants", actor.Actor)

	if len(actor.Roles) == 0 {
		t.Error("a participant's roles should stay")
	}

	marker := got.Sections[5].Wip.Markers[0]
	pointer(t, "wip", marker.Entry)

	if marker.ID == "" || marker.Content == "" || marker.Branch == "" {
		t.Errorf("a wip marker's own fields should stay beside its pointer, got %+v", marker)
	}
}

func TestBodiesPrintWholeAndMarkTheirLines(t *testing.T) {
	var held collapse.Set

	collapse.Answer("view", fixture(t, "view.json"), &held)

	raw, collapsed := collapse.Answer("view", fixture(t, "view.json"), &held)
	if !collapsed {
		t.Fatal("a repeated view should collapse what the first printed")
	}

	got := decodeView(t, raw)

	body := got.Sections[6].Bodies.Entries[0]
	whole(t, "bodies", body)

	if body.Body == "" {
		t.Error("a bodies view asks for bodies, so they should always print in full")
	}

	pointer(t, "list", got.Sections[0].List.Items[1].Entry)
	pointer(t, "participants", got.Sections[4].Participants.Groups[0].Actor)

	raw, collapsed = collapse.Answer("search", fixture(t, "search.json"), &held)
	if !collapsed {
		t.Fatal("a hit a view printed in full should collapse on a later search")
	}

	hits := decode(t, raw).Hits
	if !hits[0].ShownEarlier || !hits[1].ShownEarlier {
		t.Errorf("lines a view's list and bodies printed should collapse later hits, got %+v", hits)
	}
}

func TestAChangedViewEntryPrintsInFull(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	changed := strings.ReplaceAll(string(fixture(t, "view.json")), `"status": "closed-by 20261101-010124-s-tac-ng6"`,
		`"status": "superseded-by 20261102-010124-d-tac-new"`)

	raw, _ := collapse.Answer("view", json.RawMessage(changed), &held)
	got := decodeView(t, raw)

	whole(t, "grouped", got.Sections[1].Grouped.Groups[0].Entries[1])
	whole(t, "wip", got.Sections[5].Wip.Markers[0].Entry)
	pointer(t, "grouped", got.Sections[1].Grouped.Groups[0].Entries[0])
}

func TestAViewInTextFormPassesThrough(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	before := len(held.Lines)

	for range 2 {
		_, collapsed := collapse.Answer("view", fixture(t, "view-text.json"), &held)
		if collapsed {
			t.Error("a view in sdd's text form should never be changed")
		}
	}

	if len(held.Lines) != before {
		t.Error("a view in sdd's text form should mark nothing")
	}
}
