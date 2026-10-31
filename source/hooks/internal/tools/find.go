package tools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Ranking. The floor, `tools_query_floor`, applies to the cosine alone, before
// either lift.
const (
	takeStep = 0.05
	takeCap  = 0.1
	ownLift  = 0.05
	pageSize = 5
)

// maxLifted is the most a lifted cosine reaches: a perfect match, taken by the
// most sessions a lift counts, in the own project.
const maxLifted = 1 + takeCap + ownLift

type ranked struct {
	hit   found
	term  bool
	score *float64
}

func takeLift(takers int) float64 {
	return min(takeStep*math.Log1p(float64(takers)), takeCap)
}

// rankValue is a hit's place in find's order as one number: a term match
// counts 1, and the lifted cosine adds its share of maxLifted, at most 1, so
// a term hit never values below a hit by meaning alone, and within each the
// higher cosine values higher, as the sort orders them.
func (hit ranked) rankValue() float64 {
	value := 0.0
	if hit.term {
		value = 1
	}

	if hit.score != nil {
		value += min(max(*hit.score, 0)/maxLifted, 1)
	}

	return value
}

// percentOf is a hit's score as sdd prints it: its rank value in whole percent
// of best, the first hit's, so the first scores 100 and none below it more
// than the hit above. Every value is 0 or more; a best of 0 ties every hit.
func percentOf(value float64, best float64) int {
	if best <= 0 {
		return 100
	}

	return int(math.Round(100 * value / best))
}

func (lib *library) find(ctx context.Context, req request) result {
	if strings.TrimSpace(req.Query) == "" && len(req.Terms) == 0 {
		return refused("find needs --query '<need>', a --term '<literal>', or both")
	}

	var (
		query   []float32
		queried string
	)

	if strings.TrimSpace(req.Query) != "" {
		vector, fingerprint, err := lib.embedText(ctx, req.Query)
		if err != nil {
			return refused("--query needs an embedder (%v): search with --term '<literal>'", err)
		}

		query, queried = vector, fingerprint
	}

	stores := lib.stores(ctx)

	var scores map[string]float32

	if query != nil {
		wanted := map[string]bool{}

		for _, held := range stores {
			for _, tool := range held.index.Tools {
				wanted[tool.Described] = true
			}
		}

		scores = lib.scores(query, queried, wanted)
	}

	var hits []ranked

	total := 0

	for _, held := range stores {
		total += len(held.index.Tools)

		for _, name := range held.names() {
			tool := held.index.Tools[name]
			hit := found{store: held, tool: tool}
			matched := len(req.Terms) > 0 && lib.matchesTerms(hit, req.Terms)

			var score *float64

			cosine := float64(scores[tool.Described])
			if cosine >= lib.options.ToolsQueryFloor {
				lifted := cosine + takeLift(len(tool.Takers))
				if held.own {
					lifted += ownLift
				}

				score = &lifted
			}

			if matched || score != nil {
				hits = append(hits, ranked{hit: hit, term: matched, score: score})
			}
		}
	}

	slices.SortStableFunc(hits, func(a, b ranked) int {
		if a.term != b.term {
			if a.term {
				return -1
			}

			return 1
		}

		left, right := -1.0, -1.0
		if a.score != nil {
			left = *a.score
		}

		if b.score != nil {
			right = *b.score
		}

		if left != right {
			return cmp.Compare(right, left)
		}

		return strings.Compare(a.hit.shown(), b.hit.shown())
	})

	limit, page := cmp.Or(req.Limit, pageSize), cmp.Or(req.Page, 1)
	start := min((page-1)*limit, len(hits))
	end := min(start+limit, len(hits))
	shown := hits[start:end]

	seen := lib.presenting(ctx, req.Again)
	defer seen.save()

	if req.JSON {
		best := 0.0
		if len(hits) > 0 {
			best = hits[0].rankValue()
		}

		return lib.findJSON(seen, shown, best, len(hits), total, len(stores), page)
	}

	var lines []string

	framed := false

	for _, hit := range shown {
		if seen.printed(hit.hit) {
			lines = append(lines, seenPointer(hit.hit))

			continue
		}

		lines = append(lines, hitLine(hit))
		framed = true
	}

	if framed {
		lines = append([]string{framing}, lines...)
	}

	return done(strings.Join(append(lines, findClosing(start, end, limit, page, len(hits), total, len(stores))), "\n"))
}

// findClosing says what a find showed: one count where every hit fits the
// page it shows, else the range shown and what the next page holds.
func findClosing(start int, end int, limit int, page int, matching int, total int, projects int) string {
	takes := fmt.Sprintf("`%s take <name> ...` copies them", self)

	if start == 0 && end == matching {
		closing := fmt.Sprintf("%s (%s, %s)", counted(matching, "match", "matches"), counted(total, "tool", "tools"),
			counted(projects, "project", "projects"))
		if matching == 0 {
			return closing
		}

		return closing + "; " + takes
	}

	closing := fmt.Sprintf("%d-%d of %d matching tools shown, of %d in %d projects; %s",
		min(start+1, end), end, matching, total, projects, takes)
	if end < matching {
		closing += fmt.Sprintf("; --page %d shows the next %d", page+1, min(limit, matching-end))
	}

	return closing
}

func counted(count int, one string, many string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, one)
	}

	return fmt.Sprintf("%d %s", count, many)
}

// framedFor is the framing a JSON listing carries: only while a tool in it
// carries another agent's text, which a seen tool no longer does.
func framedFor(tools []listedTool) string {
	for _, tool := range tools {
		if !tool.Seen {
			return strings.TrimSuffix(framing, ":")
		}
	}

	return ""
}

func hitLine(hit ranked) string {
	tool := hit.hit.tool
	line := fmt.Sprintf("%s  v%d  %s", hit.hit.shown(), tool.Version, tool.Summary)

	if hit.score != nil {
		line += fmt.Sprintf("  [%.2f]", *hit.score)
	}

	return line
}

// matchesTerms holds every term, case-insensitive and literal, to a tool's
// name, fields and current files.
func (lib *library) matchesTerms(hit found, terms []string) bool {
	tool := hit.tool
	fields := strings.ToLower(strings.Join([]string{tool.Name, tool.Summary, tool.Usage, tool.Inputs, tool.Outputs}, "\n"))

	var files []byte

	for _, term := range terms {
		needle := strings.ToLower(term)
		if strings.Contains(fields, needle) {
			continue
		}

		if files == nil {
			read, err := versionFiles(hit.store, tool.Name, tool.Version)
			if err != nil {
				return false
			}

			for _, file := range read {
				files = append(files, bytes.ToLower(file.Data)...)
				files = append(files, '\n')
			}
		}

		if !bytes.Contains(files, []byte(needle)) {
			return false
		}
	}

	return true
}

// listedTool is a tool as find and list answer it in JSON. As every read
// answer, it carries a key only while the key holds a value: the own
// project's tool has no label and is own, another project's is labelled and
// not own.
type listedTool struct {
	Name string `json:"name"`
	// Score is a find hit's match in percent of the best hit's; list ranks
	// nothing and carries none.
	Score   *int    `json:"score,omitempty"`
	Label   *string `json:"label,omitempty"`
	Version int     `json:"version"`
	Summary string  `json:"summary,omitempty"`
	// Seen marks a version the calling agent's context already holds; it then
	// answers with its name and version alone.
	Seen bool `json:"-"`
}

func asListed(hit found) listedTool {
	var label *string
	if !hit.store.own {
		label = &hit.store.label
	}

	tool := hit.tool

	return listedTool{Name: tool.Name, Label: label, Version: tool.Version, Summary: tool.Summary}
}

// listing is what find and list answer as JSON.
type listing struct {
	Framing  string       `json:"framing,omitempty"`
	Tools    []listedTool `json:"tools,omitempty"`
	Shown    int          `json:"shown"`
	Matching int          `json:"matching"`
	Total    int          `json:"total"`
	Projects int          `json:"projects"`
	Page     int          `json:"page"`
}

func (lib *library) findJSON(seen *presented, shown []ranked, best float64, matching int, total int, projects int,
	page int,
) result {
	tools := make([]listedTool, 0, len(shown))
	for _, hit := range shown {
		listed := asListed(hit.hit)
		listed.Seen = seen.printed(hit.hit)

		score := percentOf(hit.rankValue(), best)
		listed.Score = &score
		tools = append(tools, listed)
	}

	return encoded(listing{Framing: framedFor(tools), Tools: tools, Shown: len(shown),
		Matching: matching, Total: total, Projects: projects, Page: page})
}

func encoded(value any) result {
	data, err := json.Marshal(value)
	if err != nil {
		return refused("encoding the answer: %v", err)
	}

	return done(string(data))
}

func (lib *library) list(ctx context.Context, req request) result {
	stores := lib.stores(ctx)
	own := stores[0]

	seen := lib.presenting(ctx, req.Again)
	defer seen.save()

	if req.JSON {
		var tools []listedTool

		for _, name := range own.names() {
			hit := found{store: own, tool: own.index.Tools[name]}
			listed := asListed(hit)
			listed.Seen = seen.printed(hit)
			tools = append(tools, listed)
		}

		total := 0
		for _, held := range stores {
			total += len(held.index.Tools)
		}

		return encoded(listing{Framing: framedFor(tools), Tools: tools, Shown: len(tools),
			Matching: len(tools), Total: total, Projects: len(stores), Page: 1})
	}

	var lines []string

	framed := false

	for _, name := range own.names() {
		tool := own.index.Tools[name]

		if seen.printed(found{store: own, tool: tool}) {
			lines = append(lines, seenPointer(found{store: own, tool: tool}))

			continue
		}

		framed = true

		taken := fmt.Sprintf("taken %d", tool.Takes)
		if tool.LastTake != "" {
			taken += ", last " + localStamp(tool.LastTake)
		}

		lines = append(lines, fmt.Sprintf("%s  %s  [%s]", name, tool.Summary, taken))
	}

	if framed {
		lines = append([]string{framing}, lines...)
	}

	if len(own.index.Tools) == 0 {
		lines = append(lines, "this project holds no tools yet")
	}

	var others []string

	for _, held := range stores[1:] {
		if len(held.index.Tools) > 0 {
			others = append(others, held.label+" "+counted(len(held.index.Tools), "tool", "tools"))
		}
	}

	if len(others) > 0 {
		lines = append(lines, fmt.Sprintf("other projects: %s; `%s find` searches them", strings.Join(others, ", "), self))
	}

	return done(strings.Join(lines, "\n"))
}

func localStamp(stamp string) string {
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return clean(stamp, false)
	}

	return at.Local().Format("2006-01-02 15:04")
}
