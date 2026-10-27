package memory

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// returns keeps the live entries that need attention, each with why and the
// verbs that answer it. A reason that cannot be checked shows as unchecked,
// never as absent: a cold graph status cache or a file that cannot be read
// says nothing either way.

const (
	returnAnchor = "anchor"
	returnLink   = "link"
	returnUntil  = "until"
	returnBranch = "branch"
)

var returnKinds = []string{returnAnchor, returnLink, returnUntil, returnBranch}

// returnReason is one reason an entry needs attention: its kind, what was
// found, the verbs that answer it and how to read them, and whether it could
// be checked at all.
type returnReason struct {
	kind      string
	detail    string
	verbs     []string
	answer    string
	unchecked bool
}

type jsonReturn struct {
	Reason    string   `json:"reason"`
	Detail    string   `json:"detail"`
	Verbs     []string `json:"verbs"`
	Answer    string   `json:"answer"`
	Unchecked bool     `json:"unchecked"`
}

func (reason returnReason) text() string {
	if reason.unchecked {
		return reason.kind + " unchecked: " + reason.detail
	}

	return reason.kind + " " + reason.detail + " → " + reason.answer
}

func (reason returnReason) json() jsonReturn {
	return jsonReturn{Reason: reason.kind, Detail: reason.detail, Verbs: orEmpty(reason.verbs), Answer: reason.answer,
		Unchecked: reason.unchecked}
}

var returnsWord = filterWord{
	name: "returns", arity: takesOptional, values: returnKinds, needs: needLinks | needFiles | needGraph | needGit,
	describe: "live entries needing attention, each with why and the verbs that answer it",
	build: func(scope *viewScope, values []string) (predicate, error) {
		kinds := values
		if len(kinds) == 0 {
			kinds = returnKinds
		}

		return func(scope *viewScope, held *entry) bool {
			return held.status == statusLive && len(scope.returnsOf(held, kinds)) > 0
		}, nil
	},
}

// returnsOf is every reason held needs attention, of the kinds given.
func (scope *viewScope) returnsOf(held *entry, kinds []string) []returnReason {
	if scope.reasons == nil {
		scope.reasons = map[string][]returnReason{}
	}

	all, known := scope.reasons[held.id]
	if !known {
		all = scope.reasonsFor(held)
		scope.reasons[held.id] = all
	}

	var kept []returnReason

	for _, reason := range all {
		if slices.Contains(kinds, reason.kind) {
			kept = append(kept, reason)
		}
	}

	return kept
}

func (scope *viewScope) reasonsFor(held *entry) []returnReason {
	var reasons []returnReason

	gone, anchors := scope.anchorReasons(held)
	reasons = append(reasons, anchors...)

	ended, links := scope.linkReasons(held)
	reasons = append(reasons, links...)

	if held.kind != kindRule {
		reasons = append(reasons, scope.untilReasons(held, ended, gone)...)
	}

	if held.branch != "" && scope.here.branch != "" && held.branch != scope.here.branch {
		reasons = append(reasons, returnReason{kind: returnBranch, detail: "filed on " + held.branch,
			answer: "none until " + held.branch + " merges"})
	}

	return reasons
}

// anchorState is how an anchored file stands against its hash at filing:
// ok, drifted, gone, or unreadable where it cannot be read.
func anchorState(here tree, anchored anchor) string {
	path := filepath.Join(here.root, filepath.FromSlash(anchored.Path))

	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return "gone"
	}

	// A cached hash would answer for a file no read could open now; one that
	// cannot be read is unchecked whatever its stamp says.
	if err != nil || unreadable(path) {
		return "unreadable"
	}

	hash, found := here.fileHash(path)
	if !found {
		return "unreadable"
	}

	if hash != anchored.Hash {
		return "drifted"
	}

	return "ok"
}

// anchorReasons is a reason per anchor that drifted, went or cannot be read,
// and whether every anchor is gone.
func (scope *viewScope) anchorReasons(held *entry) (bool, []returnReason) {
	var reasons []returnReason

	gone := 0

	for _, anchored := range held.anchors {
		switch anchorState(scope.here, anchored) {
		case "drifted":
			reasons = append(reasons, returnReason{kind: returnAnchor, detail: "drifted: " + anchored.Path,
				verbs: []string{"confirm", "supersede"}, answer: "confirm where the entry still holds, else supersede"})
		case "gone":
			gone++
			reasons = append(reasons, returnReason{kind: returnAnchor, detail: "gone: " + anchored.Path,
				verbs: []string{"supersede", "retire"}, answer: "supersede or retire"})
		case "unreadable":
			reasons = append(reasons, returnReason{kind: returnAnchor, detail: anchored.Path + " cannot be read",
				unchecked: true})
		}
	}

	return len(held.anchors) > 0 && gone == len(held.anchors), reasons
}

// linkReasons is a reason per link whose target closed, was superseded,
// retired or went, or whose state cannot be known, and whether every link
// ended.
func (scope *viewScope) linkReasons(held *entry) (bool, []returnReason) {
	var reasons []returnReason

	targets := held.links()
	ended := 0

	for _, target := range targets {
		state, checked := scope.linkState(target)

		switch {
		case !checked:
			reasons = append(reasons, returnReason{kind: returnLink, detail: target + ": the graph status cache is cold",
				unchecked: true})
		case state == "gone":
			ended++
			reasons = append(reasons, returnReason{kind: returnLink, detail: target + " gone", verbs: []string{"unlink"},
				answer: "unlink"})
		case state == "retired":
			ended++
			reasons = append(reasons, returnReason{kind: returnLink, detail: target + " retired",
				verbs: []string{"supersede", "retire"}, answer: "supersede or retire"})
		case strings.HasPrefix(state, "closed") || strings.HasPrefix(state, "superseded"):
			ended++

			successor := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(state, "closed by"), "superseded by"))
			reasons = append(reasons, returnReason{kind: returnLink, detail: target + " " + state,
				verbs:  []string{"supersede", "retire"},
				answer: fmt.Sprintf("read %s, then supersede or retire", cmp.Or(successor, "the successor"))})
		}
	}

	return len(targets) > 0 && ended == len(targets), reasons
}

// linkState is what became of a link target, and whether that can be known:
// a memory target from the folded log, a graph target from its file and the
// cached graph statuses, never from a fresh sdd export.
func (scope *viewScope) linkState(target string) (string, bool) {
	if strings.HasPrefix(target, "m-") {
		linked, found := scope.folded.entries[target]

		return memoryLinkState(linked, found), true
	}

	statuses, cached := scope.mem.cachedGraphStatuses(scope.ctx)

	if _, _, qualified := splitQualified(target); qualified {
		if !cached {
			return "", false
		}

		known, found := statuses[target]
		if !found {
			return "gone", true
		}

		return graphLinkState(known), true
	}

	_, err := os.Stat(scope.mem.graphPath(scope.ctx, target))
	if errors.Is(err, fs.ErrNotExist) {
		return "gone", true
	}

	if err != nil || !cached {
		return "", false
	}

	return graphLinkState(statuses[target]), true
}

// untilReasons is a reason per declared end met or not checkable, and one for
// an end the tool infers: every link ended, every anchor gone, or a merged:
// branch gone.
func (scope *viewScope) untilReasons(held *entry, linksEnded bool, anchorsGone bool) []returnReason {
	var reasons []returnReason

	if scope.probe == nil {
		scope.probe = &endProbe{mem: scope.mem, here: scope.here, branches: map[string]branchState{}}
	}

	for _, end := range conditionsOf(held) {
		if (end.form == "changed" || end.form == "gone") && unreadable(filepath.Join(scope.here.root, filepath.FromSlash(end.value))) {
			reasons = append(reasons, returnReason{kind: returnUntil, detail: end.String() + ": the file cannot be read",
				unchecked: true})

			continue
		}

		if end.form == "closed" {
			statuses, cached := scope.mem.cachedGraphStatuses(scope.ctx)
			if !cached {
				reasons = append(reasons, returnReason{kind: returnUntil, detail: end.String() +
					": the graph status cache is cold", unchecked: true})

				continue
			}

			scope.probe.statuses, scope.probe.asked = statuses, true
		}

		if scope.probe.met(scope.ctx, held, end) {
			reasons = append(reasons, returnReason{kind: returnUntil, detail: "end met: " + end.String(),
				verbs: []string{"retire"}, answer: "retire"})
		}
	}

	inferred := ""

	switch {
	case linksEnded:
		inferred = "every link ended"
	case anchorsGone:
		inferred = "every anchor gone"
	default:
		for _, end := range conditionsOf(held) {
			if end.form == "merged" && !scope.probe.branch(scope.ctx, end.value).exists {
				inferred = "branch " + strings.Split(end.value, "@")[0] + " is gone"
			}
		}
	}

	if inferred != "" {
		reasons = append(reasons, returnReason{kind: returnUntil, detail: "end inferred: " + inferred,
			verbs: []string{"retire"}, answer: "retire"})
	}

	return reasons
}

// unreadable is whether path exists but cannot be read, which says nothing
// of whether it changed.
func unreadable(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	if err != nil {
		return true
	}

	if !info.Mode().IsRegular() {
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		return true
	}

	_ = file.Close()

	return false
}
