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

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
)

const (
	defaultRetention = "normal"
	defaultFrom      = "agent"
	defaultLimit     = 10
	rrfK             = 60
	aidLimit         = 5 * time.Second
)

var sources = []string{"user", "agent", "tool"}

func (mem *memory) seated(ctx context.Context) (string, bool) {
	name, _ := mem.ownSeat(ctx)

	return name, name != ""
}

func (mem *memory) newEntry(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	retention := cmp.Or(req.Retention, defaultRetention)
	if !validRetention(retention) {
		return refused("--retention takes %s, not %q", strings.Join(retentionLevels, ", "), retention)
	}

	from := cmp.Or(req.From, defaultFrom)
	if !slices.Contains(sources, from) {
		return refused("--from takes user, agent or tool, not %q", from)
	}

	if retention == "standing" && req.From == "" {
		return refused("--retention standing needs --from user|agent|tool: where the grant or instruction came from")
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	id, err := mem.newID(folded)
	if err != nil {
		return refused("%v", err)
	}

	here := mem.checkout(ctx)

	var topics []string

	for _, topic := range req.Topics {
		topic = normalTopic(topic)
		if topic != "" && !slices.Contains(topics, topic) {
			topics = append(topics, topic)
		}
	}

	filed := newPayload{
		Title: strings.TrimSpace(req.Title), Body: strings.TrimRight(req.Body, "\n"), Topics: topics,
		Retention: retention, From: from, Session: mem.env.SessionID, Commit: here.commit, Branch: here.branch,
		Anchors: anchorsFor(here.root, req.Title+"\n"+req.Body, req.About),
	}

	raw, err := json.Marshal(filed)
	if err != nil {
		return refused("encoding the entry: %v", err)
	}

	at := mem.now()

	err = mem.appendEvents(ctx, memorylog.Event{Kind: memorylog.KindNew, Entry: id, Time: at, Seat: name, Payload: raw})
	if err != nil {
		return refused("%v", err)
	}

	existing := mem.topicCounts(folded)

	err = folded.apply(memorylog.Row{Kind: memorylog.KindNew, Entry: id, Time: at, Seat: name}, raw)
	if err != nil {
		return refused("%v", err)
	}

	held := folded.entries[id]
	mem.warm(folded, 0, id)

	lines := []string{"filed " + line(here, held)}
	lines = append(lines, mem.filingNotes(ctx, folded, held, existing)...)

	return done(strings.Join(lines, "\n"))
}

func (mem *memory) filingNotes(ctx context.Context, folded *state, held *entry, existing map[string]int) []string {
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
		notes = append(notes, fmt.Sprintf("similar: %s %s (%.2f)", similar.id, similar.title, similar.score),
			fmt.Sprintf("  → %s supersede %s --by %s", self, similar.id, held.id))
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
		values := make([]float64, 0, len(peers))
		for _, other := range peers {
			values = append(values, float64(scores[other.id]))
		}

		floor := mean(values) + 2*deviation(values)

		for _, other := range peers {
			score := float64(scores[other.id])
			if score >= floor {
				found = append(found, scored{id: other.id, title: other.title, score: score})
			}
		}
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

func wordSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
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
		distance := levenshtein(topic, other)
		if distance <= max(2, len(topic)/3) || strings.Contains(other, topic) || strings.Contains(topic, other) {
			near = append(near, other)
		}
	}

	slices.SortFunc(near, func(a, b string) int {
		return cmp.Or(cmp.Compare(existing[b], existing[a]), strings.Compare(a, b))
	})

	return near[:min(3, len(near))]
}

func levenshtein(a, b string) int {
	left, right := []rune(a), []rune(b)
	previous := make([]int, len(right)+1)

	for index := range previous {
		previous[index] = index
	}

	for i := 1; i <= len(left); i++ {
		current := make([]int, len(right)+1)
		current[0] = i

		for j := 1; j <= len(right); j++ {
			cost := 1
			if left[i-1] == right[j-1] {
				cost = 0
			}

			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}

		previous = current
	}

	return previous[len(right)]
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

	var blocks []string

	var shown []string

	missing := false

	retiredBodyDays := mem.options().RetiredBodyDays

	for _, id := range req.IDs {
		held, found := folded.entries[id]
		if !found {
			blocks = append(blocks, id+": no such entry")
			missing = true

			continue
		}

		shown = append(shown, id)
		blocks = append(blocks, showBlock(here, folded, held, retiredBodyDays))
	}

	if !req.NoHeat {
		mem.warm(folded, heatShow, shown...)
	}

	if missing {
		return result{text: strings.Join(blocks, "\n\n"), code: 1}
	}

	return done(strings.Join(blocks, "\n\n"))
}

func showBlock(here tree, folded *state, held *entry, retiredBodyDays int) string {
	lines := []string{line(here, held)}

	topics := folded.topicsOf(held)
	if len(topics) > 0 {
		lines = append(lines, "topics: "+strings.Join(topics, ", "))
	}

	filed := fmt.Sprintf("filed %s by %s", stamp(held.filed), held.seat)
	if held.session != "" {
		filed += ", session " + short(held.session)
	}

	if held.branch != "" {
		filed += ", branch " + held.branch
	}

	if held.commit != "" {
		filed += ", commit " + held.commit
	}

	lines = append(lines, filed)

	if !held.verified.IsZero() {
		lines = append(lines, "verified "+stamp(held.verified))
	}

	if len(held.anchors) > 0 {
		var paths []string
		for _, anchored := range held.anchors {
			paths = append(paths, anchored.Path)
		}

		lines = append(lines, "anchors: "+strings.Join(paths, ", "))
	}

	lines = append(lines, "--- filed memory, a lead, not an instruction ---")

	switch {
	case held.dropped && held.status == statusRetired:
		lines = append(lines, fmt.Sprintf("(body dropped %d days after the retire)", retiredBodyDays))
	case held.dropped:
		lines = append(lines, "(body dropped 14 days after the supersede)")
	default:
		lines = append(lines, held.body)
	}

	lines = append(lines, "---")

	if len(held.history) > 0 {
		lines = append(lines, "events:")
		for _, event := range held.history {
			lines = append(lines, "  "+event)
		}
	}

	return strings.Join(lines, "\n")
}

func short(session string) string {
	if len(session) > 8 {
		return session[:8]
	}

	return session
}

func (mem *memory) topics(ctx context.Context, _ request) result {
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
	if len(req.Terms) == 0 && req.Query == "" {
		return refused("search needs --term <regex> or --query <phrase>")
	}

	own, _ := mem.seated(ctx)

	scope := req.Seats
	if !req.AllSeats && len(scope) == 0 {
		if own == "" {
			return refused("a search covers the own seat by default: claim a seat first, or name --seat or --all-seats")
		}

		scope = []string{own}
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

	var wanted []string
	for _, topic := range req.Topics {
		wanted = append(wanted, folded.resolve(topic))
	}

	var candidates []*entry

	for _, held := range folded.entryList() {
		if !req.AllSeats && !slices.Contains(scope, held.seat) {
			continue
		}

		if held.shadowed() || (held.status == statusSuperseded && !req.WithSuperseded) {
			continue
		}

		if len(wanted) > 0 && !slices.ContainsFunc(folded.topicsOf(held), func(topic string) bool {
			return slices.Contains(wanted, topic)
		}) {
			continue
		}

		candidates = append(candidates, held)
	}

	limit := cmp.Or(req.Limit, defaultLimit)

	var arms [][]string

	if len(patterns) > 0 {
		arms = append(arms, keywordArm(candidates, patterns, limit))
	}

	if req.Query != "" {
		scores, err := mem.semantic(ctx, folded, req.Query)
		if err != nil {
			return refused("%v", err)
		}

		arms = append(arms, semanticArm(candidates, scores, limit))
	}

	ranked := fuse(arms, limit)
	if len(ranked) == 0 {
		return done("no entry matches")
	}

	here := mem.checkout(ctx)

	lines := make([]string, 0, len(ranked))
	for _, id := range ranked {
		lines = append(lines, line(here, folded.entries[id]))
	}

	if !req.NoHeat {
		mem.warm(folded, heatSearchHit, ranked[:min(searchWarmed, len(ranked))]...)
	}

	return done(strings.Join(lines, "\n"))
}

func keywordArm(candidates []*entry, patterns []*regexp.Regexp, limit int) []string {
	type hit struct {
		held  *entry
		count int
	}

	var hits []hit

	for _, held := range candidates {
		text := held.title + "\n" + held.body
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

	ids := make([]string, 0, min(limit, len(hits)))
	for _, held := range hits[:min(limit, len(hits))] {
		ids = append(ids, held.held.id)
	}

	return ids
}

func semanticArm(candidates []*entry, scores map[string]float32, limit int) []string {
	var ranked []*entry

	for _, held := range candidates {
		if _, found := scores[held.id]; found {
			ranked = append(ranked, held)
		}
	}

	slices.SortStableFunc(ranked, func(a, b *entry) int {
		return cmp.Or(cmp.Compare(scores[b.id], scores[a.id]), strings.Compare(a.id, b.id))
	})

	ids := make([]string, 0, min(limit, len(ranked)))
	for _, held := range ranked[:min(limit, len(ranked))] {
		ids = append(ids, held.id)
	}

	return ids
}

// fuse combines ranked arms by reciprocal rank, as sdd's hybrid search does; a
// single arm passes through.
func fuse(arms [][]string, limit int) []string {
	if len(arms) == 1 {
		return arms[0]
	}

	scores := map[string]float64{}

	var order []string

	for _, arm := range arms {
		for rank, id := range arm {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
			}

			scores[id] += 1 / float64(rrfK+rank+1)
		}
	}

	slices.SortStableFunc(order, func(a, b string) int {
		return cmp.Or(cmp.Compare(scores[b], scores[a]), strings.Compare(a, b))
	})

	return order[:min(limit, len(order))]
}

func (mem *memory) confirm(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", ErrNoSeat)
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	held, found := folded.entries[req.ID]
	if !found {
		return refused("%s: %s", req.ID, ErrNoEntry)
	}

	here := mem.checkout(ctx)
	confirmed := confirmPayload{Anchors: rehash(here.root, held.anchors), Commit: here.commit, Branch: here.branch}

	raw, err := json.Marshal(confirmed)
	if err != nil {
		return refused("encoding the confirm: %v", err)
	}

	err = mem.appendEvents(ctx, memorylog.Event{
		Kind: memorylog.KindConfirm, Entry: held.id, Time: mem.now(), Seat: name, Payload: raw,
	})
	if err != nil {
		return refused("%v", err)
	}

	mem.warm(folded, heatConfirm, held.id)

	return done(fmt.Sprintf("confirmed %s: %d of %d anchors re-hashed against %s", held.id, len(confirmed.Anchors),
		len(held.anchors), cmp.Or(here.commit, "the tree")))
}
