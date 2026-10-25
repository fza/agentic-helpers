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

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// Ranking. The floor, `tools_query_floor`, applies to the cosine alone, before
// either lift.
const (
	takeStep = 0.05
	takeCap  = 0.1
	ownLift  = 0.05
	pageSize = 10
)

type ranked struct {
	hit   found
	term  bool
	score *float64
}

func takeLift(takers int) float64 {
	return min(takeStep*math.Log1p(float64(takers)), takeCap)
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
			return refused("--query needs an embedder, and none answers (%v): search with --term '<literal>'", err)
		}

		query, queried = vector, fingerprint
	}

	stores := lib.stores(ctx)

	var hits []ranked

	total := 0

	for _, held := range stores {
		total += len(held.index.Tools)

		for _, name := range held.names() {
			tool := held.index.Tools[name]
			hit := found{store: held, tool: tool}
			matched := len(req.Terms) > 0 && lib.matchesTerms(hit, req.Terms)

			var score *float64

			comparable := held.index.Fingerprint == "" || held.index.Fingerprint == queried
			if query != nil && comparable && len(tool.Vector) > 0 {
				cosine := float64(embed.Cosine(query, tool.Vector))
				if cosine >= lib.options.ToolsQueryFloor {
					lifted := cosine + takeLift(len(tool.Takers))
					if held.own {
						lifted += ownLift
					}

					score = &lifted
				}
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

	if req.JSON {
		return lib.findJSON(shown, len(hits), total, len(stores), page)
	}

	lines := []string{framing}
	for _, hit := range shown {
		lines = append(lines, hitLine(hit))
	}

	closing := fmt.Sprintf("%d-%d of %d matching tools shown, of %d in %d projects; `%s take <name> ...` copies them",
		min(start+1, end), end, len(hits), total, len(stores), self)
	if end < len(hits) {
		closing += fmt.Sprintf("; --page %d shows the next %d", page+1, min(limit, len(hits)-end))
	}

	return done(strings.Join(append(lines, closing), "\n"))
}

func hitLine(hit ranked) string {
	tool := hit.hit.tool
	line := fmt.Sprintf("%s  v%d  %s", hit.hit.shown(), tool.Version, tool.Summary)

	if hit.score != nil {
		line += fmt.Sprintf("  [%.2f]", *hit.score)
	}

	return line + "\n" + strings.Join(indented("usage", tool.Usage), "\n")
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
				files = append(files, bytes.ToLower(file.data)...)
				files = append(files, '\n')
			}
		}

		if !bytes.Contains(files, []byte(needle)) {
			return false
		}
	}

	return true
}

type listedTool struct {
	Name    string   `json:"name"`
	Label   *string  `json:"label"`
	Own     bool     `json:"own"`
	Version int      `json:"version"`
	Summary string   `json:"summary"`
	Usage   string   `json:"usage"`
	Inputs  string   `json:"inputs"`
	Outputs string   `json:"outputs"`
	Score   *float64 `json:"score,omitempty"`
	Takers  int      `json:"takers"`
}

func asListed(hit found, score *float64) listedTool {
	var label *string
	if !hit.store.own {
		label = &hit.store.label
	}

	tool := hit.tool

	return listedTool{Name: tool.Name, Label: label, Own: hit.store.own, Version: tool.Version, Summary: tool.Summary,
		Usage: tool.Usage, Inputs: tool.Inputs, Outputs: tool.Outputs, Score: score, Takers: len(tool.Takers)}
}

func (lib *library) findJSON(shown []ranked, matching int, total int, projects int, page int) result {
	type scoredTool struct {
		listedTool
		Score *float64 `json:"score"`
	}

	tools := make([]scoredTool, 0, len(shown))
	for _, hit := range shown {
		tools = append(tools, scoredTool{listedTool: asListed(hit.hit, nil), Score: hit.score})
	}

	return encoded(map[string]any{
		"framing": strings.TrimSuffix(framing, ":"), "tools": tools, "shown": len(shown), "matching": matching,
		"total": total, "projects": projects, "page": page,
	})
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

	if req.JSON {
		tools := []listedTool{}

		for _, name := range own.names() {
			tools = append(tools, asListed(found{store: own, tool: own.index.Tools[name]}, nil))
		}

		total := 0
		for _, held := range stores {
			total += len(held.index.Tools)
		}

		return encoded(map[string]any{
			"framing": strings.TrimSuffix(framing, ":"), "tools": tools, "shown": len(tools),
			"matching": len(tools), "total": total, "projects": len(stores), "page": 1,
		})
	}

	lines := []string{framing}

	for _, name := range own.names() {
		tool := own.index.Tools[name]

		taken := fmt.Sprintf("taken %d", tool.Takes)
		if tool.LastTake != "" {
			taken += ", last " + localStamp(tool.LastTake)
		}

		lines = append(lines, fmt.Sprintf("%s  %s  [%s]", name, tool.Summary, taken))
	}

	if len(own.index.Tools) == 0 {
		lines = append(lines, "this project holds no tools yet")
	}

	for _, held := range stores[1:] {
		lines = append(lines, fmt.Sprintf("%s: %d tools, `%s find` searches them", held.label, len(held.index.Tools), self))
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
