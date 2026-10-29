package memory_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

// hubKeys are the keys of the hub jsonReadsHarness files, each holding a
// value: a live trap anchored to a file, linking a decision, linked to.
var hubKeys = []string{
	"anchors", "branch", "commit", "filed", "from", "id", "kind", "linked_from", "links", "retention", "seat", "shelf",
	"status", "title", "topics",
}

// noEmptyKey fails where a read answer carries a key holding an empty value.
func noEmptyKey(t *testing.T, text string) {
	t.Helper()

	empty, err := test.EmptyKeys([]byte(text))
	if err != nil || len(empty) > 0 {
		t.Errorf("a read answer should carry no key holding an empty value, got %v %v:\n%s", empty, err, text)
	}
}

func keysOf(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func decoded(t *testing.T, text string) map[string]any {
	t.Helper()

	var object map[string]any

	err := json.Unmarshal([]byte(text), &object)
	if err != nil {
		t.Fatalf("the answer should be one JSON object: %v\n%s", err, text)
	}

	return object
}

// mcpText is the text of the tool call's answer.
func mcpText(t *testing.T, answers string) string {
	t.Helper()

	for _, line := range strings.Split(answers, "\n") {
		var answer struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}

		if json.Unmarshal([]byte(line), &answer) == nil && answer.ID == 2 && len(answer.Result.Content) > 0 {
			return answer.Result.Content[0].Text
		}
	}

	t.Fatalf("the server should answer the call: %s", answers)

	return ""
}

func jsonReadsHarness(t *testing.T) (*harness, string, string) {
	t.Helper()

	h := newHarness(t)
	withGraph(t, h)
	h.must("", "claim", "main")

	hub := h.file("Gate hub about internal/gate/gate.go", "hub body", "--link", closedDecision, "--kind", "trap")
	cite := h.file("Gate finding citing the hub", "cites "+hub, "--kind", "todo", "--until", "claim", "--for", "all")
	h.must("where the gate work stands", "state")

	return h, hub, cite
}

func TestShowAnswersJSON(t *testing.T) {
	h, hub, cite := jsonReadsHarness(t)

	for _, via := range []string{"cli", "mcp"} {
		t.Run(via, func(t *testing.T) {
			text := ""
			if via == "cli" {
				text = h.must("", "show", hub, cite, "--json", "--again")
			} else {
				text = mcpText(t, h.mcp(`{"name":"show","arguments":{"ids":["`+hub+`","`+cite+`"],"json":true,"again":true}}`))
			}

			entries, _ := decoded(t, text)["entries"].([]any)
			if len(entries) != 2 {
				t.Fatalf("show --json should answer every entry asked for: %s", text)
			}

			noEmptyKey(t, text)

			first, _ := entries[0].(map[string]any)
			withBody := append(slices.Clone(hubKeys), "body")
			slices.Sort(withBody)

			if !slices.Equal(keysOf(first), withBody) {
				t.Errorf("a shown entry should carry every key holding a value and its body, got %v", keysOf(first))
			}

			if first["kind"] != "trap" || first["body"] != "hub body" || first["status"] != "live" {
				t.Errorf("the entry object should carry the entry's values: %v", first)
			}

			links, _ := first["links"].([]any)
			link, _ := links[0].(map[string]any)

			if link["target"] != closedDecision || !strings.HasPrefix(link["state"].(string), "closed by") || link["by"] != "main" {
				t.Errorf("a link should carry its target, state and seat: %v", link)
			}

			if from, _ := first["linked_from"].([]any); len(from) != 1 || from[0] != cite {
				t.Errorf("linked_from should list the entries linking in: %v", first["linked_from"])
			}

			second, _ := entries[1].(map[string]any)
			if until, _ := second["until"].([]any); len(until) != 1 || until[0] != "claim" {
				t.Errorf("until should carry the declared ends: %v", second["until"])
			}

			if forList, _ := second["for"].([]any); len(forList) != 1 || forList[0] != "all" {
				t.Errorf("for should name who the entry is for: %v", second["for"])
			}

			filed, _ := second["filed"].(string)
			if _, err := time.Parse(time.RFC3339, filed); err != nil || !strings.HasSuffix(filed, "Z") {
				t.Errorf("times should be RFC 3339 in UTC, got %q", filed)
			}
		})
	}
}

func TestSearchAnswersJSON(t *testing.T) {
	h, _, _ := jsonReadsHarness(t)

	for _, via := range []string{"cli", "mcp"} {
		t.Run(via, func(t *testing.T) {
			text := ""
			if via == "cli" {
				text = h.must("", "search", "--term", "Gate", "--limit", "1", "--json", "--again")
			} else {
				text = mcpText(t, h.mcp(`{"name":"search","arguments":{"term":["Gate"],"limit":1,"json":true,"again":true}}`))
			}

			noEmptyKey(t, text)

			answer := decoded(t, text)
			entries, _ := answer["entries"].([]any)
			first, _ := entries[0].(map[string]any)
			withScore := append(slices.Clone(hubKeys), "score")
			slices.Sort(withScore)

			if len(entries) != 1 || !slices.Equal(keysOf(first), withScore) {
				t.Errorf("a search hit should carry every key holding a value and its score, and no body, got %v", keysOf(first))
			}

			if more, _ := answer["more"].(map[string]any); more["skip"] != float64(1) {
				t.Errorf("a page with results past it should name the next skip: %v", answer["more"])
			}
		})
	}

	if last := decoded(t, h.must("", "search", "--term", "Gate", "--skip", "1", "--json")); last["more"] != nil {
		t.Errorf("the last page should carry no more: %v", last)
	}
}

func TestBriefAnswersJSON(t *testing.T) {
	h, hub, cite := jsonReadsHarness(t)
	before := h.slots("brief/")

	for _, via := range []string{"cli", "mcp"} {
		t.Run(via, func(t *testing.T) {
			text := ""
			if via == "cli" {
				text = h.must("", "brief", "--json")
			} else {
				text = mcpText(t, h.mcp(`{"name":"brief","arguments":{"json":true}}`))
			}

			noEmptyKey(t, text)

			answer := decoded(t, text)
			if !slices.Equal(keysOf(answer), []string{
				"language", "live", "notice", "retired", "seat", "shelf", "state", "todos", "topics", "traps",
			}) {
				t.Errorf("the brief should carry every section holding an entry and its counts, zero ones too, got %v", keysOf(answer))
			}

			state, _ := answer["state"].(map[string]any)
			if !slices.Equal(keysOf(state), []string{"at", "body", "branch", "commit"}) || state["body"] != "where the gate work stands" {
				t.Errorf("the brief should carry the state slot: %v", state)
			}

			traps, _ := answer["traps"].([]any)
			todos, _ := answer["todos"].([]any)

			if len(traps) != 1 || traps[0].(map[string]any)["id"] != hub || len(todos) != 1 || todos[0].(map[string]any)["id"] != cite {
				t.Errorf("the brief's entry lists should split by kind: %v", answer)
			}
		})
	}

	if after := h.slots("brief/"); !maps.Equal(after, before) {
		t.Errorf("brief --json should leave the brief mark untouched, got %v from %v", after, before)
	}
}

func TestWhoAndTopicsAnswerJSON(t *testing.T) {
	h, _, _ := jsonReadsHarness(t)

	for _, via := range []string{"cli", "mcp"} {
		t.Run(via, func(t *testing.T) {
			who, topics := "", ""
			if via == "cli" {
				who, topics = h.must("", "who", "--all", "--json"), h.must("", "topics", "--json")
			} else {
				who = mcpText(t, h.mcp(`{"name":"who","arguments":{"all":true,"json":true}}`))
				topics = mcpText(t, h.mcp(`{"name":"topics","arguments":{"json":true}}`))
			}

			noEmptyKey(t, who)
			noEmptyKey(t, topics)

			seats, _ := decoded(t, who)["seats"].([]any)
			main, _ := seats[0].(map[string]any)

			if !slices.Equal(keysOf(main), []string{"filed", "holder", "name", "project", "shelf", "state", "used"}) ||
				main["name"] != "main" || main["state"] != "held" || main["filed"] != float64(2) || main["project"] != repoID {
				t.Errorf("who --all --json should name every seat with its state, holder, counts and project: %v", main)
			}

			counts, _ := decoded(t, topics)["topics"].(map[string]any)
			if gate, _ := counts["gate"].(map[string]any); gate["main"] != float64(2) {
				t.Errorf("topics --json should count each topic per seat: %v", counts)
			}
		})
	}

	if plain := decoded(t, h.must("", "who", "--json")); plain["seats"].([]any)[0].(map[string]any)["project"] != nil {
		t.Errorf("who --json without --all should name no project: %v", plain)
	}
}

func TestARefusedJSONReadSaysSoInText(t *testing.T) {
	h, _, _ := jsonReadsHarness(t)

	got := h.run("", "show", "m-zzzzzz", "--json")
	if got.code == 0 || got.out != "" || !strings.Contains(got.err, "agentic-memory: m-zzzzzz: no such entry") {
		t.Errorf("a refused JSON read should exit non-zero with its refusal on stderr and nothing on stdout, got %+v", got)
	}

	if got := h.run("", "search", "--json"); got.code == 0 || got.out != "" || !strings.Contains(got.err, "search needs") {
		t.Errorf("a refused JSON search should refuse in text, got %+v", got)
	}
}
