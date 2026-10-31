package memory_test

import (
	"strings"
	"testing"
)

func TestForbidsBelongsToARule(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	out := h.must("never file notes like pilot v2.3", "new", "--title", "No notes on pilot versions", "--kind", "rule",
		"--from", "user", "--forbids", `(?i)pilot v\d+`)
	if !strings.Contains(h.must("", "show", idPattern.FindString(out), "--again"), `forbids: (?i)pilot v\d+`) ||
		strings.Contains(out, "forbidden by") {
		t.Errorf("the rule should carry the patterns, forbidding nothing of itself:\n%s", out)
	}

	for _, tc := range []struct {
		args    []string
		refusal string
	}{
		{args: []string{"--forbids", "pilot"}, refusal: "--forbids is for a rule"},
		{args: []string{"--kind", "rule", "--from", "user", "--forbids", "pilot("}, refusal: "is not an RE2 pattern"},
	} {
		got := h.run("body", append([]string{"new", "--title", "Rule naming what never belongs"}, tc.args...)...)
		if got.code != 1 || !strings.Contains(got.err, tc.refusal) {
			t.Errorf("%v should be refused, got %d %q", tc.args, got.code, got.err)
		}
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Rule over MCP here","body":"b","kind":"rule","from":"user","forbids":["secret"]}}`)
	if strings.Contains(answer, `"isError":true`) || !strings.Contains(h.must("", "show", idPattern.FindString(answer), "--again"),
		"forbids: secret") {
		t.Errorf("the MCP key forbids should store the patterns: %s", answer)
	}

	answer = h.mcp(`{"name":"new","arguments":{"title":"Note over MCP here","body":"b","forbids":["secret"]}}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "--forbids is for a rule") {
		t.Errorf("--forbids on a note over MCP should be refused: %s", answer)
	}
}

func TestAForbiddenEntryLandsWithAWarning(t *testing.T) {
	h, other := twoSeats(t)

	rule := other.file("No notes on pilot versions", "the project only uses pilot", "--kind", "rule", "--from", "user",
		"--forbids", `(?i)pilot v\d+\.\d+`, "--for", "all")

	out := h.must("upgraded to Pilot v2.3 today", "new", "--title", "Pilot upgrade done today", "--topic", "gate")
	id := idPattern.FindString(out)

	want := "forbidden by " + rule + " No notes on pilot versions\n" +
		`  matched: "Pilot v2.3"` + "\n" +
		"  → agentic-memory retire " + id + " --because 'forbidden by " + rule + "'"
	if !strings.Contains(out, want) {
		t.Errorf("a filing matching a received rule's pattern should warn with the rule, the match and the retire:\n%s", out)
	}

	if listed := h.must("", "search", "--term", "Pilot upgrade"); !strings.Contains(listed, id) {
		t.Errorf("a forbidden entry should still land:\n%s", listed)
	}

	if clean := h.must("the gate reads Bash only", "new", "--title", "Gate reads Bash only today", "--topic", "gate"); strings.Contains(clean, "forbidden by") {
		t.Errorf("an entry matching no pattern should draw no warning:\n%s", clean)
	}

	other.must("", "rekind", rule, "note")

	if after := h.must("Pilot v2.4 is out", "new", "--title", "Pilot release noted today", "--topic", "gate"); strings.Contains(after, "forbidden by") {
		t.Errorf("an entry no longer a rule should forbid nothing:\n%s", after)
	}
}

func TestARuleTheSeatDoesNotReceiveDoesNotWarn(t *testing.T) {
	h, other := twoSeats(t)

	other.file("No notes on pilot versions", "the project only uses pilot", "--kind", "rule", "--from", "user",
		"--forbids", `(?i)pilot v\d+`)

	if out := h.must("upgraded to Pilot v2 today", "new", "--title", "Pilot upgrade done today", "--topic", "gate"); strings.Contains(out, "forbidden by") {
		t.Errorf("a rule the seat does not receive should not warn:\n%s", out)
	}
}
