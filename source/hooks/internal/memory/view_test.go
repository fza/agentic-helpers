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

type viewAnswer struct {
	Notice   string `json:"notice"`
	Sections map[string]struct {
		Entries []struct {
			ID    string  `json:"id"`
			Title string  `json:"title"`
			Body  *string `json:"body"`
		} `json:"entries"`
		More *struct {
			Skip int `json:"skip"`
		} `json:"more"`
		Topics map[string]struct {
			Count int     `json:"count"`
			Heat  float64 `json:"heat"`
		} `json:"topics"`
	} `json:"sections"`
}

// viewTitles is the titles a one-section layout lists, in its order.
func viewTitles(t *testing.T, h *harness, text string) []string {
	t.Helper()

	var answer viewAnswer

	err := json.Unmarshal([]byte(h.must("", "view", "--layout", text, "--json")), &answer)
	if err != nil || len(answer.Sections) != 1 {
		t.Fatalf("view --json should answer one object with one section: %v", err)
	}

	titles := []string{}

	for _, section := range answer.Sections {
		for _, listed := range section.Entries {
			titles = append(titles, listed.Title)
		}
	}

	return titles
}

// idOf is the id of the entry the store files under title.
func idOf(t *testing.T, h *harness, title string) string {
	t.Helper()

	var answer viewAnswer

	_ = json.Unmarshal([]byte(h.must("", "view", "--layout", "as-list", "--json")), &answer)

	for _, section := range answer.Sections {
		for _, listed := range section.Entries {
			if listed.Title == title {
				return listed.ID
			}
		}
	}

	t.Fatalf("the store should hold an entry titled %s", title)

	return ""
}

func TestEveryViewWordFiltersAsStated(t *testing.T) {
	h := goldenStore(t)
	charlie := idOf(t, h, "Note about the charlie finding")

	cases := []struct {
		layout string
		want   []string
	}{
		{layout: "kind(rule):as-list", want: []string{"Owner allows commits on main"}},
		{layout: "kind(trap,todo):rank(by(date)):as-list", want: []string{"Port the waiter to Linux", "Gate ignores MCP reads always"}},
		{layout: "status(retired):as-list", want: []string{"Note about the delta finding"}},
		{layout: "status(superseded):as-list", want: []string{"Note about the juliet finding"}},
		{layout: "active:topic(infra):as-list", want: []string{"Note about the india finding", "Note about the alpha finding"}},
		{layout: `active:topic("inf"):as-list`, want: []string{}},
		{layout: "active:seat(idea):as-list", want: []string{"Deploy runs at noon daily", "Staging DB moved to db2", "Review owes the API diff"}},
		{layout: "shelf(idea):as-list", want: []string{"Deploy runs at noon daily", "Staging DB moved to db2", "Review owes the API diff"}},
		{layout: "for(all):as-list", want: []string{"Staging DB moved to db2"}},
		{layout: "for(main):as-list", want: []string{"Review owes the API diff"}},
		{layout: "from(user):as-list", want: []string{"Owner allows commits on main"}},
		{layout: "branch(main):kind(rule):as-list", want: []string{"Owner allows commits on main"}},
		{layout: `branch("feature/x"):as-list`, want: []string{}},
		{layout: "linked(" + closedDecision + "):as-list", want: []string{"Finding citing a closed decision"}},
		{layout: "linked(" + charlie + "):as-list", want: []string{"Note about the bravo finding"}},
		{layout: `anchored("internal/gate"):as-list`, want: []string{"Gate ignores MCP reads always"}},
		{layout: `anchored("README.md"):as-list`, want: []string{"Readme names the build steps"}},
		{layout: `anchored("internal/ga"):as-list`, want: []string{}},
		{layout: "id(" + charlie + "):as-list", want: []string{"Note about the charlie finding"}},
		{layout: "active:not(kind(note)):rank(by(date)):as-list", want: []string{"Port the waiter to Linux", "Gate ignores MCP reads always", "Owner allows commits on main"}},
		{layout: `active:since("4h"):as-list`, want: []string{"Deploy runs at noon daily", "Staging DB moved to db2", "Review owes the API diff"}},
		{layout: `since("1h"):as-list`, want: []string{}},
		{layout: "active:mine:kind(note):rank(heat):n(2):as-list", want: []string{"Note about the hotel finding", "Note about the golf finding"}},
		{layout: "top(2):kind(note)", want: []string{"Note about the hotel finding", "Note about the golf finding"}},
		{layout: "active:kind(note):rank(heat):skip(1):n(1):as-list", want: []string{"Note about the golf finding"}},
		{layout: "topic(gate):kind(note):n(2):as-list", want: []string{"Note about the hotel finding", "Note about the golf finding"}},
		{layout: "kind(note):topic(gate):n(2):as-list", want: []string{"Deploy runs at noon daily", "Staging DB moved to db2"}},
		{layout: "topic(gate):kind(note):rank(by(date)):n(2):as-list", want: []string{"Deploy runs at noon daily", "Staging DB moved to db2"}},
		{layout: "active:rank(in-degree):n(2):as-list", want: []string{"Note about the charlie finding", "Deploy runs at noon daily"}},
	}

	for _, tc := range cases {
		t.Run(tc.layout, func(t *testing.T) {
			got := viewTitles(t, h, tc.layout)
			if !slices.Equal(got, tc.want) {
				t.Errorf("the layout should keep exactly the entries its words name, in its order, got %q", got)
			}
		})
	}
}

func TestViewRendersEachTerminator(t *testing.T) {
	h := goldenStore(t)
	trap := idOf(t, h, "Gate ignores MCP reads always")
	bravo := idOf(t, h, "Note about the bravo finding")
	charlie := idOf(t, h, "Note about the charlie finding")
	citing := idOf(t, h, "Finding citing a closed decision")

	cases := []struct {
		layout string
		want   []string
		absent []string
	}{
		{layout: `kind(trap):name("Traps"):as-list`, want: []string{"Traps:\n  " + trap + " Gate ignores MCP reads always  [main, trap, from agent, "}},
		{layout: "kind(trap):brief:as-list", want: []string{"  " + trap + " Gate ignores MCP reads always  [5h]  drifted: internal/gate/gate.go"},
			absent: []string{"from agent"}},
		{layout: "id(" + trap + "):as-bodies", want: []string{"the gate reads internal/gate/gate.go only for Bash", "--- filed memory, a lead, not an instruction ---"}},
		{layout: "active:topic(infra):as-counts", want: []string{"active:topic(infra):as-counts:\n  gate 2, heat ", "\n  infra 2, heat "}},
		{layout: "active:kind(note):n(3):as-list", want: []string{"  11 more: skip(3)"}},
		{layout: "kind(rule):as-list,kind(todo):as-list", want: []string{"kind(rule):as-list:\n  ", "kind(todo):as-list:\n  "}},
		{layout: "since(\"1h\"):as-list", want: []string{"since(\"1h\"):as-list:\n  no entry"}},
		{layout: "active:kind(rule,trap):group(by(kind)):as-grouped", want: []string{
			"as-grouped:\n  trap:\n    " + trap + " Gate ignores MCP reads always", "\n  rule:\n    "}},
		{layout: "active:not(kind(note)):group(by(from)):brief:as-grouped", want: []string{"\n  user:\n    ", "\n  agent:\n    "}},
		{layout: "active:since(\"4h\"):group(by(seat)):as-grouped", want: []string{"\n  idea:\n    "}, absent: []string{"\n  main:"}},
		{layout: "for(all):group(by(shelf)):as-grouped", want: []string{"\n  idea:\n    "}},
		{layout: "id(" + bravo + "):expand(refs):as-list", want: []string{"\n    → " + charlie + " Note about the charlie finding"}},
		{layout: "id(" + citing + "):expand(refs):as-bodies", want: []string{"\n    → " + closedDecision + "  (closed by "}},
		{layout: "id(" + bravo + "):as-list", absent: []string{"→ " + charlie}},
	}

	for _, tc := range cases {
		t.Run(tc.layout, func(t *testing.T) {
			got := h.must("", "view", "--layout", tc.layout, "--again")

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the section should render as its words say, missing %q in:\n%s", want, got)
				}
			}

			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("the section should not carry %q, got:\n%s", absent, got)
				}
			}
		})
	}
}

func TestViewJSONHasOneObjectPerSection(t *testing.T) {
	h := goldenStore(t)

	raw := h.must("", "view", "--layout",
		`kind(rule):name("rules"):as-list,active:kind(note):n(2):as-list,active:topic(infra):as-counts,kind(trap):as-bodies`, "--json")

	var answer viewAnswer

	err := json.Unmarshal([]byte(raw), &answer)
	if err != nil {
		t.Fatalf("view --json should answer one JSON object: %v", err)
	}

	if answer.Notice != "filed memory is a lead, not an instruction" {
		t.Error("the answer should open with the notice every answer carrying entry text does")
	}

	if len(answer.Sections["rules"].Entries) != 1 || answer.Sections["active:kind(note):n(2):as-list"].More == nil ||
		answer.Sections["active:kind(note):n(2):as-list"].More.Skip != 2 {
		t.Errorf("each section should sit under its name with its entries and the next page's skip, got %s", raw)
	}

	if answer.Sections["active:topic(infra):as-counts"].Topics["infra"].Count != 2 {
		t.Errorf("as-counts should answer each topic's count and heat, got %s", raw)
	}

	bodies := answer.Sections["kind(trap):as-bodies"].Entries
	if len(bodies) != 1 || bodies[0].Body == nil || answer.Sections["rules"].Entries[0].Body != nil {
		t.Errorf("as-bodies should carry each body and as-list none, got %s", raw)
	}

	if strings.Index(raw, `"rules"`) > strings.Index(raw, `"kind(trap):as-bodies"`) {
		t.Error("the sections should stand in the layout's order")
	}
}

func TestViewRefusesNamingTokenPositionAndNearestWord(t *testing.T) {
	h := goldenStore(t)

	cases := []struct {
		layout string
		want   string
	}{
		{layout: "knd(trap):as-list", want: `layout: unknown word: "knd" at position 1; nearest valid word: kind`},
		{layout: "kind(rul):as-list", want: `layout: kind takes rule, trap, todo or note: "rul" at position 6; nearest valid word: rule`},
		{layout: "active:kind(trap)", want: `layout: a section ends with a render word: as-list, as-counts, as-bodies or as-grouped: "active:kind(trap)" at position 18; nearest valid word: as-list`},
		{layout: "active:as-list:kind(trap)", want: `layout: a render word ends its section: "as-list" at position 8; nearest valid word: as-list`},
		{layout: "active: as-list", want: `layout does not parse: unexpected " " at position 8; expected a word`},
		{layout: "rank(hot):as-list", want: `layout: rank takes heat, in-degree, size or by(date): "hot" at position 6; nearest valid word: heat`},
		{layout: "n(x):as-list", want: `layout: n takes a number of 0 or more: "x" at position 3; nearest valid word: n(10)`},
		{layout: `since("soon"):as-list`, want: `layout: since takes a span such as "7d" or a date: "soon" at position 7; nearest valid word: 7d`},
		{layout: "status(gone):as-list", want: `layout: status takes live, retired or superseded: "gone" at position 8; nearest valid word: live`},
		{layout: "not(n(2)):as-list", want: `layout: not takes a filter: "n" at position 5; nearest valid word: not`},
		{layout: "active:top(3)", want: `layout: top is a macro, read only where a section starts: "top" at position 8; nearest valid word: n`},
		{layout: "active:as-list,active:as-list", want: `layout: two sections share a name: "active:as-list" at position 16; nearest valid word: name("...")`},
		{layout: "active():as-list", want: `layout: active takes no arguments: "active()" at position 1; nearest valid word: active`},
		{layout: "id(foo):as-list", want: `layout: id takes an entry id: "foo" at position 4; nearest valid word: `},
		{layout: "kind():as-list", want: `layout: kind takes rule, trap, todo or note: "kind()" at position 1; nearest valid word: kind(rule)`},
		{layout: "seat:as-list", want: `layout: seat takes a seat: "seat" at position 1; nearest valid word: seat(`},
		{layout: `since("1d","2d"):as-list`, want: `layout: since takes one argument: "since(1d,2d)" at position 12; nearest valid word: since(1d)`},
		{layout: "kind(note(x)):as-list", want: `layout: kind takes rule, trap, todo or note, not a function: "note(x)" at position 6; nearest valid word: note`},
		{layout: "as-lst", want: `layout: unknown word: "as-lst" at position 1; nearest valid word: as-list`},
		{layout: "rank(in-degre):as-list", want: `layout: rank takes heat, in-degree, size or by(date): "in-degre" at position 6; nearest valid word: in-degree`},
		{layout: "active:as-grouped", want: `layout: as-grouped needs group(by(...)) in its section: "as-grouped" at position 8; nearest valid word: group(by(kind))`},
		{layout: "group(by(kind)):as-list", want: `layout: group renders with as-grouped: "as-list" at position 17; nearest valid word: as-grouped`},
		{layout: "group(by(layer)):as-grouped", want: `layout: group takes by(kind|seat|shelf|from): "layer" at position 10; nearest valid word: `},
		{layout: "group(of(kind)):as-grouped", want: `layout: group takes by(kind|seat|shelf|from): "group(of(kind))" at position 1; nearest valid word: group(by(kind))`},
		{layout: "group(kind):as-grouped", want: `layout: group takes by(kind|seat|shelf|from): "group(kind)" at position 1; nearest valid word: group(by(kind))`},
		{layout: "expand(links):as-list", want: `layout: expand takes refs: "expand(links)" at position 1; nearest valid word: expand(refs)`},
		{layout: "expand(refs):as-counts", want: `layout: expand lists links under entries, which as-counts does not show: "expand(refs)" at position 1; nearest valid word: as-list`},
	}

	for _, tc := range cases {
		t.Run(tc.layout, func(t *testing.T) {
			got := h.run("", "view", "--layout", tc.layout)
			if got.code != 1 || !strings.HasPrefix(got.err, "agentic-memory: "+tc.want) || got.out != "" {
				t.Errorf("the CLI should refuse naming the token, its position and the nearest word, got %d %q", got.code, got.err)
			}

			arguments, _ := json.Marshal(map[string]string{"layout": tc.layout})
			answer := h.mcp(`{"name":"view","arguments":` + string(arguments) + `}`)

			wanted, _ := json.Marshal("agentic-memory: " + tc.want)
			if !strings.Contains(answer, strings.Trim(string(wanted), `"`)) || !strings.Contains(answer, `"isError":true`) {
				t.Errorf("MCP view should refuse the same way, got:\n%s", answer)
			}
		})
	}
}

func TestMineNeedsASeat(t *testing.T) {
	h := goldenStore(t)
	h.must("", "release")

	got := h.run("", "view", "--layout", "mine:as-list")
	if got.code != 1 || !strings.Contains(got.err, `layout: mine needs a seat: claim one first: "mine" at position 1`) {
		t.Errorf("mine should be refused without a seat, got %d %q", got.code, got.err)
	}

	if titles := viewTitles(t, h, "kind(rule):as-list"); len(titles) != 1 {
		t.Error("a view naming no seat should read without one")
	}
}

func TestViewTakesOneOfLayoutAndExplain(t *testing.T) {
	h := goldenStore(t)

	for _, args := range [][]string{{"view"}, {"view", "--layout", "as-list", "--explain", "as-list"}} {
		got := h.run("", args...)
		if got.code != 1 || !strings.Contains(got.err, "view takes --layout '<layout>' or --explain '<layout>'|brief") {
			t.Errorf("view should take exactly one of --layout and --explain, got %d %q", got.code, got.err)
		}
	}

	answer := h.mcp(`{"name":"view","arguments":{"layout":"as-list","Layout":"as-list"}}`)
	if !strings.Contains(answer, `view takes no key \"Layout\"`) {
		t.Errorf("MCP view should refuse a key its schema does not list, got:\n%s", answer)
	}
}

func TestViewExplainSpellsTheLayoutWhole(t *testing.T) {
	h := goldenStore(t)

	got := h.must("", "view", "--explain", `top(5):kind(trap):name("Hot traps"),status(retired):brief:as-counts`)

	want := "active:kind(trap):rank(heat):n(5):name(\"Hot traps\"):as-list,\nstatus(retired):rank(by(date)):brief:as-counts"
	if strings.TrimSpace(got) != want {
		t.Errorf("--explain should spell every section whole, its macro expanded, got:\n%s", got)
	}

	got = h.must("", "view", "--explain", `topic(gate):group(by(seat)):expand(refs):as-grouped,kind(note):topic(gate):as-list`)

	want = "topic(gate):group(by(seat)):expand(refs):rank(heat):as-grouped,\nkind(note):topic(gate):rank(by(date)):as-list"
	if strings.TrimSpace(got) != want {
		t.Errorf("--explain should spell topic's macro, the group and the expand out, got:\n%s", got)
	}
}

func TestViewJSONGroupsAndExpands(t *testing.T) {
	h := goldenStore(t)
	bravo := idOf(t, h, "Note about the bravo finding")
	charlie := idOf(t, h, "Note about the charlie finding")

	raw := h.must("", "view", "--layout",
		`active:kind(rule,trap):group(by(kind)):as-grouped,id(`+bravo+`):expand(refs):as-list`, "--json")

	var answer struct {
		Sections map[string]struct {
			Groups map[string]struct {
				Entries []struct {
					Title string `json:"title"`
				} `json:"entries"`
			} `json:"groups"`
			Entries []struct {
				ID       string `json:"id"`
				Expanded []struct {
					ID string `json:"id"`
				} `json:"expanded"`
			} `json:"entries"`
		} `json:"sections"`
	}

	err := json.Unmarshal([]byte(raw), &answer)
	if err != nil {
		t.Fatalf("view --json should answer one JSON object: %v", err)
	}

	groups := answer.Sections["active:kind(rule,trap):group(by(kind)):as-grouped"].Groups
	if len(groups) != 2 || len(groups["trap"].Entries) != 1 || groups["rule"].Entries[0].Title != "Owner allows commits on main" {
		t.Errorf("a grouped section should answer its entries under each value, got %s", raw)
	}

	listed := answer.Sections["id("+bravo+"):expand(refs):as-list"].Entries
	if len(listed) != 1 || len(listed[0].Expanded) != 1 || listed[0].Expanded[0].ID != charlie {
		t.Errorf("an expanded section should carry each linked memory entry, got %s", raw)
	}

	cut := h.must("", "view", "--layout", "active:kind(note):n(2):group(by(seat)):as-grouped", "--json")
	if !strings.Contains(cut, `"more":{"skip":2}`) {
		t.Errorf("a grouped section cut by n should name the next page's skip, got %s", cut)
	}

	if strings.Contains(h.must("", "view", "--layout", "id("+bravo+"):as-list", "--json"), `"expanded"`) {
		t.Error("a section without expand should carry no expanded key")
	}

	if mcp := h.mcp(`{"name":"view","arguments":{"layout":"active:kind(rule,trap):group(by(kind)):as-grouped"}}`); !strings.Contains(mcp, `"groups"`) {
		t.Errorf("MCP view should answer the groups too, got %s", mcp)
	}
}

func TestAViewWarmsNothingAndMovesNoMark(t *testing.T) {
	h := goldenStore(t)
	h.must(hookPayload("", "", ""), "tool")

	cache := filepath.Join(h.cache, "agentic-memory", filepath.FromSlash(repoID))
	heat, _ := os.ReadFile(filepath.Join(cache, "heat.json"))
	marks, _ := filepath.Glob(filepath.Join(cache, "briefs", "*"))

	before := map[string]string{}
	for _, mark := range marks {
		raw, _ := os.ReadFile(mark)
		before[mark] = string(raw)
	}

	h.must("", "view", "--layout", "active:mine:as-list,top(3),active:as-bodies")
	h.must("", "view", "--layout", "active:as-list", "--json")

	after, _ := os.ReadFile(filepath.Join(cache, "heat.json"))
	if string(after) != string(heat) {
		t.Error("a view should leave heat.json as it was: a listing is not a use")
	}

	for mark, raw := range before {
		now, _ := os.ReadFile(mark)
		if string(now) != raw {
			t.Errorf("a view should move no seen or brief mark, %s changed", filepath.Base(mark))
		}
	}
}

func TestInDegreeLeavesSupersededLinksOut(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	cited := h.file("Older finding cited live", "cited")
	h.file("Live note citing it", "cites "+cited)
	h.clock.Advance(time.Minute)

	newer := h.file("Newer finding cited by a replaced note", "newer")
	replaced := h.file("Replaced note citing it", "cites "+newer)
	h.must("", "supersede", replaced, "--because", "wrong")

	if got := viewTitles(t, h, "active:rank(in-degree):n(1):as-list"); !slices.Equal(got, []string{"Older finding cited live"}) {
		t.Errorf("a superseded entry's link should not count toward in-degree, got %q", got)
	}
}
