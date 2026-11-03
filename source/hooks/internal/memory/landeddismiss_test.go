package memory_test

import (
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestADeliveredMappingSurvivesAFreshContextAndAClaim(t *testing.T) {
	for _, source := range []string{"compact", "clear", "resume"} {
		t.Run(source, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it", "--about", "internal/gate/gate.go")

			h.land(test.Commit{SHA: "c4c4c4c4c4c4c4c4", Message: "Rework waiter", Paths: []string{"internal/gate/gate.go"}})

			if line := landedLine(h.runPrompt()); !strings.Contains(line, id+" by c4c4c4c4 ") {
				t.Fatalf("the running session should be told of the mapping:\n%s", line)
			}

			if line := landedLine(h.runPrompt() + "\n" + h.bashHook("ls")); line != "" {
				t.Errorf("a mapping should be named once per context:\n%s", line)
			}

			// A compaction's and a /clear's brief names the mapping again; a
			// resume prints none, so the next prompt does.
			started := h.said(hookPayload(source, "", ""), "session-start")
			prompted := landedLine(h.runPrompt())

			told := strings.Contains(started, "retire? landed: c4c4c4c4 Rework waiter; 0/1")
			if source == "resume" {
				told = strings.Contains(prompted, id+" by c4c4c4c4 ")
			}

			if !told {
				t.Errorf("a context started afresh should be told of the mapping again:\n%s\n%s", started, prompted)
			}

			if source != "resume" && prompted != "" {
				t.Errorf("a mapping the fresh context's brief named should reach no hook:\n%s", prompted)
			}

			if line := briefLine(h.must("", "brief"), id); !strings.HasSuffix(strings.TrimSuffix(line, "; 0/1"), "retire? landed: c4c4c4c4 Rework waiter") {
				t.Errorf("the brief after a fresh context should still flag the entry:\n%s", line)
			}

			if line := briefLine(h.must("", "claim", "main"), id); !strings.HasSuffix(line, "retire? landed: c4c4c4c4 Rework waiter") {
				t.Errorf("the brief of a new claim should still flag the entry:\n%s", line)
			}
		})
	}
}

func TestANarrowedBriefCarriesTheLandedFlag(t *testing.T) {
	for _, narrowed := range [][]string{{"--kind", "todo"}, {"--topic", "gate"}, {"--due"}} {
		t.Run(narrowed[0], func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			id := h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it", "--topic", "gate", "--due", "2099-01-01",
				"--about", "internal/gate/gate.go")

			h.land(test.Commit{SHA: "c4c4c4c4c4c4c4c4", Message: "Rework waiter", Paths: []string{"internal/gate/gate.go"}})

			h.runPrompt()

			args := append([]string{"brief"}, narrowed...)

			if line := narrowedLine(h.must("", append(args, "--again")...), id); !strings.HasSuffix(line, "retire? landed: c4c4c4c4 Rework waiter") {
				t.Errorf("an entry's whole line should end in its landed flag:\n%s", line)
			}

			line := narrowedLine(h.must("", args...), id)
			if !strings.Contains(line, "(seen)") || !strings.HasSuffix(line, "retire? landed: c4c4c4c4 Rework waiter") {
				t.Errorf("an entry's seen pointer should end in its landed flag:\n%s", line)
			}
		})
	}
}

func TestDismissForgetsAMatchUntilALaterCommit(t *testing.T) {
	cases := []struct {
		name   string
		target func(id string, item string) string
		flag   func(item string) string
	}{
		{name: "an entry", target: func(id string, _ string) string { return id }, flag: func(string) string { return "retire? landed:" }},
		{
			name:   "an item",
			target: func(id string, item string) string { return id + "#" + item },
			flag:   func(item string) string { return "ack #" + item + "? landed:" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLandedHarness(t)
			h.must("", "claim", "main")
			out := h.must("body", "new", "--title", "Rework the waiter loop", "--topic", "gate", "--kind", "todo",
				"--item", "build waiter polling deadline", "--item", "capture the decision", "--about", "internal/gate/gate.go")
			id := idPattern.FindString(out)
			item := itemPattern.FindStringSubmatch(out)[1]
			target := tc.target(id, item)

			h.land(test.Commit{SHA: "d5d5d5d5d5d5d5d5", Message: "Build waiter polling deadline", Paths: []string{"internal/gate/gate.go"}})
			h.subagentHook("git pull")

			if answer := h.must("", "landed", "dismiss", target); strings.TrimSpace(answer) != "dismissed" {
				t.Fatalf("a dismiss should say it dismissed:\n%s", answer)
			}

			if slot := h.slot("landed"); strings.Contains(slot, `"target":"`+target+`"`) {
				t.Errorf("a dismissed match should be gone from the store:\n%s", slot)
			}

			if brief := h.must("", "brief"); strings.Contains(brief, tc.flag(item)) {
				t.Errorf("a dismissed match should be gone from the brief:\n%s", brief)
			}

			if strings.Contains(h.must("", "show", id), "retired") {
				t.Errorf("a dismiss should leave the entry live")
			}

			h.land(test.Commit{SHA: "d6d6d6d6d6d6d6d6", Message: "Build waiter polling deadline again", Paths: []string{"internal/gate/gate.go"}})
			h.subagentHook("git pull")

			if slot := h.slot("landed"); !strings.Contains(slot, `"target":"`+target+`","commit":"d6d6d6d6d6d6d6d6"`) {
				t.Errorf("a later commit should match a dismissed entry again:\n%s", slot)
			}
		})
	}
}

func TestDismissRefusesAnIDWithoutAMatch(t *testing.T) {
	h := newLandedHarness(t)
	h.must("", "claim", "main")
	matched := h.file("Rework the waiter loop", "body", "--kind", "todo", "--item", "settle it", "--about", "internal/gate/gate.go")
	unmatched := h.file("Refresh the readme", "body", "--kind", "todo", "--item", "settle it", "--about", "README.md")

	h.land(test.Commit{SHA: "e7e7e7e7e7e7e7e7", Message: "Rework waiter", Paths: []string{"internal/gate/gate.go"}})
	h.subagentHook("git pull")

	refusal := h.run("", "landed", "dismiss", matched, unmatched)
	if refusal.code == 0 || !strings.Contains(refusal.err, "nothing dismissed:\n  "+unmatched+": no landed match stored for this seat") {
		t.Errorf("an id holding no match should refuse the call, naming it:\n%s", refusal.err)
	}

	if strings.Contains(refusal.err, matched) {
		t.Errorf("the refusal should name only the failing id:\n%s", refusal.err)
	}

	if slot := h.slot("landed"); !strings.Contains(slot, `"target":"`+matched+`"`) {
		t.Errorf("a refused dismiss should forget nothing:\n%s", slot)
	}

	if usage := h.run("", "landed", matched); usage.code == 0 || !strings.Contains(usage.err, "landed dismiss <id>...") {
		t.Errorf("a landed call without dismiss should name its usage:\n%s", usage.err)
	}
}
