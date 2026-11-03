package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	cliStateAsk = `agentic-memory ack state --add '<line>' | ack state#<item> --done`
	mcpStateAsk = `memory ack {"ids":["state"],"add":["<line>"]} | ack {"ids":["state#<item>"],"done":true}`
)

// claimThrough takes the seat main through the channel named: the command
// line, or the memory server.
func (h *harness) claimThrough(through string) {
	h.t.Helper()

	if through == "mcp" {
		h.mcp(`{"name":"claim","arguments":{"name":"main"}}`)

		return
	}

	h.must("", "claim", "main")
}

func (h *harness) dropLastChannel() {
	h.t.Helper()

	err := slotFileIn(h.cacheDir()).Delete(context.Background(), "channel/"+seatID(h.t, h.store(), "main"))
	if err != nil {
		h.t.Fatalf("clearing the seat's last channel: %v", err)
	}
}

func TestEveryMemoryCallKeepsItsChannelOnTheSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	key := "channel/" + seatID(t, h.store(), "main")

	if got := h.slot(key); got != `{"last_channel":"cli"}` {
		t.Errorf("a command-line call should keep cli as the seat's last channel, got %q", got)
	}

	h.mcp(`{"name":"who","arguments":{}}`)

	if got := h.slot(key); got != `{"last_channel":"mcp"}` {
		t.Errorf("an MCP call should keep mcp as the seat's last channel, got %q", got)
	}

	h.must("", "who")

	if got := h.slot(key); got != `{"last_channel":"cli"}` {
		t.Errorf("a command-line who should keep cli as the seat's last channel, got %q", got)
	}

	before := h.generations("channel/")
	h.must("", "brief")

	if after := h.generations("channel/"); after[key] != before[key] {
		t.Error("a call through the channel the seat last used should leave its last channel unwritten")
	}

	h.mcp(`{"name":"who","arguments":{}}`)
	h.run("", "retitle", "m-none01", "No such entry here")

	if got := h.slot(key); got != `{"last_channel":"mcp"}` {
		t.Errorf("a refused command line should leave the seat's last channel, got %q", got)
	}

	h.must("", "who")
	h.mcp(`{"name":"retitle","arguments":{"id":"m-none01","title":"No such entry here"}}`)

	if got := h.slot(key); got != `{"last_channel":"cli"}` {
		t.Errorf("a refused MCP call should leave the seat's last channel, got %q", got)
	}
}

func TestACallHoldingNoSeatKeepsNoChannel(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.must("", "release")

	h.mcp(`{"name":"who","arguments":{}}`)

	held := h.slots("channel/")
	if len(held) != 1 || held["channel/"+seatID(t, h.store(), "main")] == "" {
		t.Errorf("a call holding no seat should keep no channel, only the seat's own key standing: %v", held)
	}
}

func TestASubagentReadingForTheSeatLeavesItsChannel(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	key := "channel/" + seatID(t, h.store(), "main")
	h.spawned("areader00000001", "agentic-helpers: silent")

	h.mcpAs("areader00000001", "search", map[string]any{"term": []string{"gate"}})

	if got := h.slot(key); got != `{"last_channel":"cli"}` {
		t.Errorf("a reading subagent's MCP call should leave the seat's last channel, got %q", got)
	}
}

func TestACommandsReplyHintsInItsOwnChannel(t *testing.T) {
	h := heldHarness(t)

	// Each call follows one through the other channel, so a reply taking the
	// seat's last channel in place of its own shows.
	mcp := mcpMessage(t, h.mcp(`{"name":"new","arguments":{"title":"Three four","topic":["gate"],"body":"finding"}}`))
	if !strings.Contains(mcp, `memory retitle {"id":"`) || strings.Contains(mcp, "agentic-memory retitle") {
		t.Errorf("an MCP call's reply should hint MCP calls only:\n%s", mcp)
	}

	cli := h.must("finding", "new", "--title", "One two", "--topic", "gate")
	if !strings.Contains(cli, "agentic-memory retitle ") || strings.Contains(cli, `memory retitle {`) {
		t.Errorf("a command line's reply should hint command lines only:\n%s", cli)
	}
}

func TestACLIOnlyHintStaysACommandLineInBothChannels(t *testing.T) {
	h := heldHarness(t)
	h.file("A finding about clocks", "body", "--topic", "clocks")

	cli := h.must("finding", "new", "--title", "Another finding about clocks", "--topic", "clock")
	mcp := mcpMessage(t, h.mcp(`{"name":"new","arguments":{"title":"A third clock finding","topic":["clocck"],"body":"finding"}}`))

	for name, reply := range map[string]string{"command line": cli, "MCP": mcp} {
		if !strings.Contains(reply, "→ agentic-memory merge ") || strings.Contains(reply, "memory merge {") {
			t.Errorf("a topic merge, which the server does not list, should be hinted as a command line over the %s:\n%s",
				name, reply)
		}
	}
}

func TestAToolHookLineFollowsTheCallItFollows(t *testing.T) {
	cases := []struct {
		name    string
		claimed string
		call    string
		want    string
		refused string
	}{
		{"after an MCP memory call", "cli",
			`"tool_name":"mcp__plugin_agentic-helpers_memory__show","tool_input":{"ids":["state"]}`,
			mcpStateAsk, cliStateAsk},
		{"after a Bash memory call", "mcp",
			`"tool_name":"Bash","tool_input":{"command":"agentic-memory show state"}`,
			cliStateAsk, mcpStateAsk},
		{"after a Bash call running no memory command, the last channel", "mcp",
			`"tool_name":"Bash","tool_input":{"command":"grep -r agentic-memory docs"}`,
			mcpStateAsk, cliStateAsk},
		{"after any other call, the last channel", "cli",
			`"tool_name":"Read","tool_input":{"file_path":"x"}`,
			cliStateAsk, mcpStateAsk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.claimThrough(tc.claimed)
			h.file("Gate reads Bash only today", "finding")
			h.claimThrough(tc.claimed)
			h.sensor(70)

			for range 9 {
				h.readCall("")
			}

			said := toolContext(h.must(fmt.Sprintf(`{"session_id":%q,%s}`, h.session, tc.call), "tool"))
			if !strings.Contains(said, reminderOpening) {
				t.Fatalf("the tenth tool call should carry the reminder:\n%s", said)
			}

			if !strings.Contains(said, tc.want) || strings.Contains(said, tc.refused) {
				t.Errorf("the reminder should name the form of the call it follows:\n%s", said)
			}
		})
	}
}

func TestALineAnsweringNoCallFollowsTheLastChannel(t *testing.T) {
	cases := []struct {
		name    string
		claimed string
		dropped bool
		want    string
		refused string
	}{
		{"last channel cli", "cli", false, cliStateAsk, mcpStateAsk},
		{"last channel mcp", "mcp", false, mcpStateAsk, cliStateAsk},
		{"no last channel", "cli", true, mcpStateAsk, cliStateAsk},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.file("Gate reads Bash only today", "finding")
			h.claimThrough(tc.claimed)

			if tc.dropped {
				h.dropLastChannel()
			}

			h.sensor(70)

			said := h.said(hookPayload("", "", ""), "prompt")
			if !strings.Contains(said, reminderOpening) {
				t.Fatalf("the prompt should carry the near-compaction nudge:\n%s", said)
			}

			if !strings.Contains(said, tc.want) || strings.Contains(said, tc.refused) {
				t.Errorf("the nudge should take the seat's last channel, MCP where it has none:\n%s", said)
			}
		})
	}
}

func TestTheSessionStartBriefFollowsTheLastChannel(t *testing.T) {
	h := newHarness(t)
	h.mcp(`{"name":"claim","arguments":{"name":"main"}}`)
	h.mcp(`{"name":"ack","arguments":{"ids":["state"],"add":["halfway through the gate fix"]}}`)

	start, _ := json.Marshal(map[string]string{"session_id": h.session, "source": "compact"})

	said := h.said(string(start), "session-start")
	if !strings.Contains(said, `state (memory ack {"ids":["state#<item>"],"done":true}`) {
		t.Errorf("the brief after a compaction should name MCP calls for a seat last called over MCP:\n%s", said)
	}
}
