package memory

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/layout"
)

// A view reads the store through a layout: colon-chained filters that
// intersect, then rank and paging, then a required render word, with
// comma-separated sections. A layout is caller input only; nothing stored is
// ever run as one. A view warms nothing and moves no mark: a listing is not a
// use.

const (
	rankDate = "by(date)"
	rankHeat = "heat"

	renderList   = "as-list"
	renderCounts = "as-counts"
	renderBodies = "as-bodies"
)

var renderWords = []string{renderList, renderCounts, renderBodies}

// shapeWords rank, page, name and render a section's entries without
// choosing them.
var shapeWords = []string{"rank", "n", "skip", "name", "brief"}

const macroTop = "top"

// briefName is the brief's fixed layout, which --explain prints and no
// --layout runs: the brief has its own command.
const briefName = "brief"

// layoutRefusal is a layout refused: what is wrong, the token at fault, where
// it stands, and the nearest valid word.
type layoutRefusal struct {
	problem  string
	token    string
	position int
	nearest  string
}

func (refusal *layoutRefusal) Error() string {
	return fmt.Sprintf("layout: %s: %q at position %d; nearest valid word: %s", refusal.problem, refusal.token,
		refusal.position, refusal.nearest)
}

// viewSection is one compiled section.
type viewSection struct {
	text     string
	position int
	name     string
	filters  []filter
	rank     string
	limit    int
	limited  bool
	skip     int
	compact  bool
	render   string
	// returns is the reasons a returns filter shows under each entry, none
	// where the section has none.
	returns []string
}

func viewWords() []string {
	words := filterNames()
	words = append(words, shapeWords...)
	words = append(words, renderWords...)

	return append(words, macroTop)
}

// compileLayout reads a layout into its sections, refusing anything outside
// the grammar or the vocabulary; nothing is repaired.
func (scope *viewScope) compileLayout(text string) ([]viewSection, error) {
	parsed, err := layout.Parse(text)
	if err != nil {
		var problem *layout.Error
		if errors.As(err, &problem) {
			return nil, fmt.Errorf("layout does not parse: %s", problem.Error())
		}

		return nil, err
	}

	sections := make([]viewSection, 0, len(parsed))
	named := map[string]bool{}

	for _, section := range parsed {
		compiled, err := scope.compileSection(section)
		if err != nil {
			return nil, err
		}

		if named[compiled.title()] {
			return nil, &layoutRefusal{problem: "two sections share a name", token: section.Text,
				position: section.Position, nearest: `name("...")`}
		}

		named[compiled.title()] = true
		sections = append(sections, compiled)
	}

	return sections, nil
}

func (section viewSection) title() string {
	return cmp.Or(section.name, section.text)
}

func (scope *viewScope) compileSection(section layout.Section) (viewSection, error) {
	held := viewSection{text: section.Text, position: section.Position, rank: rankDate}
	last := len(section.Calls) - 1

	scope.inbox = ""

	for _, call := range section.Calls {
		if call.Name == "inbox" && len(call.Args) == 1 {
			scope.inbox, _ = call.Args[0].Word()
		}
	}

	for index, call := range section.Calls {
		_, isFilter := filterWordNamed(call.Name)

		var err error

		switch {
		case call.Name == macroTop && index == 0:
			err = held.top(call)
		case call.Name == macroTop:
			err = &layoutRefusal{problem: "top is a macro, read only where a section starts", token: call.Name,
				position: call.Position, nearest: "n"}
		case isFilter || call.Name == "not":
			var compiled filter

			compiled, err = scope.compileFilter(call)
			held.filters = append(held.filters, compiled)

			if call.Name == "returns" {
				held.returns = returnKinds
				if len(call.Args) > 0 {
					held.returns = nil
					for _, arg := range call.Args {
						kind, _ := arg.Word()
						held.returns = append(held.returns, kind)
					}
				}
			}
		case slices.Contains(renderWords, call.Name):
			err = held.terminate(call, index == last)
		case slices.Contains(shapeWords, call.Name):
			err = held.shape(call)
		default:
			err = &layoutRefusal{problem: "unknown word", token: call.Name, position: call.Position,
				nearest: nearestWord(call.Name, viewWords())}
		}

		if err != nil {
			return viewSection{}, err
		}
	}

	if held.render == "" {
		return viewSection{}, &layoutRefusal{problem: "a section ends with a render word: " + orList(renderWords),
			token: section.Text, position: section.Position + len([]rune(section.Text)), nearest: renderList}
	}

	return held, nil
}

// top is the macro top(N): live entries, hottest first, the first N, as a
// list; later words override it.
func (held *viewSection) top(call layout.Call) error {
	count, err := countArgument(call, 1)
	if err != nil {
		return err
	}

	held.filters = append(held.filters, filter{spelled: "active", keep: func(_ *viewScope, entry *entry) bool {
		return entry.status == statusLive
	}})
	held.rank, held.limit, held.limited, held.render = rankHeat, count, true, renderList

	return nil
}

func (held *viewSection) terminate(call layout.Call, last bool) error {
	switch {
	case call.Parens:
		return &layoutRefusal{problem: call.Name + " takes no arguments", token: callText(call), position: call.Position,
			nearest: call.Name}
	case !last:
		return &layoutRefusal{problem: "a render word ends its section", token: call.Name, position: call.Position,
			nearest: call.Name}
	}

	held.render = call.Name

	return nil
}

func (held *viewSection) shape(call layout.Call) error {
	var err error

	switch call.Name {
	case "rank":
		held.rank, err = rankArgument(call)
	case "n":
		held.limit, err = countArgument(call, 0)
		held.limited = true
	case "skip":
		held.skip, err = countArgument(call, 0)
	case "name":
		if len(call.Args) != 1 {
			return &layoutRefusal{problem: "name takes one quoted title", token: callText(call), position: call.Position,
				nearest: `name("title")`}
		}

		title, isWord := call.Args[0].Word()
		if !isWord || title == "" {
			return &layoutRefusal{problem: "name takes one quoted title", token: callText(call),
				position: call.Args[0].Position, nearest: `name("title")`}
		}

		held.name = title
	case "brief":
		if call.Parens {
			return &layoutRefusal{problem: "brief takes no arguments", token: callText(call), position: call.Position,
				nearest: "brief"}
		}

		held.compact = true
	}

	return err
}

func rankArgument(call layout.Call) (string, error) {
	if len(call.Args) == 1 {
		arg := call.Args[0]

		word, isWord := arg.Word()
		if isWord && !arg.Quoted && word == rankHeat {
			return rankHeat, nil
		}

		if arg.Call != nil && callText(*arg.Call) == rankDate {
			return rankDate, nil
		}

		token := word
		if arg.Call != nil {
			token = callText(*arg.Call)
		}

		return "", &layoutRefusal{problem: "rank takes heat or by(date)", token: token, position: arg.Position,
			nearest: nearestWord(token, []string{rankHeat, rankDate})}
	}

	return "", &layoutRefusal{problem: "rank takes heat or by(date)", token: callText(call), position: call.Position,
		nearest: "rank(heat)"}
}

func countArgument(call layout.Call, least int) (int, error) {
	kind := "a number of 0 or more"
	if least > 0 {
		kind = "a positive number"
	}

	if len(call.Args) != 1 {
		return 0, &layoutRefusal{problem: call.Name + " takes " + kind, token: callText(call), position: call.Position,
			nearest: call.Name + "(10)"}
	}

	word, _ := call.Args[0].Word()

	count, err := strconv.Atoi(word)
	if err != nil || count < least {
		return 0, &layoutRefusal{problem: call.Name + " takes " + kind, token: cmp.Or(word, callText(call)),
			position: call.Args[0].Position, nearest: call.Name + "(10)"}
	}

	return count, nil
}

// explain spells a compiled section back whole, its macro expanded.
func (section viewSection) explain() string {
	parts := make([]string, 0, len(section.filters)+6)
	for _, held := range section.filters {
		parts = append(parts, held.spelled)
	}

	parts = append(parts, "rank("+section.rank+")")

	if section.skip > 0 {
		parts = append(parts, fmt.Sprintf("skip(%d)", section.skip))
	}

	if section.limited {
		parts = append(parts, fmt.Sprintf("n(%d)", section.limit))
	}

	if section.name != "" {
		parts = append(parts, "name("+strconv.Quote(section.name)+")")
	}

	if section.compact {
		parts = append(parts, "brief")
	}

	return strings.Join(append(parts, section.render), ":")
}

// run is the section's entries: every kept entry ranked, the page its skip and
// n cut from them, and how many were kept.
func (scope *viewScope) run(section viewSection) ([]*entry, []*entry) {
	kept := scope.selected(section.filters)

	if section.rank == rankHeat {
		scope.byHeat(kept)
	} else {
		byDate(kept)
	}

	page := kept[min(section.skip, len(kept)):]
	if section.limited {
		page = page[:min(section.limit, len(page))]
	}

	return page, kept
}

func (mem *memory) view(ctx context.Context, req request) result {
	if (req.Layout == "") == (req.Explain == "") {
		return refused("view takes --layout '<layout>' or --explain '<layout>'|brief")
	}

	if req.Layout == briefName {
		return refused("brief is the brief's own layout: %s brief prints it, %s view --explain brief shows it", self, self)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	reader, _ := mem.seated(ctx)
	scope := mem.scope(ctx, folded, reader)

	text := cmp.Or(req.Layout, req.Explain)
	if req.Explain == briefName {
		text = briefLayout(mem.options().BriefNoteLimit)
	}

	sections, err := scope.compileLayout(text)
	if err != nil {
		return refused("%v", err)
	}

	if req.Explain != "" {
		lines := make([]string, 0, len(sections)+1)
		if req.Explain == briefName {
			lines = append(lines, briefParts)
		}

		explained := make([]string, 0, len(sections))
		for _, section := range sections {
			explained = append(explained, section.explain())
		}

		return done(strings.Join(append(lines, strings.Join(explained, ",\n")), "\n"))
	}

	if req.JSON {
		return scope.viewJSON(sections)
	}

	return done(scope.viewText(sections))
}

func (scope *viewScope) viewText(sections []viewSection) string {
	var lines []string

	now := scope.mem.now()

	for _, section := range sections {
		page, kept := scope.run(section)
		lines = append(lines, section.title()+":")

		switch section.render {
		case renderCounts:
			counts := scope.counts(kept)
			for _, row := range counts {
				lines = append(lines, fmt.Sprintf("  %s %d, heat %.2f", row.topic, row.count, row.heat))
			}

			if len(counts) == 0 {
				lines = append(lines, "  no entry")
			}

			continue
		case renderBodies:
			for _, held := range page {
				lines = append(lines, showBlock(scope.here, scope.folded, held, scope.mem.options().RetiredBodyDays, false))
			}
		default:
			for _, held := range page {
				if section.compact {
					lines = append(lines, "  "+shortLine(scope.here, held, scope.reader, now))
				} else {
					lines = append(lines, "  "+line(scope.here, held))
				}

				for _, reason := range scope.returnsOf(held, section.returns) {
					lines = append(lines, "    "+reason.text())
				}
			}
		}

		if len(page) == 0 {
			lines = append(lines, "  no entry")
		}

		next := min(section.skip, len(kept)) + len(page)
		if next < len(kept) {
			lines = append(lines, fmt.Sprintf("  %d more: skip(%d)", len(kept)-next, next))
		}
	}

	return strings.Join(lines, "\n")
}

type topicCount struct {
	topic string
	count int
	heat  float64
}

// counts is every topic the entries carry, with how many carry it and their
// heat summed, the most carried first.
func (scope *viewScope) counts(entries []*entry) []topicCount {
	byTopic := map[string]*topicCount{}

	for _, held := range entries {
		for _, topic := range scope.folded.topicsOf(held) {
			row, found := byTopic[topic]
			if !found {
				row = &topicCount{topic: topic}
				byTopic[topic] = row
			}

			row.count++
			row.heat += scope.heat(held)
		}
	}

	rows := make([]topicCount, 0, len(byTopic))
	for _, row := range byTopic {
		rows = append(rows, *row)
	}

	slices.SortFunc(rows, func(a, b topicCount) int {
		return cmp.Or(cmp.Compare(b.count, a.count), strings.Compare(a.topic, b.topic))
	})

	return rows
}

type jsonTopicCount struct {
	Count int     `json:"count"`
	Heat  float64 `json:"heat"`
}

type jsonListSection struct {
	Entries []jsonEntry `json:"entries"`
	More    *jsonMore   `json:"more,omitempty"`
}

type jsonCountsSection struct {
	Topics map[string]jsonTopicCount `json:"topics"`
}

// viewJSON is one object: the notice, then each section under its name, in
// the order the layout gives them.
func (scope *viewScope) viewJSON(sections []viewSection) result {
	var answer bytes.Buffer

	notice, _ := json.Marshal(filedNotice)
	answer.WriteString(`{"notice":` + string(notice) + `,"sections":{`)

	for index, section := range sections {
		page, kept := scope.run(section)

		var value any

		switch section.render {
		case renderCounts:
			topics := map[string]jsonTopicCount{}
			for _, row := range scope.counts(kept) {
				topics[row.topic] = jsonTopicCount{Count: row.count, Heat: row.heat}
			}

			value = jsonCountsSection{Topics: topics}
		default:
			listed := jsonListSection{Entries: make([]jsonEntry, 0, len(page))}
			for _, held := range page {
				object := scope.mem.entryJSON(scope.here, scope.folded, held, section.render == renderBodies)
				for _, reason := range scope.returnsOf(held, section.returns) {
					object.Returns = append(object.Returns, reason.json())
				}

				listed.Entries = append(listed.Entries, object)
			}

			if next := min(section.skip, len(kept)) + len(page); next < len(kept) {
				listed.More = &jsonMore{Skip: next}
			}

			value = listed
		}

		name, _ := json.Marshal(section.title())

		encoded, err := json.Marshal(value)
		if err != nil {
			return refused("encoding the answer: %v", err)
		}

		if index > 0 {
			answer.WriteString(",")
		}

		answer.Write(name)
		answer.WriteString(":")
		answer.Write(encoded)
	}

	answer.WriteString("}}")

	return done(answer.String())
}
