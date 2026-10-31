package memory

import (
	"encoding/json"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

func TestTheScratchMapperMapsEachOldPathByRule(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{Seats: map[string]seat.Entry{
		"s-aaaa": {Name: "main"}, "s-bbbb": {Name: "drive", Expired: true},
	}})

	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"a seat folder", "see .tmp/claude/main/state-nudges.sh", "see .tmp/seats/main/state-nudges.sh"},
		{"an expired seat's folder", "`.tmp/claude/drive/x`", "`.tmp/seats/drive/x`"},
		{"a seat folder ending a sentence", "it lives in .tmp/claude/main.", "it lives in .tmp/seats/main."},
		{"a session folder", ".tmp/claude/0b1c2d3e-4f50-4617-8899-aabbccddeeff/probe.txt",
			".tmp/seats/0b1c2d3e-4f50-4617-8899-aabbccddeeff/probe.txt"},
		{"an upper-case session id is no session", ".tmp/claude/0B1C2D3E-4F50-4617-8899-AABBCCDDEEFF/x",
			".tmp/keep/0B1C2D3E-4F50-4617-8899-AABBCCDDEEFF/x"},
		{"any other folder", "(.tmp/claude/todos/list.md)", "(.tmp/keep/todos/list.md)"},
		{"a file straight below", ".tmp/claude/main-notes.md", ".tmp/keep/main-notes.md"},
		{"an absolute path", "/repo/.tmp/claude/main/x", "/repo/.tmp/seats/main/x"},
		{"every path of a text", ".tmp/claude/main/a and .tmp/claude/probe/b", ".tmp/seats/main/a and .tmp/keep/probe/b"},
		{"the bare old folder", "scratch lived under .tmp/claude/ before", "scratch lived under .tmp/claude/ before"},
		{"a placeholder", ".tmp/claude/<seat>/", ".tmp/claude/<seat>/"},
		{"a variable", ".tmp/claude/$SESSION/x", ".tmp/claude/$SESSION/x"},
		{"a glob", ".tmp/claude/*/", ".tmp/claude/*/"},
		{"a path already moved", ".tmp/seats/main/x and .tmp/keep/y", ".tmp/seats/main/x and .tmp/keep/y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapper.rewrite(tc.text); got != tc.want {
				t.Errorf("the old scratch path should map by its rule, got %q", got)
			}
		})
	}
}

func TestTheScratchMapperMapsEndsAndTriggers(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{Seats: map[string]seat.Entry{"s-aaaa": {Name: "main"}}})

	for _, tc := range []struct {
		name   string
		mapped func(string) string
		text   string
		want   string
	}{
		{"a gone end", mapper.end, "gone:.tmp/claude/main/x", "gone:.tmp/seats/main/x"},
		{"a changed end", mapper.end, "changed:.tmp/claude/todos/list.md", "changed:.tmp/keep/todos/list.md"},
		{"an end naming no path", mapper.end, "merged:.tmp/claude/main", "merged:.tmp/claude/main"},
		{"an end without a form", mapper.end, ".tmp/claude/main/x", ".tmp/claude/main/x"},
		{"a bare end form", mapper.end, "gone", "gone"},
		{"a glob below a seat folder", mapper.trigger, "path:.tmp/claude/main/**", "path:.tmp/seats/main/**"},
		{"a glob naming a folder", mapper.trigger, "path:.tmp/claude/todos", "path:.tmp/keep/todos"},
		{"a glob before the name", mapper.trigger, "path:.tmp/claude/*", "path:.tmp/claude/*"},
		{"a glob within the name", mapper.trigger, "path:.tmp/claude/ma*/x", "path:.tmp/claude/ma*/x"},
		{"a glob alternative within the name", mapper.trigger, "path:.tmp/claude/main{,2}/x", "path:.tmp/claude/main{,2}/x"},
		{"a command trigger", mapper.trigger, "command:cat .tmp/claude/main/x", "command:cat .tmp/claude/main/x"},
		{"a trigger without a form", mapper.trigger, ".tmp/claude/main/x", ".tmp/claude/main/x"},
		{"a bare trigger form", mapper.trigger, "path", "path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mapped(tc.text); got != tc.want {
				t.Errorf("only the path of a path end or a literal-named glob should map, got %q", got)
			}
		})
	}
}

func TestAStoredPathMissingItsDotMapsLikeTheDottedOne(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{Seats: map[string]seat.Entry{"s-aaaa": {Name: "main"}}})

	for _, tc := range []struct {
		name   string
		mapped func(string) string
		text   string
		want   string
	}{
		{"a seat folder", mapper.storedPath, "tmp/claude/main/x", ".tmp/seats/main/x"},
		{"any other folder", mapper.storedPath, "tmp/claude/todos/y", ".tmp/keep/todos/y"},
		{"the bare old folder", mapper.storedPath, "tmp/claude/", "tmp/claude/"},
		{"another dotless tmp path", mapper.storedPath, "tmp/other/x", "tmp/other/x"},
		{"a dotless tmp path deeper down", mapper.storedPath, "src/tmp/claude/main/x", "src/tmp/claude/main/x"},
		{"a dotted path", mapper.storedPath, ".tmp/claude/main/x", ".tmp/seats/main/x"},
		{"a gone end", mapper.end, "gone:tmp/claude/main/x", "gone:.tmp/seats/main/x"},
		{"a changed end", mapper.end, "changed:tmp/claude/todos/y", "changed:.tmp/keep/todos/y"},
		{"a trigger keeps its text", mapper.trigger, "path:tmp/claude/main/**", "path:tmp/claude/main/**"},
		{"text keeps its text", mapper.rewrite, "see tmp/claude/main/x", "see tmp/claude/main/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mapped(tc.text); got != tc.want {
				t.Errorf("a stored path under tmp/claude/ alone should map as its dotted form, got %q", got)
			}
		})
	}
}

func TestTheScratchPassRewritesOnlyTheFieldsQuotingPaths(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{Seats: map[string]seat.Entry{"s-aaaa": {Name: "main"}}})

	for _, tc := range []struct {
		name    string
		kind    memorylog.Kind
		payload string
		want    map[string]any
	}{
		{"a new entry's title, body and anchors", memorylog.KindNew,
			`{"title":"t .tmp/claude/main/a","body":"b .tmp/claude/x/y","anchors":[{"path":".tmp/claude/main/a","hash":"h1"}],"from":".tmp/claude/main/z"}`,
			map[string]any{
				"title": "t .tmp/seats/main/a", "body": "b .tmp/keep/x/y", "from": ".tmp/claude/main/z",
				"anchors": []any{map[string]any{"path": ".tmp/seats/main/a", "hash": "h1"}},
			}},
		{"a new entry's path fields", memorylog.KindNew,
			`{"about":[".tmp/claude/main/a"],"until":["gone:.tmp/claude/main/a","changed:.tmp/claude/k"],` +
				`"until_hashes":{".tmp/claude/k":"h"},"when":["path:.tmp/claude/main/**","path:.tmp/claude/*"],` +
				`"proof":["path:.tmp/claude/k/x","command:ls .tmp/claude/main"]}`,
			map[string]any{
				"about": []any{".tmp/seats/main/a"}, "until": []any{"gone:.tmp/seats/main/a", "changed:.tmp/keep/k"},
				"until_hashes": map[string]any{".tmp/keep/k": "h"}, "when": []any{"path:.tmp/seats/main/**", "path:.tmp/claude/*"},
				"proof": []any{"path:.tmp/keep/k/x", "command:ls .tmp/claude/main"},
			}},
		{"a new entry's dotless path fields", memorylog.KindNew,
			`{"title":"tmp/claude/main/a","anchors":[{"path":"tmp/claude/main/a","hash":"h"}],"about":["tmp/claude/k"],` +
				`"until":["gone:tmp/claude/main/a"],"until_hashes":{"tmp/claude/k":"h"},"when":["path:tmp/claude/main/**"]}`,
			map[string]any{
				"title": "tmp/claude/main/a", "anchors": []any{map[string]any{"path": ".tmp/seats/main/a", "hash": "h"}},
				"about": []any{".tmp/keep/k"}, "until": []any{"gone:.tmp/seats/main/a"},
				"until_hashes": map[string]any{".tmp/keep/k": "h"}, "when": []any{"path:tmp/claude/main/**"},
			}},
		{"path fields of another kind stay", memorylog.KindConfirm,
			`{"about":[".tmp/claude/main/a"],"until":["gone:.tmp/claude/main/a"]}`,
			map[string]any{"about": []any{".tmp/claude/main/a"}, "until": []any{"gone:.tmp/claude/main/a"}}},
		{"a retitle", memorylog.KindRetitle, `{"title":".tmp/claude/main/a"}`, map[string]any{"title": ".tmp/seats/main/a"}},
		{"a confirm's anchors", memorylog.KindConfirm, `{"anchors":[{"path":".tmp/claude/k/a","hash":"h"}],"commit":"c"}`,
			map[string]any{"anchors": []any{map[string]any{"path": ".tmp/keep/k/a", "hash": "h"}}, "commit": "c"}},
		{"a todo item's text", memorylog.KindItem, `{"op":"add","id":"1","text":"run .tmp/claude/main/x.sh"}`,
			map[string]any{"op": "add", "id": "1", "text": "run .tmp/seats/main/x.sh"}},
		{"a seat's state", memorylog.KindState, `{"body":"next: .tmp/claude/main/plan.md"}`,
			map[string]any{"body": "next: .tmp/seats/main/plan.md"}},
		{"a todo item's change carrying no text", memorylog.KindItem, `{"op":"state","id":"1","state":"done"}`,
			map[string]any{"op": "state", "id": "1", "state": "done"}},
		{"a confirm carrying no anchors", memorylog.KindConfirm, `{"commit":"c"}`, map[string]any{"commit": "c"}},
		{"a retire's reason stays", memorylog.KindRetire, `{"because":".tmp/claude/main/x"}`,
			map[string]any{"because": ".tmp/claude/main/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mapper.payload(tc.kind, []byte(tc.payload))
			if err != nil {
				t.Fatalf("the payload should be rewritten: %v", err)
			}

			var read map[string]any

			err = json.Unmarshal(got, &read)
			if err != nil {
				t.Fatalf("the rewritten payload should be JSON: %v", err)
			}

			wanted, _ := json.Marshal(tc.want)
			held, _ := json.Marshal(read)

			if string(held) != string(wanted) {
				t.Errorf("only the fields quoting a path should change, got %s", held)
			}
		})
	}
}

func TestTheScratchPassSkipsEventsQuotingNoPath(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{})

	events := []memorylog.Event{
		{Kind: memorylog.KindRetire, Entry: "m-aaaa"},
		{Kind: memorylog.KindLink, Entry: "m-aaaa", Target: "m-bbbb"},
	}

	err := mapper.pass(events)
	if err != nil {
		t.Fatalf("an event carrying no payload, as a retire from an earlier release, should pass untouched: %v", err)
	}

	for _, event := range events {
		if event.Payload != nil {
			t.Errorf("an event of a kind quoting no path should keep its payload, got %s", event.Payload)
		}
	}
}

func TestAPayloadNoPathChangesStaysByteForByte(t *testing.T) {
	mapper := newScratchMapper(seat.Registry{})
	payload := []byte(`{"title":"t","body":"<b>",  "anchors":[{"path":"a.go","hash":"h"}]}`)

	got, err := mapper.payload(memorylog.KindNew, payload)
	if err != nil {
		t.Fatalf("the payload should be read: %v", err)
	}

	if string(got) != string(payload) {
		t.Errorf("a payload naming no old scratch path should stay byte for byte, got %s", got)
	}
}
