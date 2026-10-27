package memory_test

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
)

type heatRecord struct {
	Value float64   `json:"value"`
	Used  time.Time `json:"used"`
}

func (h *harness) heatOf(id string) heatRecord {
	h.t.Helper()

	var heats map[string]heatRecord

	err := json.Unmarshal([]byte(h.heat()), &heats)
	if err != nil {
		h.t.Fatalf("the heat file should hold JSON: %v", err)
	}

	return heats[id]
}

func TestRecallWarmsNothing(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	prompt := hookPayload("", "", "how does the tail seal manifest rename work")
	target := h.file("Tail seal manifest rename", "the tail seal writes the manifest by rename")

	for _, word := range unrelated[:19] {
		h.file("Unrelated finding about "+word, word+" "+word+" "+word)
	}

	h.clock.Advance(time.Hour)

	before := h.heat()

	if out := h.must(prompt, "prompt"); !strings.Contains(out, target) {
		t.Fatalf("the prompt should recall the entry it touches:\n%s", out)
	}

	if h.heat() != before {
		t.Error("an entry shown in recall should keep the heat it had")
	}
}

func TestALinkWarmsItsMemoryTarget(t *testing.T) {
	cases := []struct {
		name  string
		link  func(h *harness, from string, target string)
		warms bool
	}{
		{name: "the command", warms: true, link: func(h *harness, from string, target string) {
			h.must("", "link", from, target)
		}},
		{name: "the MCP tool", warms: true, link: func(h *harness, from string, target string) {
			h.mcp(`{"name":"link","arguments":{"id":"` + from + `","target":"` + target + `"}}`)
		}},
		{name: "the command with --no-heat", link: func(h *harness, from string, target string) {
			h.must("", "link", from, target, "--no-heat")
		}},
		{name: "the MCP tool with no_heat", link: func(h *harness, from string, target string) {
			h.mcp(`{"name":"link","arguments":{"id":"` + from + `","target":"` + target + `","no_heat":true}}`)
		}},
	}

	for _, linked := range cases {
		t.Run(linked.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			target := h.file("Gate finding linked later", "the target")
			from := h.file("Gate finding that links", "the linking entry")

			before := h.heatOf(target)
			if !before.Used.IsZero() {
				t.Fatal("filing an entry should set no use")
			}

			linked.link(h, from, target)

			after := h.heatOf(target)
			if !strings.Contains(h.must("", "show", from, "--no-heat"), "→ "+target) {
				t.Fatal("the link should land")
			}

			if !linked.warms {
				if after != before {
					t.Error("a link without heat should leave the target's heat and use as they were")
				}

				return
			}

			if math.Abs(after.Value-before.Value-0.5) > 1e-3 || after.Used.IsZero() {
				t.Errorf("a link should warm its target by half a use and record the use, got %+v from %+v", after, before)
			}

			if !h.heatOf(from).Used.IsZero() {
				t.Error("a link should warm its target, not the entry linking")
			}
		})
	}
}

func TestWhoCountsFiledAndUsedEntriesPerSeat(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	old := h.file("Gate finding filed long ago", "an old finding")

	h.clock.Advance(20 * 24 * time.Hour)

	fresh := h.file("Gate finding filed this week", "a fresh finding")
	h.file("Gate finding nobody reads", "an unread finding")
	h.must("", "show", old)
	h.must("", "confirm", fresh)

	other := newHarnessSharing(h)
	other.must("", "claim", "drive")
	other.file("Drive finding filed once", "a drive finding")

	counts := map[string]string{
		"main":  `(?m)^main\s+shelf main\s+.*  filed 2 / used 2 \(14d\)$`,
		"drive": `(?m)^drive\s+shelf drive\s+.*  filed 1 / used 0 \(14d\)$`,
	}

	out := h.must("", "who")
	for seatName, pattern := range counts {
		if !regexp.MustCompile(pattern).MatchString(out) {
			t.Errorf("who should count %s's entries filed and used in the last 14 days, got:\n%s", seatName, out)
		}
	}

	h.clock.Advance(15 * 24 * time.Hour)

	if out := h.must("", "who"); !regexp.MustCompile(`(?m)^main\s+shelf main\s+.*  filed 0 / used 0 \(14d\)$`).MatchString(out) {
		t.Errorf("filings and uses older than 14 days should fall out of the counts, got:\n%s", out)
	}
}

func TestADoneAckWarmsTheRule(t *testing.T) {
	cases := []struct {
		name  string
		ack   func(h *harness, rule string)
		warms bool
	}{
		{name: "ack --done", warms: true, ack: func(h *harness, rule string) {
			h.must("", "ack", rule, "--done")
		}},
		{name: "the MCP ack done", warms: true, ack: func(h *harness, rule string) {
			h.mcp(`{"name":"ack","arguments":{"ids":["` + rule + `"],"done":true}}`)
		}},
		{name: "ack --done --no-heat", ack: func(h *harness, rule string) {
			h.must("", "ack", rule, "--done", "--no-heat")
		}},
		{name: "ack --decline", ack: func(h *harness, rule string) {
			h.must("", "ack", rule, "--decline", "no board here")
		}},
	}

	for _, acked := range cases {
		t.Run(acked.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			rule := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
			h.must("", "claim", "main")

			before := h.heatOf(rule)

			acked.ack(h, rule)

			after := h.heatOf(rule)

			if !acked.warms {
				if after != before {
					t.Error("an ack that is no done use should leave the rule's heat and use as they were")
				}

				return
			}

			if math.Abs(after.Value-before.Value-0.5) > 1e-3 || after.Used.IsZero() {
				t.Errorf("a done ack should warm the rule by half a use and record the use, got %+v from %+v", after, before)
			}
		})
	}
}
