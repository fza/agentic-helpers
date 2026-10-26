package secrets_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func ruleIDs(findings []secrets.Finding) []string {
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, finding.RuleID)
	}

	return ids
}

func TestScanFixtures(t *testing.T) {
	fixtures := test.SecretFixtures(t)

	for _, kind := range []string{"positive", "negative"} {
		entries, err := fs.ReadDir(fixtures, kind)
		if err != nil {
			t.Fatal(err)
		}

		for _, entry := range entries {
			rule := strings.TrimSuffix(entry.Name(), path.Ext(entry.Name()))

			t.Run(kind+"/"+rule, func(t *testing.T) {
				content, err := fs.ReadFile(fixtures, path.Join(kind, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}

				findings := secrets.Scan(context.Background(), string(content), "")

				raised := slices.Contains(ruleIDs(findings), rule)
				if kind == "positive" && !raised {
					t.Errorf("the sample should raise its rule, raised %v", ruleIDs(findings))
				}

				if kind == "negative" && raised {
					t.Error("the allowlisted or low-entropy sample should not raise its rule")
				}
			})
		}
	}
}

func TestScanCleanTextFindsNothing(t *testing.T) {
	findings := secrets.Scan(context.Background(), test.Fixture(t, "secrets/clean.txt"), "")

	if len(findings) != 0 {
		t.Errorf("prose without credentials should raise nothing, raised %v", ruleIDs(findings))
	}
}

func TestScanReportsTheLineOfTheMatch(t *testing.T) {
	text := "first line\nsecond line\n" + test.Fixture(t, "secrets/positive/github-pat.txt")

	findings := secrets.Scan(context.Background(), text, "")

	if len(findings) == 0 || findings[0].Line != 3 {
		t.Errorf("the finding should point at the line holding the token: %+v", findings)
	}
}

func TestScanIgnoresTheGitleaksAllowMarker(t *testing.T) {
	text := test.SecretFixture(t, "positive/github-pat.txt") + " # gitleaks:allow"

	findings := secrets.Scan(context.Background(), text, "")

	if !slices.Contains(ruleIDs(findings), "github-pat") {
		t.Error("the inline marker should override nothing")
	}
}

func TestScanAppliesPathRulesToNamedFiles(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		raises bool
	}{
		{name: "certificate bundle by name", file: "client.p12", raises: true},
		{name: "same content under another name", file: "client.txt", raises: false},
		{name: "text that is no file", file: "", raises: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := secrets.Scan(context.Background(), "binary", tc.file)

			if slices.Contains(ruleIDs(findings), "pkcs12-file") != tc.raises {
				t.Errorf("the path-scoped rule should fire only on its file names, raised %v", ruleIDs(findings))
			}
		})
	}
}

func TestScanSkipsGloballyAllowedFiles(t *testing.T) {
	findings := secrets.Scan(context.Background(), test.Fixture(t, "secrets/positive/github-pat.txt"), "go.sum")

	if len(findings) != 0 {
		t.Errorf("a file the global allowlist names should be skipped, raised %v", ruleIDs(findings))
	}
}

func TestScanReportsAnEndedContextAsIncomplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	findings := secrets.Scan(ctx, "plain prose", "")

	if !slices.Contains(ruleIDs(findings), secrets.Incomplete) {
		t.Error("a scan that could not finish should report itself rather than pass the text")
	}
}

func TestDescribeNamesTheRulesOfTheFirstLine(t *testing.T) {
	cases := []struct {
		name     string
		findings []secrets.Finding
		rules    string
		line     int
	}{
		{name: "nothing found", findings: nil, rules: "", line: 0},
		{
			name: "rules of the first line, sorted and distinct",
			findings: []secrets.Finding{{RuleID: "slack-bot-token", Line: 2}, {RuleID: "generic-api-key", Line: 2},
				{RuleID: "slack-bot-token", Line: 2}, {RuleID: "github-pat", Line: 5}},
			rules: "generic-api-key, slack-bot-token",
			line:  2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, line := secrets.Describe(tc.findings)

			if rules != tc.rules || line != tc.line {
				t.Errorf("the description should name the first line's rules and that line, got %q at %d", rules, line)
			}
		})
	}
}

type gitleaksSample struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type gitleaksSamples struct {
	Rule           string           `json:"rule"`
	TruePositives  []gitleaksSample `json:"truePositives"`
	FalsePositives []gitleaksSample `json:"falsePositives"`
}

// gitleaks' own samples hold the engine to gitleaks' verdicts rule by rule;
// a sample with a path is scanned as a file of that name.
func TestScanGitleaksSamples(t *testing.T) {
	fixtures := test.SecretFixtures(t)

	files, err := fs.ReadDir(fixtures, "gitleaks")
	if err != nil {
		t.Fatal(err)
	}

	if len(files) == 0 {
		t.Fatal("gitleaks' per-rule samples should be present")
	}

	for _, file := range files {
		data, err := fs.ReadFile(fixtures, path.Join("gitleaks", file.Name()))
		if err != nil {
			t.Fatal(err)
		}

		var samples gitleaksSamples

		err = json.Unmarshal(data, &samples)
		if err != nil {
			t.Fatalf("%s: %v", file.Name(), err)
		}

		t.Run(samples.Rule, func(t *testing.T) {
			for index, sample := range samples.TruePositives {
				findings := secrets.Scan(context.Background(), sample.Text, sample.Path)
				if !slices.Contains(ruleIDs(findings), samples.Rule) {
					t.Errorf("true positive %d should raise the rule, raised %v", index, ruleIDs(findings))
				}
			}

			for index, sample := range samples.FalsePositives {
				findings := secrets.Scan(context.Background(), sample.Text, sample.Path)
				if slices.Contains(ruleIDs(findings), samples.Rule) {
					t.Errorf("false positive %d should not raise the rule", index)
				}
			}
		})
	}
}
