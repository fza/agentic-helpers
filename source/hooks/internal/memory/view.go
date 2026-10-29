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
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/layout"
	"github.com/fza/agentic-helpers/source/hooks/internal/levenshtein"
)

// A view reads the store through a layout: colon-chained filters that
// intersect, then rank and paging, then a required render word, with
// comma-separated sections. A layout is caller input only; nothing stored is
// ever run as one. A view warms nothing and moves no mark: a listing is not a
// use.

const (
	rankDate     = "by(date)"
	rankHeat     = "heat"
	rankInDegree = "in-degree"
	rankSize     = "size"

	renderList    = "as-list"
	renderCounts  = "as-counts"
	renderBodies  = "as-bodies"
	renderGrouped = "as-grouped"
)

var (
	renderWords = []string{renderList, renderCounts, renderBodies, renderGrouped}
	rankWords   = []string{rankHeat, rankInDegree, rankSize, rankDate}
)

// shapeWords rank, page, group, widen, name and render a section's entries
// without choosing them.
var shapeWords = []string{"rank", "n", "skip", "group", "expand", "name", "brief"}

// groupFields are what group(by(...)) reads: sdd's kind, and seat where sdd
// has participant, since a seat files an entry; sdd's layer and type have no
// counterpart in memory.
var groupFields = []string{"kind", "seat", "shelf", "from"}

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
	renderAt int
	// group is the field as-grouped blocks the entries by; expand lists each
	// entry's links under it.
	group    string
	groupAt  int
	expand   bool
	expandAt int
	// returns is the reasons a returns filter shows under each entry, none
	// where the section has none.
	returns []string
	// sized lists each entry's attachments, their sizes and their total under
	// it, where the section filters by attached or ranks by size.
	sized bool
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

			// topic(L) opening a section is sdd's macro, the topic's entries
			// hottest first; anywhere else it only filters.
			if call.Name == "topic" && index == 0 {
				held.rank = rankHeat
			}

			if call.Name == "attached" {
				held.sized = true
			}

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
				nearest: levenshtein.Nearest(call.Name, viewWords())}
		}

		if err != nil {
			return viewSection{}, err
		}
	}

	if held.render == "" {
		return viewSection{}, &layoutRefusal{problem: "a section ends with a render word: " + orList(renderWords),
			token: section.Text, position: section.Position + len([]rune(section.Text)), nearest: renderList}
	}

	return held, held.consistent()
}

// consistent refuses a section whose words do not fit together: as-grouped
// needs a group, a group renders only as as-grouped, and as-counts shows no
// entry to list links under.
func (held viewSection) consistent() error {
	switch {
	case held.render == renderGrouped && held.group == "":
		return &layoutRefusal{problem: "as-grouped needs group(by(...)) in its section", token: renderGrouped,
			position: held.renderAt, nearest: "group(by(kind))"}
	case held.group != "" && held.render != renderGrouped:
		return &layoutRefusal{problem: "group renders with as-grouped", token: held.render, position: held.renderAt,
			nearest: renderGrouped}
	case held.expand && held.render == renderCounts:
		return &layoutRefusal{problem: "expand lists links under entries, which as-counts does not show",
			token: "expand(refs)", position: held.expandAt, nearest: renderList}
	}

	return nil
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

	held.render, held.renderAt = call.Name, call.Position

	return nil
}

func (held *viewSection) shape(call layout.Call) error {
	var err error

	switch call.Name {
	case "rank":
		held.rank, err = rankArgument(call)
		held.sized = held.sized || held.rank == rankSize
	case "n":
		held.limit, err = countArgument(call, 0)
		held.limited = true
	case "skip":
		held.skip, err = countArgument(call, 0)
	case "group":
		held.group, err = groupArgument(call)
		held.groupAt = call.Position
	case "expand":
		err = expandArgument(call)
		held.expand, held.expandAt = err == nil, call.Position
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
		if isWord && !arg.Quoted && (word == rankHeat || word == rankInDegree || word == rankSize) {
			return word, nil
		}

		if arg.Call != nil && callText(*arg.Call) == rankDate {
			return rankDate, nil
		}

		token := word
		if arg.Call != nil {
			token = callText(*arg.Call)
		}

		return "", &layoutRefusal{problem: "rank takes heat, in-degree, size or by(date)", token: token, position: arg.Position,
			nearest: levenshtein.Nearest(token, rankWords)}
	}

	return "", &layoutRefusal{problem: "rank takes heat, in-degree, size or by(date)", token: callText(call),
		position: call.Position, nearest: "rank(heat)"}
}

func groupArgument(call layout.Call) (string, error) {
	refusal := &layoutRefusal{problem: "group takes by(" + strings.Join(groupFields, "|") + ")", token: callText(call),
		position: call.Position, nearest: "group(by(kind))"}

	if len(call.Args) != 1 || call.Args[0].Call == nil || call.Args[0].Call.Name != "by" ||
		len(call.Args[0].Call.Args) != 1 {
		return "", refusal
	}

	field := call.Args[0].Call.Args[0]

	word, isWord := field.Word()
	if !isWord || field.Quoted || !slices.Contains(groupFields, word) {
		refusal.token, refusal.position, refusal.nearest = cmp.Or(word, callText(call)), field.Position,
			levenshtein.Nearest(word, groupFields)

		return "", refusal
	}

	return word, nil
}

func expandArgument(call layout.Call) error {
	if len(call.Args) == 1 {
		word, isWord := call.Args[0].Word()
		if isWord && !call.Args[0].Quoted && word == "refs" {
			return nil
		}
	}

	return &layoutRefusal{problem: "expand takes refs", token: callText(call), position: call.Position,
		nearest: "expand(refs)"}
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

	if section.group != "" {
		parts = append(parts, "group(by("+section.group+"))")
	}

	if section.expand {
		parts = append(parts, "expand(refs)")
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

	shown := len(kept)
	if section.limited {
		shown = min(section.skip+section.limit, len(kept))
	}

	ordered := best(kept, shown, scope.order(kept, section.rank))

	return ordered[min(section.skip, len(ordered)):], kept
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

	defer mem.presenting(ctx, req.Again)()

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
				if scope.mem.seen.hasBody(held) {
					lines = append(lines, pointer(held))

					continue
				}

				scope.mem.seen.markBody(held)
				lines = append(lines, showBlock(scope.here, scope.folded, held, scope.mem.options().RetiredBodyDays, false, false))
				lines = append(lines, scope.expandedLines(section, held, "  ")...)
			}
		case renderGrouped:
			for _, group := range scope.grouped(page, section.group) {
				lines = append(lines, "  "+group.value+":")
				for _, held := range group.entries {
					lines = append(lines, scope.entryLines(section, held, "    ", now)...)
				}
			}
		default:
			for _, held := range page {
				lines = append(lines, scope.entryLines(section, held, "  ", now)...)
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

// plain is whether the section prints an entry as a read printed it before:
// one carrying return reasons, expanded links or sizes holds what no earlier
// line held, so it prints in full however much of it stands in the context.
func (section viewSection) plain() bool {
	return len(section.returns) == 0 && !section.expand && !section.sized
}

// entryLines is one entry of a listed section: its line, then the reasons it
// returns and the links expand(refs) lists, indented beneath it.
func (scope *viewScope) entryLines(section viewSection, held *entry, indent string, now time.Time) []string {
	text := line(scope.here, held)
	if section.compact {
		text = shortLine(scope.here, held, scope.reader, now)
	}

	if section.plain() {
		text = scope.mem.lineOf(held, text)
	} else {
		scope.mem.seen.markLine(held)
	}

	lines := []string{indent + text}

	for _, reason := range scope.returnsOf(held, section.returns) {
		lines = append(lines, indent+"  "+reason.text())
	}

	if section.sized && len(held.attachments) > 0 {
		lines = append(lines, indent+"  "+attachedSizes(held))
	}

	return append(lines, scope.expandedLines(section, held, indent)...)
}

// expandedLines is each link of held, where its section expands them: a
// memory target as its short line, a graph target as its id, either followed
// by what became of it where that is not ok.
func (scope *viewScope) expandedLines(section viewSection, held *entry, indent string) []string {
	if !section.expand {
		return nil
	}

	var lines []string

	for _, link := range held.linkStates() {
		text := link.Target

		linked, found := scope.folded.entries[link.Target]
		if found {
			text = scope.mem.lineOf(linked, shortLine(scope.here, linked, scope.reader, scope.mem.now()))
		}

		if link.State != "ok" {
			text += "  (" + link.State + ")"
		}

		lines = append(lines, indent+"  → "+text)
	}

	return lines
}

// expandedJSON is the memory entries held links to, where its section
// expands them.
func (scope *viewScope) expandedJSON(section viewSection, held *entry) []jsonEntry {
	if !section.expand {
		return nil
	}

	expanded := []jsonEntry{}

	for _, target := range held.links() {
		linked, found := scope.folded.entries[target]
		if found {
			expanded = append(expanded, scope.mem.entryJSON(scope.here, scope.folded, linked, false))
		}
	}

	return expanded
}

type entryGroup struct {
	value   string
	entries []*entry
}

// grouped blocks the page by a field's value, each block in the section's
// rank and the blocks in the order their first entry ranks.
func (scope *viewScope) grouped(page []*entry, field string) []entryGroup {
	var groups []entryGroup

	at := map[string]int{}

	for _, held := range page {
		value := scope.groupValue(held, field)

		index, found := at[value]
		if !found {
			index = len(groups)
			at[value] = index
			groups = append(groups, entryGroup{value: value})
		}

		groups[index].entries = append(groups[index].entries, held)
	}

	return groups
}

func (scope *viewScope) groupValue(held *entry, field string) string {
	switch field {
	case "kind":
		return held.kind
	case "seat":
		return held.seat
	case "shelf":
		return scope.mem.shelfName(scope.mem.entryShelf(held))
	default:
		return held.from
	}
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
	Entries []jsonEntry `json:"entries,omitempty"`
	More    *jsonMore   `json:"more,omitempty"`
}

type jsonGroupedSection struct {
	Groups map[string]jsonGroup `json:"groups,omitempty"`
	More   *jsonMore            `json:"more,omitempty"`
}

type jsonGroup struct {
	Entries []jsonEntry `json:"entries,omitempty"`
}

type jsonCountsSection struct {
	Topics map[string]jsonTopicCount `json:"topics,omitempty"`
}

// viewJSON is one object: the notice, then each section under its name, in
// the order the layout gives them. A section with nothing in it is an empty
// object and leaves the answer, as an empty key does everywhere, and so does
// sections where every section is empty.
func (scope *viewScope) viewJSON(sections []viewSection) result {
	var written bytes.Buffer

	for _, section := range sections {
		page, kept := scope.run(section)

		var value any

		switch section.render {
		case renderCounts:
			topics := map[string]jsonTopicCount{}
			for _, row := range scope.counts(kept) {
				topics[row.topic] = jsonTopicCount{Count: row.count, Heat: row.heat}
			}

			value = jsonCountsSection{Topics: topics}
		case renderGrouped:
			grouped := jsonGroupedSection{Groups: map[string]jsonGroup{}, More: moreAfter(section, page, kept)}
			for _, group := range scope.grouped(page, section.group) {
				entries := make([]jsonEntry, 0, len(group.entries))
				for _, held := range group.entries {
					entries = append(entries, scope.entryObject(section, held))
				}

				grouped.Groups[group.value] = jsonGroup{Entries: entries}
			}

			value = grouped
		default:
			listed := jsonListSection{Entries: make([]jsonEntry, 0, len(page)), More: moreAfter(section, page, kept)}
			for _, held := range page {
				listed.Entries = append(listed.Entries, scope.entryObject(section, held))
			}

			value = listed
		}

		name, _ := json.Marshal(section.title())

		encoded, err := json.Marshal(value)
		if err != nil {
			return refused("encoding the answer: %v", err)
		}

		if string(encoded) == "{}" {
			continue
		}

		if written.Len() > 0 {
			written.WriteString(",")
		}

		written.Write(name)
		written.WriteString(":")
		written.Write(encoded)
	}

	notice, _ := json.Marshal(filedNotice)

	answer := `{"notice":` + string(notice)
	if written.Len() > 0 {
		answer += `,"sections":{` + written.String() + "}"
	}

	return done(answer + "}")
}

// entryObject is one entry of a listed section as JSON, with the reasons it
// returns and the entries expand(refs) adds.
func (scope *viewScope) entryObject(section viewSection, held *entry) jsonEntry {
	object := scope.mem.entryJSON(scope.here, scope.folded, held, section.render == renderBodies)
	if section.plain() {
		object = scope.mem.objectOf(held, object)
	}
	for _, reason := range scope.returnsOf(held, section.returns) {
		object.Returns = append(object.Returns, reason.json())
	}

	object.Expanded = scope.expandedJSON(section, held)

	return object
}

// moreAfter is the paging a section cut leaves, nil on its last page.
func moreAfter(section viewSection, page []*entry, kept []*entry) *jsonMore {
	next := min(section.skip, len(kept)) + len(page)
	if next < len(kept) {
		return &jsonMore{Skip: next}
	}

	return nil
}

// attachedSizes is an entry's attachments with their sizes, and their total.
func attachedSizes(held *entry) string {
	files := make([]string, 0, len(held.attachments))
	for _, attached := range held.attachments {
		files = append(files, fmt.Sprintf("%s (%s)", attached.Name, size(attached.Size)))
	}

	return fmt.Sprintf("attached: %s; total %s", strings.Join(files, ", "), size(attachedBytes(held)))
}

func attachedBytes(held *entry) int64 {
	var total int64
	for _, attached := range held.attachments {
		total += attached.Size
	}

	return total
}
