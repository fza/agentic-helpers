// Package secrets finds credentials in text bound for agentic-helpers' stores
// and the graph, using gitleaks' default rule set vendored as rules.json
// (vendor-rules.py rebuilds it from the pinned upstream file). The engine
// mirrors gitleaks' detectRule and checkFindingAllowed with Go's regexp, as
// the sdd fork's scanner does, so both refuse the same strings. A finding
// names the rule and the line, never the matched text: a caller refuses the
// write rather than redacting it, since a redacted copy beside a missed
// variant of the same credential is worse than a refusal.
package secrets

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Incomplete is the rule a finding names when the scan could not run to the
// end: the vendored rules failed to load or compile, or the context ended. A
// caller refusing on any finding then refuses rather than letting unscanned
// text through.
const Incomplete = "scan-incomplete"

//go:embed rules.json
var rulesJSON []byte

type Finding struct {
	RuleID string
	Line   int
}

type allowlist struct {
	Condition   string   `json:"condition"`
	RegexTarget string   `json:"regexTarget"`
	Regexes     []string `json:"regexes"`
	Paths       []string `json:"paths"`
	Stopwords   []string `json:"stopwords"`

	regexes []*regexp.Regexp
	paths   []*regexp.Regexp
}

type rule struct {
	ID          string      `json:"id"`
	Regex       string      `json:"regex"`
	Path        string      `json:"path"`
	Entropy     float64     `json:"entropy"`
	SecretGroup int         `json:"secretGroup"`
	Keywords    []string    `json:"keywords"`
	Allowlists  []allowlist `json:"allowlists"`

	// Patterns compile on first need, so text holding none of a rule's
	// keywords never pays for its regex.
	compile sync.Once
	regex   *regexp.Regexp
	path    *regexp.Regexp
	err     error
}

type ruleSet struct {
	Allowlists []allowlist `json:"allowlists"`
	Rules      []*rule     `json:"rules"`
}

var loadRules = sync.OnceValues(func() (*ruleSet, error) {
	return parseRules(rulesJSON)
})

func parseRules(data []byte) (*ruleSet, error) {
	var set ruleSet

	err := json.Unmarshal(data, &set)
	if err != nil {
		return nil, fmt.Errorf("decoding the vendored rules: %w", err)
	}

	for index := range set.Allowlists {
		err = set.Allowlists[index].compilePatterns()
		if err != nil {
			return nil, fmt.Errorf("compiling the global allowlist: %w", err)
		}
	}

	return &set, nil
}

// Scan reports the credentials in text, sorted by line. name is the file the
// text is the content of, an attachment's or a tool file's, and empty for
// text that is no file (an entry's body, a prompt, a command): rules and
// allowlists scoped to file paths apply only where a name is given.
func Scan(ctx context.Context, text string, name string) []Finding {
	set, err := loadRules()
	if err != nil {
		return []Finding{{RuleID: Incomplete, Line: 1}}
	}

	findings, err := set.scan(ctx, text, name)
	if err != nil {
		return []Finding{{RuleID: Incomplete, Line: 1}}
	}

	return findings
}

// Describe is what a refusal names: every distinct rule found on the first
// finding's line, sorted and comma-separated, and that line.
func Describe(findings []Finding) (string, int) {
	if len(findings) == 0 {
		return "", 0
	}

	line := findings[0].Line

	var rules []string

	for _, finding := range findings {
		if finding.Line == line && !slices.Contains(rules, finding.RuleID) {
			rules = append(rules, finding.RuleID)
		}
	}

	slices.Sort(rules)

	return strings.Join(rules, ", "), line
}

func (set *ruleSet) scan(ctx context.Context, text string, name string) ([]Finding, error) {
	if name != "" && pathAllowed(set.Allowlists, name) {
		return nil, nil
	}

	lowered := strings.ToLower(text)

	var findings []Finding

	for _, current := range set.Rules {
		err := ctx.Err()
		if err != nil {
			return nil, fmt.Errorf("scanning for credentials: %w", err)
		}

		if !current.mayMatch(lowered) {
			continue
		}

		err = current.compilePatterns()
		if err != nil {
			return nil, err
		}

		findings = append(findings, current.find(name, text, set.Allowlists)...)
	}

	slices.SortStableFunc(findings, func(a, b Finding) int { return a.Line - b.Line })

	return findings, nil
}

func (r *rule) mayMatch(lowered string) bool {
	if len(r.Keywords) == 0 {
		return true
	}

	for _, keyword := range r.Keywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}

	return false
}

func (r *rule) compilePatterns() error {
	r.compile.Do(func() {
		if r.Regex != "" {
			r.regex, r.err = regexp.Compile(r.Regex)
			if r.err != nil {
				r.err = fmt.Errorf("compiling rule %s: %w", r.ID, r.err)

				return
			}
		}

		if r.Path != "" {
			r.path, r.err = regexp.Compile(r.Path)
			if r.err != nil {
				r.err = fmt.Errorf("compiling the path of rule %s: %w", r.ID, r.err)

				return
			}
		}

		for index := range r.Allowlists {
			r.err = r.Allowlists[index].compilePatterns()
			if r.err != nil {
				r.err = fmt.Errorf("compiling an allowlist of rule %s: %w", r.ID, r.err)

				return
			}
		}
	})

	return r.err
}

func (r *rule) find(name string, text string, global []allowlist) []Finding {
	if r.path != nil {
		if name == "" || !r.path.MatchString(name) {
			return nil
		}

		if r.regex == nil {
			return []Finding{{RuleID: r.ID, Line: 1}}
		}
	}

	if r.regex == nil {
		return nil
	}

	if name != "" && pathAllowed(r.Allowlists, name) {
		return nil
	}

	var findings []Finding

	for _, bounds := range r.regex.FindAllStringIndex(text, -1) {
		match := strings.Trim(text[bounds[0]:bounds[1]], "\n")

		secret := r.secret(match)
		if secret == "" {
			continue
		}

		if r.Entropy != 0 && shannonEntropy(secret) <= r.Entropy {
			continue
		}

		line := linesAround(text, bounds[0], bounds[0]+len(match))
		if allowed(global, name, secret, match, line) || allowed(r.Allowlists, name, secret, match, line) {
			continue
		}

		findings = append(findings, Finding{RuleID: r.ID, Line: strings.Count(text[:bounds[0]], "\n") + 1})
	}

	return findings
}

// secret is the part of a match the entropy floor and allowlists judge: the
// rule's secretGroup, else its first non-empty group, else the whole match.
func (r *rule) secret(match string) string {
	groups := r.regex.FindStringSubmatch(match)
	if len(groups) < 2 {
		return match
	}

	if r.SecretGroup > 0 {
		if len(groups) <= r.SecretGroup {
			return ""
		}

		return groups[r.SecretGroup]
	}

	for _, group := range groups[1:] {
		if group != "" {
			return group
		}
	}

	return match
}

func (a *allowlist) compilePatterns() error {
	if a.regexes != nil || a.paths != nil {
		return nil
	}

	var err error

	a.regexes, err = compileAll(a.Regexes)
	if err != nil {
		return err
	}

	a.paths, err = compileAll(a.Paths)

	return err
}

func compileAll(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))

	for _, pattern := range patterns {
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("compiling %q: %w", pattern, err)
		}

		compiled = append(compiled, expression)
	}

	return compiled, nil
}

// pathAllowed reports whether an allowlist skips the whole file: an OR
// allowlist naming the path, or an AND allowlist checking nothing but paths.
func pathAllowed(lists []allowlist, name string) bool {
	for index := range lists {
		list := &lists[index]
		if len(list.paths) == 0 || !anyMatch(list.paths, name) {
			continue
		}

		if list.Condition != "AND" || (len(list.regexes) == 0 && len(list.Stopwords) == 0) {
			return true
		}
	}

	return false
}

func allowed(lists []allowlist, name string, secret string, match string, line string) bool {
	for index := range lists {
		list := &lists[index]

		target := secret

		switch list.RegexTarget {
		case "match":
			target = match
		case "line":
			target = line
		}

		regexAllowed := target != "" && anyMatch(list.regexes, target)
		stopword := containsStopword(list.Stopwords, secret)

		if list.Condition != "AND" {
			if regexAllowed || stopword {
				return true
			}

			continue
		}

		checks, passed := 0, 0

		if len(list.paths) > 0 {
			checks++

			if name != "" && anyMatch(list.paths, name) {
				passed++
			}
		}

		if len(list.regexes) > 0 {
			checks++

			if regexAllowed {
				passed++
			}
		}

		if len(list.Stopwords) > 0 {
			checks++

			if stopword {
				passed++
			}
		}

		if checks > 0 && passed == checks {
			return true
		}
	}

	return false
}

func anyMatch(patterns []*regexp.Regexp, text string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}

	return false
}

func containsStopword(stopwords []string, secret string) bool {
	if secret == "" {
		return false
	}

	lowered := strings.ToLower(secret)

	for _, word := range stopwords {
		if strings.Contains(lowered, word) {
			return true
		}
	}

	return false
}

// linesAround is the whole of every line the match spans, which the
// allowlists' "line" target reads.
func linesAround(text string, from int, to int) string {
	start := strings.LastIndexByte(text[:from], '\n') + 1

	end := strings.IndexByte(text[to:], '\n')
	if end < 0 {
		return text[start:]
	}

	return text[start : to+end]
}

func shannonEntropy(data string) float64 {
	if data == "" {
		return 0
	}

	counts := make(map[rune]int)
	for _, char := range data {
		counts[char]++
	}

	var entropy float64

	inverse := 1.0 / float64(len(data))

	for _, count := range counts {
		frequency := float64(count) * inverse
		entropy -= frequency * math.Log2(frequency)
	}

	return entropy
}
