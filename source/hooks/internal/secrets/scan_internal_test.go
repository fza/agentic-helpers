package secrets

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestScanCompilesNoPatternForTextWithoutKeywords(t *testing.T) {
	set, err := parseRules(rulesJSON)
	if err != nil {
		t.Fatal(err)
	}

	_, err = set.scan(context.Background(), "zzzz qqqq", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, current := range set.Rules {
		if len(current.Keywords) > 0 && current.regex != nil {
			t.Errorf("rule %s compiled without any of its keywords in the text", current.ID)
		}
	}
}

func TestEveryVendoredRuleCompiles(t *testing.T) {
	set, err := parseRules(rulesJSON)
	if err != nil {
		t.Fatal(err)
	}

	for _, current := range set.Rules {
		err = current.compilePatterns()
		if err != nil {
			t.Error(err)
		}
	}
}

// The vendored rules are the sdd fork's file at 26f1aaec byte for byte, so
// both tools refuse the same strings; the fork's git blob names that file.
func TestVendoredRulesMatchThePin(t *testing.T) {
	const forkBlob = "96a5484f95768465f2a267a03f09d1c1e6912b3c"

	digest := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(rulesJSON))), rulesJSON...))
	if hex.EncodeToString(digest[:]) != forkBlob {
		t.Error("rules.json should equal the sdd fork's vendored rules byte for byte")
	}

	set, err := parseRules(rulesJSON)
	if err != nil {
		t.Fatal(err)
	}

	if len(set.Allowlists) == 0 || len(set.Rules) == 0 {
		t.Error("the vendored file should carry the global allowlist and the rules")
	}

	var pinned struct {
		Source struct {
			Repository string `json:"repository"`
			File       string `json:"file"`
			Version    string `json:"version"`
			Commit     string `json:"commit"`
		} `json:"source"`
	}

	err = json.Unmarshal(rulesJSON, &pinned)
	if err != nil {
		t.Fatal(err)
	}

	if pinned.Source.Repository != "github.com/gitleaks/gitleaks" || pinned.Source.File != "config/gitleaks.toml" ||
		pinned.Source.Version != "v8.30.1" || pinned.Source.Commit != "83d9cd684c87d95d656c1458ef04895a7f1cbd8e" {
		t.Errorf("the source should name the pinned gitleaks release: %+v", pinned.Source)
	}

	license, err := os.ReadFile("LICENSE-gitleaks")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(license), "MIT License") || !strings.Contains(string(license), "Zachary Rice") {
		t.Error("gitleaks' MIT license notice should lie beside the vendored rules")
	}
}

func compiled(t *testing.T, set *ruleSet) *ruleSet {
	t.Helper()

	for index := range set.Allowlists {
		err := set.Allowlists[index].compilePatterns()
		if err != nil {
			t.Fatal(err)
		}
	}

	return set
}

func token(id string, lists ...allowlist) *rule {
	return &rule{ID: id, Regex: `(?i)tok_([a-z0-9]{8})`, Allowlists: lists}
}

// Every case scans text with a hand-made rule set whose verdict hinges on
// one check of the engine, so breaking that check flips the case.
func TestScanSemantics(t *testing.T) {
	cases := []struct {
		name   string
		set    *ruleSet
		text   string
		file   string
		raises bool
	}{
		{
			name:   "a secret at the entropy floor is skipped",
			set:    &ruleSet{Rules: []*rule{{ID: "floor", Regex: `tok_([a-z]+)`, Entropy: shannonEntropy("abcd")}}},
			text:   "tok_abcd",
			raises: false,
		},
		{
			name:   "a secret above the entropy floor is found",
			set:    &ruleSet{Rules: []*rule{{ID: "floor", Regex: `tok_([a-z]+)`, Entropy: shannonEntropy("abcd")}}},
			text:   "tok_abcde",
			raises: true,
		},
		{
			name: "the entropy floor judges the secretGroup, not the first group",
			set: &ruleSet{Rules: []*rule{{ID: "group", Regex: `key=([a-z]+)-([a-z0-9]+)`, SecretGroup: 2,
				Entropy: 3}}},
			text:   "key=aaaa-q8w7e6r5t4",
			raises: true,
		},
		{
			name:   "a secretGroup the match does not hold yields no secret",
			set:    &ruleSet{Rules: []*rule{{ID: "group", Regex: `key=([a-z]+)`, SecretGroup: 3}}},
			text:   "key=abcdef",
			raises: false,
		},
		{
			name: "without a secretGroup the first non-empty group is the secret",
			set: &ruleSet{Rules: []*rule{{ID: "first", Regex: `(pre_)?tok_([a-z0-9]+)`,
				Allowlists: []allowlist{{Regexes: []string{`^safe1234$`}}}}}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name: "an empty first group never becomes the secret",
			set: &ruleSet{Rules: []*rule{{ID: "first", Regex: `(pre_)?tok_([a-z0-9]+)`,
				Allowlists: []allowlist{{Regexes: []string{`^safe1234$`}}}}}},
			text:   "tok_q8w7e6r5",
			raises: true,
		},
		{
			name: "a rule without groups judges the whole match",
			set: &ruleSet{Rules: []*rule{{ID: "whole", Regex: `tok_[a-z0-9]+`,
				Allowlists: []allowlist{{Regexes: []string{`^tok_safe1234$`}}}}}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name:   "a rule without groups finds the whole match",
			set:    &ruleSet{Rules: []*rule{{ID: "whole", Regex: `tok_[a-z0-9]+`}}},
			text:   "tok_q8w7e6r5",
			raises: true,
		},
		{
			name:   "an allowlist without a target tests the secret",
			set:    &ruleSet{Rules: []*rule{token("target", allowlist{Regexes: []string{`^safe1234$`}})}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name:   "the match target tests the whole match",
			set:    &ruleSet{Rules: []*rule{token("target", allowlist{RegexTarget: "match", Regexes: []string{`^tok_safe1234$`}})}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name:   "the line target tests every full line the match spans",
			set:    &ruleSet{Rules: []*rule{token("target", allowlist{RegexTarget: "line", Regexes: []string{`^example: .* end$`}})}},
			text:   "before\nexample: tok_q8w7e6r5 end\nafter",
			raises: false,
		},
		{
			name:   "the line target sees no other line",
			set:    &ruleSet{Rules: []*rule{token("target", allowlist{RegexTarget: "line", Regexes: []string{`before`}})}},
			text:   "before\nexample: tok_q8w7e6r5 end\nafter",
			raises: true,
		},
		{
			name:   "a stopword the lowercased secret contains allows it",
			set:    &ruleSet{Rules: []*rule{token("stop", allowlist{Stopwords: []string{"safe"}})}},
			text:   "tok_SAFE1234",
			raises: false,
		},
		{
			name:   "a stopword outside the secret allows nothing",
			set:    &ruleSet{Rules: []*rule{token("stop", allowlist{Stopwords: []string{"safe"}})}},
			text:   "safe: tok_q8w7e6r5",
			raises: true,
		},
		{
			name: "an OR allowlist passes on a stopword alone",
			set: &ruleSet{Rules: []*rule{token("or", allowlist{Regexes: []string{`^never$`},
				Stopwords: []string{"safe"}})}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name: "an AND allowlist needs its regex as well as its stopword",
			set: &ruleSet{Rules: []*rule{token("and", allowlist{Condition: "AND", Regexes: []string{`^never$`},
				Stopwords: []string{"safe"}})}},
			text:   "tok_safe1234",
			raises: true,
		},
		{
			name: "an AND allowlist passes when every check does",
			set: &ruleSet{Rules: []*rule{token("and", allowlist{Condition: "AND", Regexes: []string{`1234$`},
				Stopwords: []string{"safe"}})}},
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name: "an AND allowlist with paths fails on text that is no file",
			set: &ruleSet{Rules: []*rule{token("and", allowlist{Condition: "AND", Paths: []string{`\.md$`},
				Regexes: []string{`1234$`}})}},
			text:   "tok_safe1234",
			raises: true,
		},
		{
			name: "an AND allowlist with paths passes on its file",
			set: &ruleSet{Rules: []*rule{token("and", allowlist{Condition: "AND", Paths: []string{`\.md$`},
				Regexes: []string{`1234$`}})}},
			text:   "tok_safe1234",
			file:   "notes.md",
			raises: false,
		},
		{
			name:   "the global allowlist runs before the rule's own",
			set:    compiled(t, &ruleSet{Allowlists: []allowlist{{Stopwords: []string{"safe"}}}, Rules: []*rule{token("global")}}),
			text:   "tok_safe1234",
			raises: false,
		},
		{
			name:   "a rule runs only when one of its keywords occurs",
			set:    &ruleSet{Rules: []*rule{{ID: "keyword", Regex: `secret([0-9]{4})`, Keywords: []string{"zzz"}}}},
			text:   "secret1234",
			raises: false,
		},
		{
			name:   "a keyword occurring in any case lets the rule run",
			set:    &ruleSet{Rules: []*rule{{ID: "keyword", Regex: `secret([0-9]{4})`, Keywords: []string{"zzz"}}}},
			text:   "ZZZ secret1234",
			raises: true,
		},
		{
			name:   "a path rule fires on its file name alone",
			set:    &ruleSet{Rules: []*rule{{ID: "path", Path: `\.p12$`}}},
			file:   "client.p12",
			raises: true,
		},
		{
			name:   "a path rule never fires on text that is no file",
			set:    &ruleSet{Rules: []*rule{{ID: "path", Path: `\.p12$`}}},
			text:   "client.p12",
			raises: false,
		},
		{
			name:   "a path rule with a pattern fires only inside its files",
			set:    &ruleSet{Rules: []*rule{{ID: "path", Path: `\.env$`, Regex: `tok_([a-z0-9]{8})`}}},
			text:   "tok_q8w7e6r5",
			file:   "notes.md",
			raises: false,
		},
		{
			name:   "a path rule with a pattern fires on a match inside its file",
			set:    &ruleSet{Rules: []*rule{{ID: "path", Path: `\.env$`, Regex: `tok_([a-z0-9]{8})`}}},
			text:   "tok_q8w7e6r5",
			file:   "prod.env",
			raises: true,
		},
		{
			name:   "a rule's OR allowlist naming the file skips it",
			set:    &ruleSet{Rules: []*rule{token("skip", allowlist{Paths: []string{`\.lock$`}})}},
			text:   "tok_q8w7e6r5",
			file:   "deps.lock",
			raises: false,
		},
		{
			name: "a rule's AND allowlist of paths alone skips the file",
			set: &ruleSet{Rules: []*rule{token("skip", allowlist{Condition: "AND",
				Paths: []string{`\.lock$`}})}},
			text:   "tok_q8w7e6r5",
			file:   "deps.lock",
			raises: false,
		},
		{
			name: "a rule's AND allowlist with paths and a regex skips no whole file",
			set: &ruleSet{Rules: []*rule{token("skip", allowlist{Condition: "AND", Paths: []string{`\.lock$`},
				Regexes: []string{`^never$`}})}},
			text:   "tok_q8w7e6r5",
			file:   "deps.lock",
			raises: true,
		},
		{
			name: "the global allowlist naming the file skips it",
			set: compiled(t, &ruleSet{Allowlists: []allowlist{{Paths: []string{`\.lock$`}}},
				Rules: []*rule{{ID: "path", Path: `\.lock$`}}}),
			file:   "deps.lock",
			raises: false,
		},
		{
			name: "a global AND allowlist of paths alone skips the file before a path rule fires",
			set: compiled(t, &ruleSet{Allowlists: []allowlist{{Condition: "AND", Paths: []string{`\.lock$`}}},
				Rules: []*rule{{ID: "path", Path: `\.lock$`}}}),
			file:   "deps.lock",
			raises: false,
		},
		{
			name: "the match target sees the match trimmed of newlines",
			set: &ruleSet{Rules: []*rule{{ID: "trim", Regex: `\n?tok_([a-z0-9]{8})\n`,
				Allowlists: []allowlist{{RegexTarget: "match", Regexes: []string{`^tok_safe1234$`}}}}}},
			text:   "x\ntok_safe1234\ny",
			raises: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := tc.set.scan(context.Background(), tc.text, tc.file)
			if err != nil {
				t.Fatal(err)
			}

			if (len(findings) > 0) != tc.raises {
				t.Errorf("the rule's verdict should follow the engine's semantics, found %v", findings)
			}
		})
	}
}

func TestScanSortsFindingsByLineAcrossRules(t *testing.T) {
	set := &ruleSet{Rules: []*rule{{ID: "late", Regex: `late_([a-z0-9]{8})`}, {ID: "early", Regex: `early_([a-z0-9]{8})`}}}

	findings, err := set.scan(context.Background(), "early_q8w7e6r5\nlate_q8w7e6r5", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(findings) != 2 || findings[0].RuleID != "early" {
		t.Errorf("the first finding should be the one on the earliest line, whichever rule found it: %v", findings)
	}
}

func TestScanRefusesToFinishOnABrokenRule(t *testing.T) {
	set := &ruleSet{Rules: []*rule{{ID: "broken", Regex: `(`}}}

	_, err := set.scan(context.Background(), "any text", "")
	if err == nil {
		t.Error("a rule that cannot compile should stop the scan")
	}
}
