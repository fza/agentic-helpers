package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/levenshtein"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/internal/span"
)

const (
	defaultFrom  = "agent"
	defaultLimit = 10
	rrfK         = 60
	aidLimit     = 5 * time.Second
)

var sources = []string{"user", "agent", "tool"}

func (mem *memory) seated(ctx context.Context) (string, bool) {
	name, _ := mem.ownSeat(ctx)

	return name, name != ""
}

func (mem *memory) newEntry(ctx context.Context, req request) result {
	name, claim := mem.ownSeat(ctx)
	if name == "" {
		return refused("%s", ErrNoSeat)
	}

	kind := cmp.Or(req.Kind, kindNote)

	from := cmp.Or(req.From, defaultFrom)
	if !slices.Contains(sources, from) {
		return refused("--from takes user, agent or tool, not %q", from)
	}

	why := kindRefusal(kind, from)
	if why != "" {
		return refused("%s", why)
	}

	if len(req.Until) > 0 && kind == kindRule {
		return refused("%v", ErrRuleUntil)
	}

	if len(req.Items) > 0 && kind != kindTodo {
		return refused("%v", ErrItemKind)
	}

	var due time.Time

	if strings.TrimSpace(req.Due.text) != "" {
		if !takesDue(kind) {
			return refused("%v, not a %s", ErrDueKind, kind)
		}

		parsed, err := parseDue(req.Due.text)
		if err != nil {
			return refused("%v", err)
		}

		due = parsed.UTC()
	}

	modeRefusal := itemsModeRefusal(req.ItemsMode)
	if modeRefusal != "" {
		return refused("%s", modeRefusal)
	}

	declaredLife, err := lifeFor(kind, req)
	if err != nil {
		return refused("%v", err)
	}

	forbids, err := forbidsFor(kind, req.Forbids)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	until, untilHashes, err := mem.untilFor(ctx, here, req.Until)
	if err != nil {
		return refused("%v", err)
	}

	var untilHold *hold
	if slices.Contains(until, "session") {
		untilHold = &hold{Seat: mem.seatKey(name), Claimed: claim.Claimed}
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	id, err := mem.newID(folded)
	if err != nil {
		return refused("%v", err)
	}

	var topics []string

	for _, topic := range req.Topics {
		topic = normalTopic(topic)
		if topic != "" && !slices.Contains(topics, topic) {
			topics = append(topics, topic)
		}
	}

	shared, err := mem.recipientsFor(req.For, name)
	if err != nil {
		return refused("%v", err)
	}

	links := make([]string, 0, len(req.Links))

	for _, target := range req.Links {
		resolved, err := mem.resolveTarget(ctx, folded, target)
		if err != nil {
			return refused("%v", err)
		}

		links = append(links, resolved)
	}

	toolTargets, err := mem.toolTargets(ctx, req.Tools)
	if err != nil {
		return refused("%v", err)
	}

	links = append(links, toolTargets...)

	textLinks, unlinked := mem.textLinks(ctx, folded, req.Title+"\n"+req.Body, id)

	var failures []string

	for _, absorbed := range req.Absorbs {
		held, found := folded.entries[absorbed]
		if !found {
			failures = append(failures, fmt.Sprintf("%s: %s", absorbed, ErrNoEntry))

			continue
		}

		// The successor is the entry being filed, which the log does not hold yet.
		err = mem.supersedable("")(folded, held)
		if err != nil {
			failures = append(failures, err.Error())
		}

		switch {
		case holdsItems(held) && req.ItemsMode == "":
			failures = append(failures, absorbed+" "+ErrItemsUnnamed.Error())
		case holdsItems(held) && req.ItemsMode == itemsCarry && kind != kindTodo:
			failures = append(failures, absorbed+": --items carry moves a todolist onto a todo; file this as --kind todo")
		}
	}

	if len(failures) > 0 {
		return refused("nothing filed:\n  %s", strings.Join(failures, "\n  "))
	}

	prepared, err := mem.prepareFiles(ctx, folded, nil, req.Attach)
	if err != nil {
		return refused("%v", err)
	}

	filed := newPayload{
		Title: strings.TrimSpace(req.Title), Body: strings.TrimRight(req.Body, "\n"), Topics: topics,
		Kind: kind, From: from, Session: mem.env.SessionID, Commit: here.commit, Branch: here.branch,
		Anchors: anchorsFor(here.root, req.Title+"\n"+req.Body, req.About), For: shared,
		TextLinks: textLinks, Links: links, Until: until, UntilHashes: untilHashes, UntilHold: untilHold,
		When: declaredLife.when, On: declaredLife.on, Proof: declaredLife.proof, Forbids: forbids,
		About: declaredPaths(here.root, req.About), Due: due,
	}

	raw, err := json.Marshal(filed)
	if err != nil {
		return refused("encoding the entry: %v", err)
	}

	at := mem.now()

	events := []memorylog.Event{{Kind: memorylog.KindNew, Entry: id, Time: at, Seat: name, Payload: raw}}

	absorbing, err := json.Marshal(supersedePayload{By: id})
	if err != nil {
		return refused("encoding the supersede: %v", err)
	}

	for _, absorbed := range slices.Compact(slices.Clone(req.Absorbs)) {
		events = append(events, memorylog.Event{
			Kind: memorylog.KindSupersede, Entry: absorbed, Time: at, Seat: name, Target: id, Payload: absorbing,
		})
	}

	var moved []itemPayload

	renamed := []string{}

	if req.ItemsMode == itemsCarry {
		for _, absorbed := range slices.Compact(slices.Clone(req.Absorbs)) {
			taken := func(id string) bool {
				return slices.ContainsFunc(moved, func(one itemPayload) bool { return one.ID == id })
			}

			more, names, err := carried(folded.entries[absorbed], taken)
			if err != nil {
				return refused("%v", err)
			}

			moved, renamed = append(moved, more...), append(renamed, names...)
		}
	}

	ownItems, err := addItems(&entry{items: itemsOf(moved)}, req.Items)
	if err != nil {
		return refused("%v", err)
	}

	listed, err := itemEvents(id, name, at, append(moved, ownItems...))
	if err != nil {
		return refused("%v", err)
	}

	events = append(events, listed...)

	attached, err := mem.attachEvents(id, name, at, prepared)
	if err != nil {
		return refused("%v", err)
	}

	events = append(events, attached...)

	err = mem.appendEvents(ctx, events...)
	if err != nil {
		return refused("%v", err)
	}

	existing := mem.topicCounts(folded)

	err = folded.apply(memorylog.Row{Kind: memorylog.KindNew, Entry: id, Time: at, Seat: name}, raw)
	if err != nil {
		return refused("%v", err)
	}

	mem.annotate(folded)
	mem.annotateLinks(ctx, folded)

	held := folded.entries[id]
	mem.warm(folded, 0, id)

	lines := []string{filedLine(here, held)}
	for _, file := range prepared {
		lines = append(lines, attachedLine(id, file.attachPayload))
	}

	if len(req.Absorbs) > 0 {
		lines = append(lines, fmt.Sprintf("superseded %s by %s", strings.Join(slices.Compact(slices.Clone(req.Absorbs)), ", "), id))
	}

	if len(unlinked) > 0 {
		lines = append(lines, "not linked (no such entry): "+strings.Join(unlinked, ", "))
	}

	listedItems := len(moved) + len(ownItems)
	if listedItems > 0 {
		ids := make([]string, 0, listedItems)
		for _, one := range append(slices.Clone(moved), ownItems...) {
			ids = append(ids, "#"+one.ID)
		}

		lines = append(lines, fmt.Sprintf("items %s: %s", id, strings.Join(ids, " ")))
	}

	lines = append(lines, renamed...)

	counted := listLines(req.Body)
	if kind == kindTodo && counted > 0 {
		lines = append(lines, fmt.Sprintf("%d list lines in the text; steps go into --item", counted))
	}

	lines = append(lines, mem.filingNotes(ctx, folded, held, existing, req.Absorbs)...)
	lines = append(lines, mem.forbidden(folded, held, name)...)

	if len(prepared) > 0 {
		lines = append(lines, space.Line(mem.recordAttached(ctx, prepared)))
	}

	return done(strings.Join(lines, "\n"))
}

// filedLine opens the reply to a filing with the new id and the entry's
// flags alone: the title, seat, kind, source, time and commit are what the
// caller just sent or cannot act on, while a flag carries the normalized or
// computed form of an input, a due time or a link target already closed.
func filedLine(here tree, held *entry) string {
	return withFlags("filed "+held.id, append(flagsOf(here, held, held.flags()), declared(held)...))
}

// absorbed are the entries the filing superseded, which no hint offers again.
func (mem *memory) filingNotes(ctx context.Context, folded *state, held *entry, existing map[string]int,
	absorbed []string,
) []string {
	var notes []string

	limit := mem.options().EntryMaxWords

	count := words(held.body)
	if limit > 0 && count > limit {
		notes = append(notes, fmt.Sprintf("note: the body has %d words, past the %d an entry should hold (entry_max_words)",
			count, limit))
	}

	titleWords := words(held.title)
	if titleWords < 3 || titleWords > 10 {
		notes = append(notes, fmt.Sprintf("note: title has %s, a title takes 3-10; %s retitle %s '<title>'",
			plural(titleWords, "word"), self, held.id))
	}

	if len(held.topics) == 0 {
		notes = append(notes, fmt.Sprintf("note: no topic; %s tag %s <topic>...", self, held.id))
	}

	for _, topic := range held.topics {
		if _, known := existing[topic]; known {
			continue
		}

		near := nearTopics(topic, existing)
		if len(near) == 0 {
			continue
		}

		var named []string
		for _, other := range near {
			named = append(named, fmt.Sprintf("%s (%d)", other, existing[other]))
		}

		notes = append(notes, fmt.Sprintf("new topic %s; near: %s", topic, strings.Join(named, ", ")),
			fmt.Sprintf("  → %s merge %s %s", self, topic, near[0]))
	}

	for _, similar := range mem.similar(ctx, folded, held) {
		if slices.Contains(absorbed, similar.id) {
			continue
		}

		notes = append(notes, fmt.Sprintf("similar: %s %s (%.2f)", similar.id, similar.title, similar.score))
	}

	return notes
}

type scored struct {
	id    string
	title string
	score float64
}

// similar is up to three live entries of the same seat that stand out from the
// rest as close to the new one: by meaning where the embedder answers, by
// shared words where it does not, since filing never waits on it.
func (mem *memory) similar(ctx context.Context, folded *state, held *entry) []scored {
	var peers []*entry

	for _, other := range folded.entryList() {
		if other.id != held.id && other.seat == held.seat && other.status == statusLive {
			peers = append(peers, other)
		}
	}

	if len(peers) == 0 {
		return nil
	}

	bounded, cancel := context.WithTimeout(ctx, aidLimit)
	defer cancel()

	var found []scored

	scores, err := mem.semantic(bounded, folded, held.title+"\n"+held.body)
	if err == nil && len(peers) >= 5 {
		found = standouts(peers, scores)
	} else {
		mine := wordSet(held.title + " " + held.body)
		for _, other := range peers {
			score := jaccard(mine, wordSet(other.title+" "+other.body))
			if score >= 0.4 {
				found = append(found, scored{id: other.id, title: other.title, score: score})
			}
		}
	}

	slices.SortFunc(found, func(a, b scored) int { return cmp.Compare(b.score, a.score) })

	return found[:min(3, len(found))]
}

// similarFloor is the least score by meaning a similar entry needs, however
// far it stands out: below it, filing replies named entries agents almost never
// superseded or linked (4 of 101 hints over three days of transcripts).
const similarFloor = 0.6

// standouts is the peers whose score by meaning stands two deviations above the
// rest and reaches similarFloor.
func standouts(peers []*entry, scores map[string]float32) []scored {
	values := make([]float64, 0, len(peers))
	for _, other := range peers {
		values = append(values, float64(scores[other.id]))
	}

	floor := max(similarFloor, mean(values)+2*deviation(values))

	var found []scored

	for _, other := range peers {
		score := float64(scores[other.id])
		if score >= floor {
			found = append(found, scored{id: other.id, title: other.title, score: score})
		}
	}

	return found
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, value := range values {
		sum += value
	}

	return sum / float64(len(values))
}

func deviation(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	centre := mean(values)
	sum := 0.0

	for _, value := range values {
		sum += (value - centre) * (value - centre)
	}

	return math.Sqrt(sum / float64(len(values)))
}

var wordPattern = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)

// wordSet is the words two entries are compared by: each character of an
// unspaced script on its own, and every other run of three letters or digits.
func wordSet(text string) map[string]bool {
	set := map[string]bool{}

	spaced := strings.Map(func(char rune) rune {
		if isUnspaced(char) {
			set[string(char)] = true

			return ' '
		}

		return char
	}, strings.ToLower(text))

	for _, word := range wordPattern.FindAllString(spaced, -1) {
		set[word] = true
	}

	return set
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	shared := 0
	for word := range a {
		if b[word] {
			shared++
		}
	}

	return float64(shared) / float64(len(a)+len(b)-shared)
}

// nearTopics is up to three existing topics a new name probably means: a small
// spelling distance, or one name inside the other.
func nearTopics(topic string, existing map[string]int) []string {
	var near []string

	for other := range existing {
		distance := levenshtein.Distance(topic, other)
		if distance <= max(2, len(topic)/3) || strings.Contains(other, topic) || strings.Contains(topic, other) {
			near = append(near, other)
		}
	}

	slices.SortFunc(near, func(a, b string) int {
		return cmp.Or(cmp.Compare(existing[b], existing[a]), strings.Compare(a, b))
	})

	return near[:min(3, len(near))]
}

// topicCounts is every topic with its count of live entries, across seats.
func (mem *memory) topicCounts(folded *state) map[string]int {
	counts := map[string]int{}

	for _, held := range folded.entryList() {
		if held.status != statusLive {
			continue
		}

		for _, topic := range folded.topicsOf(held) {
			counts[topic]++
		}
	}

	return counts
}

func (mem *memory) show(ctx context.Context, req request) result {
	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	defer mem.presenting(ctx, req.Again)()

	if req.JSON {
		return mem.showJSON(ctx, here, folded, req)
	}

	var blocks []string

	var shown []string

	missing := false

	retiredBodyLifetime := mem.options().RetiredBodyLifetime

	for _, id := range req.IDs {
		held, found := folded.entries[id]
		if !found {
			blocks = append(blocks, id+": no such entry")
			missing = true

			continue
		}

		shown = append(shown, id)

		if mem.seen.hasBody(held) {
			blocks = append(blocks, pointer(held))

			continue
		}

		mem.seen.markBody(held)

		block := []string{showBlock(here, folded, held, retiredBodyLifetime, showItemsOf(req), req.WithSuperseded)}
		block = append(block, mem.toolSection(here, mem.takenTools(ctx, held, req.Again))...)
		blocks = append(blocks, strings.Join(block, "\n"))
	}

	if !req.NoHeat {
		mem.warm(folded, heatShow, shown...)
	}

	if missing {
		return result{text: strings.Join(blocks, "\n\n"), code: 1}
	}

	return done(strings.Join(blocks, "\n\n"))
}

// linkLine is one outbound link in show: the target, a connected repo's by
// its full qualified id, and, where it is no longer live, active or open,
// what became of it.
func linkLine(folded *state, held *entry, target string) string {
	for _, flag := range held.flags() {
		named := "→ " + foreignName(target) + " "
		if strings.HasPrefix(flag, named) {
			return "→ " + target + " " + strings.TrimPrefix(flag, named)
		}
	}

	if linked, found := folded.entries[target]; found {
		return "→ " + target + " " + linked.title
	}

	return "→ " + target
}

// droppedNote says when a merge dropped the entry's body, or nothing where
// the entry still holds it.
func droppedNote(held *entry, retiredBodyLifetime time.Duration) string {
	switch {
	case held.dropped && held.status == statusRetired:
		return fmt.Sprintf("body dropped %s after the retire", daysOrSpan(retiredBodyLifetime))
	case held.dropped:
		return "body dropped 14 days after the supersede"
	}

	return ""
}

// daysOrSpan spells a retention in days where it is whole days, as the
// dropped-body note always has, else as the span it was set to.
func daysOrSpan(held time.Duration) string {
	day := 24 * time.Hour
	if held%day == 0 {
		return fmt.Sprintf("%d days", held/day)
	}

	return span.Format(held)
}

// itemsShown is which of a todo's items show prints beside the open ones.
type itemsShown struct {
	removed, settled bool
}

func showItemsOf(req request) itemsShown {
	return itemsShown{removed: req.WithRemoved, settled: req.AllItems}
}

func showBlock(here tree, folded *state, held *entry, retiredBodyLifetime time.Duration, items itemsShown, withSuperseded bool) string {
	// The header leaves the link and anchor flags to the sections below,
	// which name each state once.
	other := branchFlag(here, held)
	header := append(statusFlags(held), other...)

	for _, flag := range held.flags() {
		if strings.HasPrefix(flag, "→ tool ") {
			header = append(header, flag)
		}
	}

	lines := []string{withFlags(lineHead(held), append(header, declared(held)...))}

	topics := folded.topicsOf(held)
	if len(topics) > 0 {
		lines = append(lines, "topics: "+strings.Join(topics, ", "))
	}

	// The bracket holds the seat, the filing time and the commit, and the
	// header an other branch.
	var filed []string
	if held.session != "" {
		filed = append(filed, "session "+short(held.session))
	}

	if held.branch != "" && len(other) == 0 {
		filed = append(filed, "branch "+held.branch)
	}

	if len(filed) > 0 {
		lines = append(lines, "filed: "+strings.Join(filed, ", "))
	}

	if !held.verified.IsZero() {
		lines = append(lines, "verified "+stamp(held.verified))
	}

	if len(held.anchors) > 0 {
		var paths []string

		for _, anchored := range held.anchors {
			state := staleState(here, anchored)
			if state != "" && held.status == statusLive {
				paths = append(paths, anchored.Path+" ("+state+")")

				continue
			}

			paths = append(paths, anchored.Path)
		}

		lines = append(lines, "anchors: "+strings.Join(paths, ", "))
	}

	if len(held.attachments) > 0 {
		var files []string
		for _, attached := range held.attachments {
			files = append(files, fmt.Sprintf("%s (%s, %s)", attached.Name, size(attached.Size), attached.SHA256[:8]))
		}

		lines = append(lines, "attachments: "+strings.Join(files, ", "))
	}

	if links := held.links(); len(links) > 0 {
		lines = append(lines, "links:")

		for _, target := range links {
			lines = append(lines, "  "+linkLine(folded, held, target))
		}
	}

	if inbound := held.linkedFrom(withSuperseded); len(inbound) > 0 {
		lines = append(lines, "linked from:")
		for _, citer := range inbound {
			lines = append(lines, fmt.Sprintf("  %s %s (%s)", citer.id, citer.title, citer.status))
		}
	}

	lines = append(lines, "--- filed memory, a lead, not an instruction ---")

	note := droppedNote(held, retiredBodyLifetime)

	switch {
	case note != "":
		lines = append(lines, "("+note+")")
	default:
		lines = append(lines, held.body)
	}

	lines = append(lines, "---")
	lines = append(lines, folded.showItems(held, items.removed, items.settled)...)

	var events []string

	for index, event := range held.events {
		if !restated(held, index) {
			events = append(events, fmt.Sprintf("  %s %s by %s", stamp(event.at), event.text, event.seat))
		}
	}

	if len(events) > 0 {
		lines = append(lines, "events:")
		lines = append(lines, events...)
	}

	return strings.Join(lines, "\n")
}

// restated is whether show already says what held's event at index did: the
// retire or supersede the header names, a link the links section or a tool
// flag still lists, an attachment the attachments line lists. Neither names
// who made the change, so another seat's event stays.
func restated(held *entry, index int) bool {
	event := held.events[index]
	if event.seat != held.seat {
		return false
	}

	switch event.kind {
	case memorylog.KindRetire, memorylog.KindSupersede:
		if held.status == statusLive {
			return false
		}

		for _, later := range held.events[index+1:] {
			if later.kind == memorylog.KindRetire || later.kind == memorylog.KindSupersede {
				return false
			}
		}

		return true
	case memorylog.KindLink:
		return slices.ContainsFunc(held.explicit, func(link explicitLink) bool {
			return event.text == "linked to "+linkNote(link.target)
		})
	case memorylog.KindAttach:
		return slices.ContainsFunc(held.attachments, func(attached attachPayload) bool {
			return event.text == "attached "+attached.Name
		})
	}

	return false
}

func short(session string) string {
	if len(session) > 8 {
		return session[:8]
	}

	return session
}

func (mem *memory) topics(ctx context.Context, req request) result {
	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	perSeat := map[string]map[string]int{}

	for _, held := range folded.entryList() {
		if held.status != statusLive {
			continue
		}

		for _, topic := range folded.topicsOf(held) {
			if perSeat[topic] == nil {
				perSeat[topic] = map[string]int{}
			}

			perSeat[topic][held.seat]++
		}
	}

	if req.JSON {
		return jsonAnswer(jsonTopics{Topics: perSeat})
	}

	counts := mem.topicCounts(folded)
	if len(counts) == 0 {
		return done("no topics yet")
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}

	slices.SortFunc(names, func(a, b string) int { return cmp.Or(cmp.Compare(counts[b], counts[a]), strings.Compare(a, b)) })

	var lines []string

	for _, name := range names {
		seats := make([]string, 0, len(perSeat[name]))
		for seatName := range perSeat[name] {
			seats = append(seats, seatName)
		}

		slices.Sort(seats)

		var parts []string
		for _, seatName := range seats {
			parts = append(parts, fmt.Sprintf("%s %d", seatName, perSeat[name][seatName]))
		}

		lines = append(lines, fmt.Sprintf("%s  %d (%s)", name, counts[name], strings.Join(parts, ", ")))
	}

	return done(strings.Join(lines, "\n"))
}

func (mem *memory) search(ctx context.Context, req request) result {
	if len(req.Terms) == 0 && req.Query == "" && len(req.Statuses) == 0 {
		return refused("search needs --term <regex>, --query <phrase> or --status <status>")
	}

	for _, status := range req.Statuses {
		if !slices.Contains(searchStatuses, status) {
			return refused("--status takes %s, not %q", strings.Join(searchStatuses, ", "), status)
		}
	}

	if req.Skip < 0 {
		return refused("--skip takes a number of 0 or more, not %d", req.Skip)
	}

	own, _ := mem.seated(ctx)

	defer mem.presenting(ctx, req.Again)()

	if !req.AllSeats && len(req.Seats) == 0 && own == "" {
		return refused("a search covers the own shelf by default: claim a seat first, or name --seat or --all-seats")
	}

	var patterns []*regexp.Regexp

	for _, term := range req.Terms {
		if strings.TrimSpace(term) == "" {
			continue
		}

		pattern, err := regexp.Compile("(?i)" + term)
		if err != nil {
			return refused("--term %q is not a regex: %v", term, err)
		}

		patterns = append(patterns, pattern)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	scope := mem.scope(ctx, folded, own)

	filters, err := scope.searchFilters(req)
	if err != nil {
		return refused("%v", err)
	}

	candidates := scope.selected(filters)

	limit := cmp.Or(req.Limit, defaultLimit)
	window := req.Skip + limit
	listing := len(patterns) == 0 && req.Query == ""

	// Each arm keeps the whole window before fusion, so a later page is the fused
	// ranking's next slice; total counts every result where it is exact, and is
	// -1 where a query scores every candidate.
	var arms [][]rankedHit

	total := 0

	if listing {
		arms = append(arms, listingArm(candidates))
		total = len(arms[0])
	}

	if len(patterns) > 0 {
		matched := keywordArm(candidates, patterns, len(candidates))
		total = len(matched)
		arms = append(arms, matched[:min(window, len(matched))])
	}

	if req.Query != "" {
		scores, err := mem.semantic(ctx, folded, req.Query)
		if err != nil {
			return refused("%v", err)
		}

		arms = append(arms, semanticArm(candidates, scores, window))
		total = -1

		if len(scoredCandidates(candidates, scores)) <= window && len(patterns) == 0 {
			total = len(scoredCandidates(candidates, scores))
		}
	}

	ranked := fuse(arms, window)
	hits := ranked[min(req.Skip, len(ranked)):]

	page := make([]string, 0, len(hits))
	for _, hit := range hits {
		page = append(page, hit.id)
	}

	// A listing matches nothing, so its entries carry no score.
	var scores []int

	if !listing {
		for _, hit := range hits {
			scores = append(scores, percentOf(hit.value, ranked[0].value))
		}
	}

	next := req.Skip + len(page)

	// The top three a search warms are the top of its ranking, which only the
	// first page shows; a listing matches nothing, so it warms nothing.
	warm := func() {
		if !req.NoHeat && req.Skip == 0 && !listing && len(page) > 0 {
			mem.warm(folded, heatSearchHit, page[:min(searchWarmed, len(page))]...)
		}
	}

	if req.JSON {
		here := mem.checkout(ctx)
		answer := jsonSearch{Notice: filedNotice, Entries: make([]jsonEntry, 0, len(page))}

		for index, id := range page {
			object := mem.objectOf(folded.entries[id], mem.entryJSON(here, folded, folded.entries[id], false))
			if scores != nil {
				object.Score = &scores[index]
			}

			answer.Entries = append(answer.Entries, object)
		}

		if (total < 0 && len(ranked) == window) || total > next {
			answer.More = &jsonMore{Skip: next}
		}

		warm()

		return jsonAnswer(answer)
	}

	if len(page) == 0 {
		return done("no entry matches")
	}

	here := mem.checkout(ctx)

	lines := make([]string, 0, len(page)+1)
	for index, id := range page {
		text := mem.lineOf(folded.entries[id], line(here, folded.entries[id]))
		if scores != nil {
			text = fmt.Sprintf("%s %d%%%s", id, scores[index], strings.TrimPrefix(text, id))
		}

		lines = append(lines, text)
	}

	switch {
	case total < 0 && len(ranked) == window:
		lines = append(lines, fmt.Sprintf("more: --skip %d", next))
	case total > next:
		lines = append(lines, fmt.Sprintf("%d more: --skip %d", total-next, next))
	}

	warm()

	return done(strings.Join(lines, "\n"))
}

var searchStatuses = []string{statusLive, statusRetired, statusSuperseded}

// searchFilters is what search's flags compile to: the own shelf and what it
// receives unless --seat or --all-seats widens it, the statuses named, else
// every one but superseded unless --with-superseded, and the topics named.
func (scope *viewScope) searchFilters(req request) ([]filter, error) {
	var calls [][]string

	switch {
	case req.AllSeats:
	case len(req.Seats) > 0:
		calls = append(calls, append([]string{"seat"}, req.Seats...))
	default:
		calls = append(calls, []string{"mine"})
	}

	if len(req.Statuses) > 0 {
		calls = append(calls, append([]string{"status"}, req.Statuses...))
	}

	if len(req.Topics) > 0 {
		calls = append(calls, append([]string{"topic"}, req.Topics...))
	}

	filters := make([]filter, 0, len(calls)+1)

	for _, call := range calls {
		held, err := scope.filterOf(call[0], call[1:]...)
		if err != nil {
			return nil, err
		}

		filters = append(filters, held)
	}

	if len(req.Statuses) == 0 && !req.WithSuperseded {
		held, err := scope.filterOf("status", statusSuperseded)
		if err != nil {
			return nil, err
		}

		filters = append(filters, negated(held))
	}

	return filters, nil
}

// listingArm orders entries by when they last changed status: the most
// recently ended first for a retired or superseded entry, the most recently
// filed first for a live one.
func listingArm(candidates []*entry) []rankedHit {
	since := func(held *entry) time.Time {
		last, found := held.latest()
		if found && held.status != statusLive {
			return last.at
		}

		return held.filed
	}

	ordered := slices.Clone(candidates)
	slices.SortStableFunc(ordered, func(a, b *entry) int {
		return cmp.Or(since(b).Compare(since(a)), strings.Compare(a.id, b.id))
	})

	hits := make([]rankedHit, 0, len(ordered))
	for _, held := range ordered {
		hits = append(hits, rankedHit{id: held.id})
	}

	return hits
}

// rankedHit is an entry's place in a search arm or the fused ranking, value
// being what ranked it: a term search's match count, a query's cosine, a
// hybrid's reciprocal rank sum. A listing ranks by time and leaves it zero.
type rankedHit struct {
	id    string
	value float64
}

// percentOf is a hit's score as sdd prints it: its value in whole percent of
// best, the value of the result's first hit. A ranking is never increasing,
// so the first hit scores 100 and every later one no more than the one above
// it. A best of zero or below, a query matching nothing by meaning, leaves
// the hits tied with it at 100 and the rest at 0.
func percentOf(value float64, best float64) int {
	if best <= 0 {
		if value >= best {
			return 100
		}

		return 0
	}

	return int(math.Round(100 * max(value, 0) / best))
}

func scoredCandidates(candidates []*entry, scores map[string]float32) []*entry {
	var found []*entry

	for _, held := range candidates {
		if _, has := scores[held.id]; has {
			found = append(found, held)
		}
	}

	return found
}

func keywordArm(candidates []*entry, patterns []*regexp.Regexp, limit int) []rankedHit {
	type hit struct {
		held  *entry
		count int
	}

	var hits []hit

	for _, held := range candidates {
		text := held.title + "\n" + held.body + "\n" + held.endedBecause()
		total := 0

		matchedAll := true
		for _, pattern := range patterns {
			count := len(pattern.FindAllStringIndex(text, -1))
			if count == 0 {
				matchedAll = false

				break
			}

			total += count
		}

		if matchedAll {
			hits = append(hits, hit{held: held, count: total})
		}
	}

	slices.SortStableFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.count, a.count), b.held.filed.Compare(a.held.filed))
	})

	ranked := make([]rankedHit, 0, min(limit, len(hits)))
	for _, held := range hits[:min(limit, len(hits))] {
		ranked = append(ranked, rankedHit{id: held.held.id, value: float64(held.count)})
	}

	return ranked
}

func semanticArm(candidates []*entry, scores map[string]float32, limit int) []rankedHit {
	var ranked []*entry

	for _, held := range candidates {
		if _, found := scores[held.id]; found {
			ranked = append(ranked, held)
		}
	}

	slices.SortStableFunc(ranked, func(a, b *entry) int {
		return cmp.Or(cmp.Compare(scores[b.id], scores[a.id]), strings.Compare(a.id, b.id))
	})

	hits := make([]rankedHit, 0, min(limit, len(ranked)))
	for _, held := range ranked[:min(limit, len(ranked))] {
		hits = append(hits, rankedHit{id: held.id, value: float64(scores[held.id])})
	}

	return hits
}

// fuse combines ranked arms by reciprocal rank, as sdd's hybrid search does; a
// single arm passes through with its own values, cut to the limit as a fused one is.
func fuse(arms [][]rankedHit, limit int) []rankedHit {
	if len(arms) == 1 {
		return arms[0][:min(limit, len(arms[0]))]
	}

	scores := map[string]float64{}

	var order []string

	for _, arm := range arms {
		for rank, hit := range arm {
			if _, seen := scores[hit.id]; !seen {
				order = append(order, hit.id)
			}

			scores[hit.id] += 1 / float64(rrfK+rank+1)
		}
	}

	slices.SortStableFunc(order, func(a, b string) int {
		return cmp.Or(cmp.Compare(scores[b], scores[a]), strings.Compare(a, b))
	})

	fused := make([]rankedHit, 0, min(limit, len(order)))
	for _, id := range order[:min(limit, len(order))] {
		fused = append(fused, rankedHit{id: id, value: scores[id]})
	}

	return fused
}

func (mem *memory) confirm(ctx context.Context, req request) result {
	here := mem.checkout(ctx)
	rehashed := map[string]confirmPayload{}

	folded, ids, failure, ok := mem.batch(ctx, "confirmed", req.IDs, false, nil,
		func(_ *state, held *entry, seat string) (memorylog.Event, error) {
			confirmed := confirmPayload{Anchors: rehash(here.root, held.anchors), Commit: here.commit, Branch: here.branch}
			rehashed[held.id] = confirmed

			raw, err := json.Marshal(confirmed)
			if err != nil {
				return memorylog.Event{}, fmt.Errorf("encoding the confirm: %w", err)
			}

			return memorylog.Event{Kind: memorylog.KindConfirm, Entry: held.id, Time: mem.now(), Seat: seat, Payload: raw}, nil
		})
	if !ok {
		return failure
	}

	mem.warm(folded, heatConfirm, ids...)

	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf("confirmed %s: %d of %d anchors re-hashed against %s", id, len(rehashed[id].Anchors),
			len(folded.entries[id].anchors), cmp.Or(here.commit, "the tree")))
	}

	return done(strings.Join(lines, "\n"))
}
