package layout_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/layout"
)

// shape spells a parsed layout back with its structure made plain: each
// function as name[args], a quoted string as «text», sections split by |.
func shape(sections []layout.Section) string {
	var spelled string

	for index, section := range sections {
		if index > 0 {
			spelled += "|"
		}

		for at, call := range section.Calls {
			if at > 0 {
				spelled += ":"
			}

			spelled += callShape(call)
		}
	}

	return spelled
}

func callShape(call layout.Call) string {
	spelled := call.Name
	if !call.Parens {
		return spelled
	}

	spelled += "["

	for index, arg := range call.Args {
		if index > 0 {
			spelled += ","
		}

		if arg.Quoted {
			spelled += "«" + arg.Text + "»"
		} else {
			spelled += callShape(*arg.Call)
		}
	}

	return spelled + "]"
}

func TestParseReadsSddLayouts(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{text: "top(20)", want: "top[20]"},
		{text: "active:kind(plan):rank(heat(exp-14d)):n(10):as-list", want: "active:kind[plan]:rank[heat[exp-14d]]:n[10]:as-list"},
		{text: `topic("infrastructure/cli"):rank(by(date)):n(20):as-list`, want: "topic[«infrastructure/cli»]:rank[by[date]]:n[20]:as-list"},
		{text: `active:participant("Jonathan Philipp"):as-list`, want: "active:participant[«Jonathan Philipp»]:as-list"},
		{text: "decisions,signals,participants", want: "decisions|signals|participants"},
		{text: "top(20):not(kind(contract,aspiration))", want: "top[20]:not[kind[contract,aspiration]]"},
		{text: `name("say \"hi\""):as-list`, want: `name[«say "hi"»]:as-list`},
		{text: "seat(главная):as-list", want: "seat[главная]:as-list"},
		{text: "inbox():as-list", want: "inbox[]:as-list"},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			sections, err := layout.Parse(tc.text)
			if err != nil {
				t.Fatalf("a layout of sdd's grammar should parse: %v", err)
			}

			if shape(sections) != tc.want {
				t.Errorf("the layout should parse into its functions and arguments, got %s", shape(sections))
			}
		})
	}
}

func TestParseKeepsWhereEachPartStands(t *testing.T) {
	sections, err := layout.Parse(`active:as-list,seat("a b"):n(3):as-counts`)
	if err != nil {
		t.Fatal(err)
	}

	second := sections[1]
	if second.Text != `seat("a b"):n(3):as-counts` || second.Position != 16 {
		t.Error("a section should keep its own text and where it starts")
	}

	if second.Calls[1].Position != 28 || second.Calls[0].Args[0].Position != 21 {
		t.Error("a function and an argument should keep where they start, counted in characters from 1")
	}
}

func TestParseRefusesWhatIsNotTheGrammar(t *testing.T) {
	cases := []struct {
		text     string
		token    string
		position int
		expected string
	}{
		{text: "", token: layout.EndOfLayout, position: 1, expected: "a word"},
		{text: "active:", token: layout.EndOfLayout, position: 8, expected: "a word"},
		{text: "active: as-list", token: " ", position: 8, expected: "a word"},
		{text: "active,,as-list", token: ",", position: 8, expected: "a word"},
		{text: "kind(trap", token: layout.EndOfLayout, position: 10, expected: `"," or ")"`},
		{text: "kind(trap note)", token: " ", position: 10, expected: `"," or ")"`},
		{text: `name("open)`, token: layout.EndOfLayout, position: 12, expected: `the closing '"' of the string at position 6`},
		{text: "active:as-list)", token: ")", position: 15, expected: `":", "," or the end of the layout`},
		{text: "seat(a/b)", token: "/", position: 7, expected: `"," or ")"`},
		{text: "active:as-list x", token: " ", position: 15, expected: `":", "," or the end of the layout`},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			_, err := layout.Parse(tc.text)

			var problem *layout.Error
			if !errors.As(err, &problem) {
				t.Fatalf("a layout outside the grammar should be refused with its token, got %v", err)
			}

			if problem.Token != tc.token || problem.Position != tc.position || problem.Expected != tc.expected {
				t.Errorf("the refusal should name the token, where it stands and what was expected, got %+v", problem)
			}
		})
	}
}

func TestArgWord(t *testing.T) {
	sections, err := layout.Parse(`f(bare,"quoted",nested(x),empty())`)
	if err != nil {
		t.Fatal(err)
	}

	var words []string

	for _, arg := range sections[0].Calls[0].Args {
		word, isWord := arg.Word()
		if isWord {
			words = append(words, word)
		}
	}

	if !slices.Equal(words, []string{"bare", "quoted"}) {
		t.Error("a bare word and a quoted string should read as words, a function with parentheses not")
	}
}
