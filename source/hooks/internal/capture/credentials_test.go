package capture_test

import (
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	captureLeaked = "agentic-capture: refused. The capture holds what looks like a credential ("
	immutable     = "; the graph is shared and immutable"
)

// header is a draft's leading block clearing validation in a project setting
// no listing prefix, before the extra lines given.
const header = "---\ntype: s\nlayer: tac\nkind: fact\n"

// writeDraft writes a draft of the header lines and body given and returns its
// path.
func (f *fixture) writeDraft(extra string, body string) string {
	f.t.Helper()

	path := filepath.Join(f.root, ".tmp", "drafts", "entry.md")
	test.WriteFile(f.t, path, header+extra+"---\n\n"+body+"\n")

	return path
}

// sentNothing asserts no `sdd new` ran past the probe: no pre-flight, no
// capture.
func (f *fixture) sentNothing() {
	f.t.Helper()

	if len(f.runner.CallsOf("dry-run"))+len(f.runner.CallsOf("capture")) > 0 {
		f.t.Errorf("nothing should reach sdd, got: %v", f.runner.Calls)
	}
}

func TestACaptureHoldingACredentialNeverReachesSdd(t *testing.T) {
	entries, err := fs.ReadDir(test.SecretFixtures(t), "positive")
	if err != nil {
		t.Fatalf("listing the samples: %v", err)
	}

	for _, entry := range entries {
		sample := test.SecretFixture(t, path.Join("positive", entry.Name()))

		t.Run("the body "+entry.Name(), func(t *testing.T) {
			f := newFixture(t, "")

			got := f.run(f.writeDraft("", "A body.\n"+sample))
			if got.code != 1 || !strings.Contains(got.stderr, captureLeaked) {
				t.Errorf("a credential in the body should refuse the capture, got: %+v", got)
			}

			f.sentNothing()
		})

		t.Run("an attached file "+entry.Name(), func(t *testing.T) {
			f := newFixture(t, "")
			test.WriteFile(t, filepath.Join(f.root, "keys", "sample.txt"), sample)

			got := f.run(f.writeDraft("attach: [keys/sample.txt]\n", "A body."))
			if got.code != 1 || !strings.Contains(got.stderr, captureLeaked) {
				t.Errorf("a credential in an attached file should refuse the capture, got: %+v", got)
			}

			f.sentNothing()
		})
	}
}

func TestTheCaptureRefusalNamesWhereAndTheLine(t *testing.T) {
	pat := test.SecretFixture(t, "positive/github-pat.txt")
	pem := test.SecretFixture(t, "positive/private-key.txt")
	token := strings.TrimSuffix(strings.TrimPrefix(pat, `export GITHUB_TOKEN="`), `"`)

	cases := []struct {
		name  string
		draft func(f *fixture) string
		says  func(draft string) string
	}{
		{
			name:  "a body line, counted in the draft",
			draft: func(f *fixture) string { return f.writeDraft("", "A body.\n\n"+pat) },
			says:  func(draft string) string { return "generic-api-key, github-pat, " + draft + ", line 9)" },
		},
		{
			name:  "a header value, decoded as its flag carries it",
			draft: func(f *fixture) string { return f.writeDraft(`title: "token: \x67hp_`+token[4:]+`"`+"\n", "A body.") },
			says:  func(draft string) string { return "generic-api-key, github-pat, " + draft + "'s --title, line 1)" },
		},
		{
			name: "an attached file under another name",
			draft: func(f *fixture) string {
				test.WriteFile(f.t, filepath.Join(f.root, "keys", "deploy.key"), pem)

				return f.writeDraft("attach: ['keys/deploy.key:deploy.txt']\n", "A body.")
			},
			says: func(string) string { return "private-key, keys/deploy.key, line 1)" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")
			draft := tc.draft(f)

			got := f.run(draft)

			want := captureLeaked + tc.says(draft) + immutable + "\n"
			if got.code != 1 || got.stderr != want {
				t.Errorf("the refusal should name the rules, where and the line, got %q", got.stderr)
			}

			if strings.Contains(got.stderr, "ghp_") || strings.Contains(got.stderr, "PRIVATE KEY") {
				t.Error("the refusal should never print the credential")
			}
		})
	}
}

func TestWhatTheCaptureCannotScanIsRefused(t *testing.T) {
	cases := []struct {
		name   string
		attach string
		says   string
	}{
		{
			name:   "standard input",
			attach: "attach: ['-:pasted.txt']\n",
			says:   "agentic-capture: refused. The capture attaches standard input as pasted.txt, which cannot be scanned for a credential; attach a file instead",
		},
		{
			name:   "a file not on disk",
			attach: "attach: [keys/absent.key]\n",
			says:   "agentic-capture: refused. The capture names keys/absent.key, which cannot be read to scan for a credential",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "")

			got := f.run(f.writeDraft(tc.attach, "A body."))
			if got.code != 1 || got.stderr != tc.says+"\n" {
				t.Errorf("an attachment that cannot be scanned should refuse the capture, got: %+v", got)
			}

			f.sentNothing()
		})
	}
}

func TestTheEntrysTypeAndLayerAreScannedToo(t *testing.T) {
	pat := test.SecretFixture(t, "positive/github-pat.txt")
	token := strings.TrimSuffix(strings.TrimPrefix(pat, `export GITHUB_TOKEN="`), `"`)

	f := newFixture(t, "")
	draft := filepath.Join(f.root, ".tmp", "drafts", "entry.md")
	test.WriteFile(t, draft, "---\ntype: s\nlayer: "+token+"\nkind: fact\n---\n\nA body.\n")

	got := f.run(draft)
	if got.code != 1 || !strings.Contains(got.stderr, "github-pat, "+draft+"'s layer, line 1)") {
		t.Errorf("a credential in the layer should refuse the capture, got: %+v", got)
	}

	f.sentNothing()
}

func TestAnAttachmentIsScannedByItsNameInTheEntry(t *testing.T) {
	f := newFixture(t, "")
	test.WriteFile(t, filepath.Join(f.root, "bundle.bin"), "opaque bytes")

	got := f.run(f.writeDraft("attach: ['bundle.bin:client.p12']\n", "A body."))
	if got.code != 1 || !strings.Contains(got.stderr, captureLeaked+"pkcs12-file, bundle.bin, line 1)") {
		t.Errorf("a file named as a certificate bundle in the entry should refuse the capture, got: %+v", got)
	}
}

func TestAnAttachmentByAbsolutePathIsReadThere(t *testing.T) {
	f := newFixture(t, "")
	elsewhere := filepath.Join(t.TempDir(), "run.log")
	test.WriteFile(t, elsewhere, test.SecretFixture(t, "clean.txt"))

	got := f.run(f.writeDraft("attach: ['"+elsewhere+"']\n", "A body."))
	if got.code != 0 {
		t.Errorf("a clean file attached by its absolute path should capture, got: %+v", got)
	}
}

func TestACleanAttachmentReachesSdd(t *testing.T) {
	f := newFixture(t, "")
	test.WriteFile(t, filepath.Join(f.root, "logs", "run.log"), test.SecretFixture(t, "clean.txt"))

	got := f.run(f.writeDraft("attach: ['logs/run.log:run.log']\n", "A body."))
	if got.code != 0 {
		t.Fatalf("a clean attachment should capture, got: %+v", got)
	}

	captures := f.runner.CallsOf("capture")
	if len(captures) != 1 || !slices.Contains(captures[0], "logs/run.log:run.log") {
		t.Errorf("the attachment should reach sdd as the header names it, got: %v", f.runner.Calls)
	}
}

func TestAFileChangedBeforeTheWriteIsScannedAgain(t *testing.T) {
	cases := []struct {
		name   string
		config string
		stock  bool
		during string
	}{
		{name: "during the pre-flight", during: "dry-run"},
		{name: "during the lint, before the one call stock sdd checks and writes in", config: "grounding/lint.yaml", stock: true, during: "lint"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.config)
			f.runner.Stock = tc.stock
			attached := filepath.Join(f.root, "logs", "run.log")
			test.WriteFile(t, attached, test.SecretFixture(t, "clean.txt"))

			f.runner.Before = func(argv []string) {
				lint := argv[0] == "sh"
				dry := slices.Contains(argv, "--dry-run")

				if tc.during == "lint" && lint || tc.during == "dry-run" && dry {
					test.WriteFile(t, attached, test.SecretFixture(t, "positive/private-key.txt"))
				}
			}

			got := f.run(f.writeDraft("attach: [logs/run.log]\n", "A body."))
			if got.code != 1 || !strings.Contains(got.stderr, captureLeaked+"private-key, logs/run.log, line 1)") {
				t.Errorf("a file changed before sdd writes should refuse the capture, got: %+v", got)
			}

			if len(f.runner.CallsOf("capture")) > 0 {
				t.Errorf("no write should run, got: %v", f.runner.Calls)
			}
		})
	}
}
