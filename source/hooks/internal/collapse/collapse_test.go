package collapse_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/collapse"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type chainItem struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Status       string   `json:"status"`
	Desc         string   `json:"desc"`
	Ref          string   `json:"ref"`
	Relation     string   `json:"relation"`
	ShownEarlier bool     `json:"shown_earlier"`
	Seen         bool     `json:"seen_in_response"`
	Depth        int      `json:"depth"`
	Truncated    []string `json:"truncated"`
}

type shown struct {
	Note  string `json:"shown_earlier_note"`
	Items []struct {
		ID         string      `json:"id"`
		Body       string      `json:"body"`
		Summary    string      `json:"summary"`
		Upstream   []chainItem `json:"upstream"`
		Downstream []chainItem `json:"downstream"`
	} `json:"items"`
	Hits []struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Summary      string  `json:"summary"`
		Score        float64 `json:"score"`
		ShownEarlier bool    `json:"shown_earlier"`
	} `json:"hits"`
	Lanes []struct {
		Name         string `json:"name"`
		Text         string `json:"text"`
		ShownEarlier bool   `json:"shown_earlier"`
	} `json:"lanes"`
	Blocks []struct {
		Name         string `json:"name"`
		Text         string `json:"text"`
		ShownEarlier bool   `json:"shown_earlier"`
	} `json:"framing_blocks"`
	Schema   map[string]any `json:"report_schema"`
	Junction struct {
		Lanes []struct {
			Name         string `json:"name"`
			Text         string `json:"text"`
			ShownEarlier bool   `json:"shown_earlier"`
		} `json:"lanes"`
	} `json:"base_junction"`
}

func decode(t *testing.T, raw json.RawMessage) shown {
	t.Helper()

	var got shown

	err := json.Unmarshal(raw, &got)
	if err != nil {
		t.Fatalf("decoding the collapsed answer: %v", err)
	}

	return got
}

func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()

	return json.RawMessage(test.Fixture(t, "collapse/"+name))
}

func TestAFirstAnswerPrintsInFull(t *testing.T) {
	for _, tool := range []string{"show", "search", "next"} {
		t.Run(tool, func(t *testing.T) {
			var held collapse.Set

			name := map[string]string{"show": "show.json", "search": "search.json", "next": "next.json"}[tool]

			_, collapsed := collapse.Answer(tool, fixture(t, name), &held)
			if collapsed {
				t.Error("an answer nothing of which was shown before should print as sdd gave it")
			}

			if held.Empty() {
				t.Error("an answer printed in full should mark what it printed")
			}
		})
	}
}

func TestASecondShowCollapsesTheChainsNotTheAskedEntry(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	raw, collapsed := collapse.Answer("show", fixture(t, "show.json"), &held)
	if !collapsed {
		t.Fatal("a show repeating chain entries the agent holds should collapse them")
	}

	got := decode(t, raw)
	item := got.Items[0]

	if item.Body == "" || item.Summary == "" {
		t.Error("the entry a show asks for should always print in full")
	}

	upstream := item.Upstream[0]
	if !upstream.ShownEarlier || upstream.Summary != "" || upstream.Status != "" {
		t.Errorf("a chain entry shown unchanged should collapse to a pointer, got %+v", upstream)
	}

	if upstream.ID == "" || upstream.Title == "" || upstream.Desc == "" || upstream.Ref == "" || upstream.Relation == "" {
		t.Errorf("a pointer should keep the edge, id, title and the reason for the ref, got %+v", upstream)
	}

	below := item.Upstream[1]
	if upstream.Depth != 1 || below.Depth != 2 || !below.ShownEarlier {
		t.Errorf("pointers should keep their depth, so the list keeps its tree, got %+v", item.Upstream)
	}

	if len(item.Downstream[0].Truncated) == 0 {
		t.Error("a pointer should keep the ids its chain truncated")
	}

	seen := item.Downstream[1]
	if seen.ShownEarlier || seen.Summary == "" || !seen.Seen {
		t.Errorf("an item sdd already marks as printed in the answer should stay as sdd gave it, got %+v", seen)
	}

	if !strings.Contains(got.Note, "show an entry by its id") {
		t.Errorf("a collapsed answer should say how the full text comes back, got %q", got.Note)
	}
}

func TestAChangedLinePrintsInFull(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	changed := strings.Replace(string(fixture(t, "show.json")), `"status": "closed-by 20261101-010124-s-tac-ng6"`,
		`"status": "superseded-by 20261102-010124-d-tac-new"`, 1)

	raw, _ := collapse.Answer("show", json.RawMessage(changed), &held)
	got := decode(t, raw)

	changedItem := got.Items[0].Downstream[0]
	if changedItem.ShownEarlier || changedItem.Summary == "" {
		t.Errorf("a chain entry whose status changed should print in full, got %+v", changedItem)
	}

	if !got.Items[0].Upstream[0].ShownEarlier {
		t.Error("an unchanged chain entry beside it should still collapse")
	}
}

func TestAnEntryTwiceInOneAnswerPrintsTwice(t *testing.T) {
	var held collapse.Set

	twice := strings.Replace(string(fixture(t, "show.json")), `"seen_in_response": true`, `"seen_in_response": false`, 1)

	_, collapsed := collapse.Answer("show", json.RawMessage(twice), &held)
	if collapsed {
		t.Error("an entry should never collapse against the answer printing it the first time")
	}
}

func TestSearchHitsCollapseOnLinesShownAnywhere(t *testing.T) {
	var held collapse.Set

	collapse.Answer("show", fixture(t, "show.json"), &held)

	raw, collapsed := collapse.Answer("search", fixture(t, "search.json"), &held)
	if !collapsed {
		t.Fatal("a hit whose line a show printed should collapse")
	}

	got := decode(t, raw)

	first, second := got.Hits[0], got.Hits[1]
	if !first.ShownEarlier || first.Summary != "" || first.Title == "" || first.Score == 0 {
		t.Errorf("a hit shown before should keep id, title and score only, got %+v", first)
	}

	if second.ShownEarlier || second.Summary == "" {
		t.Errorf("a hit never shown should print in full, got %+v", second)
	}

	raw, _ = collapse.Answer("search", fixture(t, "search.json"), &held)
	if !decode(t, raw).Hits[1].ShownEarlier {
		t.Error("a hit printed by the earlier search should collapse on the next")
	}
}

func TestServesCollapsePiecesServedByteForByte(t *testing.T) {
	var held collapse.Set

	collapse.Answer("next", fixture(t, "next.json"), &held)

	changed := strings.Replace(string(fixture(t, "next.json")), "Each ref names why the pointer exists.",
		"Each ref names why it exists.", 1)

	raw, collapsed := collapse.Answer("next", json.RawMessage(changed), &held)
	if !collapsed {
		t.Fatal("a serve repeating pieces the agent holds should collapse them")
	}

	got := decode(t, raw)

	if !got.Lanes[0].ShownEarlier || got.Lanes[0].Text != "" || got.Lanes[0].Name != "intro" {
		t.Errorf("a lane served unchanged should collapse to its name, got %+v", got.Lanes[0])
	}

	if got.Lanes[1].ShownEarlier || got.Lanes[1].Text == "" {
		t.Errorf("a lane whose text changed should print in full, got %+v", got.Lanes[1])
	}

	if !got.Blocks[0].ShownEarlier || !got.Blocks[1].ShownEarlier {
		t.Errorf("framing blocks served unchanged should collapse, got %+v", got.Blocks)
	}

	if got.Schema["shown_earlier"] != true || got.Schema["properties"] != nil {
		t.Errorf("a report schema served unchanged should collapse, got %+v", got.Schema)
	}

	if !got.Junction.Lanes[0].ShownEarlier {
		t.Error("the serves an answer carries should collapse as well")
	}
}

func TestAServeCarriedAloneCollapses(t *testing.T) {
	var held collapse.Set

	collapse.Answer("abandon", fixture(t, "abandon.json"), &held)

	raw, collapsed := collapse.Answer("abandon", fixture(t, "abandon.json"), &held)
	if !collapsed {
		t.Fatal("an answer carrying only the junction's serve should collapse it too")
	}

	got := decode(t, raw)
	if !got.Junction.Lanes[0].ShownEarlier {
		t.Errorf("the junction's lane served unchanged should collapse, got %+v", got.Junction.Lanes)
	}
}

func TestServesListedAsOpenInstancesCollapse(t *testing.T) {
	var held collapse.Set

	listed := json.RawMessage(`{"session":"s_1","open_instances":[` + string(fixture(t, "abandon.json")) + `]}`)

	collapse.Answer("start_session", listed, &held)

	_, collapsed := collapse.Answer("start_session", listed, &held)
	if !collapsed {
		t.Error("serves an answer lists under open_instances should collapse too")
	}
}

func TestAResumePrintsEverything(t *testing.T) {
	var held collapse.Set

	collapse.Answer("next", fixture(t, "next.json"), &held)

	_, collapsed := collapse.Answer("resume_session", fixture(t, "next.json"), &held)
	if collapsed {
		t.Error("resume_session asks for the full position, so nothing in it should collapse")
	}
}

func TestAnAnswerWithoutStructurePassesThrough(t *testing.T) {
	var held collapse.Set

	for range 2 {
		_, collapsed := collapse.Answer("show", fixture(t, "show-text.json"), &held)
		if collapsed {
			t.Error("an answer in sdd's text form should never be changed")
		}
	}

	if !held.Empty() {
		t.Error("an answer in sdd's text form should mark nothing")
	}
}

func TestEveryAnswerFormIsRead(t *testing.T) {
	answer := string(fixture(t, "search.json"))

	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("encoding the answer: %v", err)
	}

	forms := map[string]string{
		"object":     answer,
		"string":     string(encoded),
		"structured": `{"structuredContent":` + answer + `}`,
		"content":    `{"content":[{"type":"text","text":` + string(encoded) + `}]}`,
		"blocks":     `[{"type":"text","text":` + string(encoded) + `}]`,
	}

	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			var held collapse.Set

			collapse.Answer("search", json.RawMessage(form), &held)

			_, collapsed := collapse.Answer("search", json.RawMessage(form), &held)
			if !collapsed {
				t.Error("a repeated search should collapse in whatever form the answer arrives")
			}
		})
	}
}
