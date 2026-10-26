package memory_test

import (
	"strings"
	"testing"
	"time"
)

func TestUntagRemovesTopics(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding under three topics", "the finding", "--topic", "mcp", "--topic", "stale")

	if out := h.must("", "untag", id, "stale", "MCP"); !strings.Contains(out, id+" untagged stale, mcp") {
		t.Errorf("untag should name the topics it removed, got:\n%s", out)
	}

	shown := h.must("", "show", id, "--no-heat")
	if !strings.Contains(shown, "topics: gate\n") || !strings.Contains(shown, "untagged mcp, stale by main") {
		t.Errorf("show should list only the topics left and the untag event, got:\n%s", shown)
	}

	if out := h.must("", "search", "--term", "finding", "--topic", "stale", "--no-heat"); strings.Contains(out, id) {
		t.Errorf("a search by a removed topic should no longer find the entry, got:\n%s", out)
	}

	if out := h.must("", "topics"); strings.Contains(out, "stale") || strings.Contains(out, "mcp") {
		t.Errorf("the topic counts should drop the removed topics, got:\n%s", out)
	}

	answer := h.mcp(`{"name":"untag","arguments":{"id":"` + id + `","topics":["gate"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, id+" keeps at least one topic") {
		t.Errorf("the MCP untag should refuse the last topic as the command does: %s", answer)
	}

	h.must("", "tag", id, "wire")

	answer = h.mcp(`{"name":"untag","arguments":{"id":"` + id + `","topics":["gate"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, id+" untagged gate") {
		t.Errorf("the MCP untag should take id and topics and remove the topic: %s", answer)
	}

	if out := h.must("", "show", id, "--no-heat"); !strings.Contains(out, "topics: wire\n") {
		t.Errorf("an MCP untag should show in show like a command's, got:\n%s", out)
	}
}

func TestUntagReadsTopicsThroughMerges(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding under a merged topic", "the finding", "--topic", "old")
	h.must("", "merge", "old", "new")

	h.must("", "untag", id, "new")

	if out := h.must("", "show", id, "--no-heat"); !strings.Contains(out, "topics: gate\n") {
		t.Errorf("untag should remove a topic the entry carries under a name merged into it, got:\n%s", out)
	}
}

func TestUntagRefusals(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding under two topics", "the finding", "--topic", "mcp")

	cases := []struct {
		name    string
		args    []string
		refusal string
	}{
		{name: "the last topic", args: []string{"untag", id, "gate", "mcp"},
			refusal: id + " keeps at least one topic; tag the right one first: agentic-memory tag " + id + " <topic>"},
		{name: "a topic the entry does not carry", args: []string{"untag", id, "wire"},
			refusal: id + " carries no topic wire"},
		{name: "no topic", args: []string{"untag", id}, refusal: "an entry id and at least one topic expected"},
	}

	for _, refused := range cases {
		t.Run(refused.name, func(t *testing.T) {
			got := h.run("", refused.args...)
			if got.code != 1 || !strings.Contains(got.err, refused.refusal) {
				t.Errorf("untag should be refused, saying why, got %d %q", got.code, got.err)
			}
		})
	}

	if out := h.must("", "show", id, "--no-heat"); !strings.Contains(out, "topics: gate, mcp\n") {
		t.Errorf("a refused untag should leave the topics as they were, got:\n%s", out)
	}
}

func TestUntagIsAShelfEdit(t *testing.T) {
	h, other := rowShelf(t)
	id := h.file("Gate finding on the row", "the row's finding", "--topic", "mcp", "--topic", "wire")

	h.must(h.promptHook(""), "prompt")
	h.clock.Advance(time.Minute)
	other.must("", "untag", id, "mcp")

	out := h.must(h.promptHook("again"), "prompt")
	if !strings.Contains(out, "agentic-memory: changed on shelf row-7ah (1):") ||
		!strings.Contains(out, id+" Gate finding on the row: untagged mcp by rev") {
		t.Errorf("a shelf mate's untag should reach the other seats on the shelf, got:\n%s", out)
	}

	other.must("", "release")
	other.must("", "claim", "main")

	got := other.run("", "untag", id, "wire")
	if got.code != 1 || !strings.Contains(got.err, id+" is on shelf row-7ah") {
		t.Errorf("untag from a seat on another shelf should be refused naming the shelf, got %d %q", got.code, got.err)
	}
}
