package memory_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	releaseNotice = "agentic-helpers: release 2.18.0 is out, this machine runs 2.17.0; tell the user in prose"
	updateCommand = "claude plugin update agentic-helpers@agentic-helpers"
	autoSnippet   = `{"extraKnownMarketplaces": {"agentic-helpers": {"source": {"source": "github", "repo": "fza/agentic-helpers"}, "autoUpdate": true}}}`
)

// onStaleRelease runs the harness as release 2.17.0 while GitHub's latest is
// 2.18.0.
func (h *harness) onStaleRelease() *harness {
	h.releases = &test.Releases{Tag: "v2.18.0"}
	h.version = "2.17.0"

	return h
}

func TestAClaimTellsOfANewerReleaseOncePerLocalDay(t *testing.T) {
	h := newHarness(t).onStaleRelease()

	out := h.must("", "claim", "main")
	for _, part := range []string{releaseNotice, updateCommand, autoSnippet, "update_notice: false"} {
		if !strings.Contains(out, part) {
			t.Errorf("a claim on a stale release should tell of the newer one, the update command, the snippet and the option, missing %q in:\n%s", part, out)
		}
	}

	other := newHarnessSharing(h)
	if out := other.must("", "claim", "rev"); strings.Contains(out, "is out") {
		t.Errorf("another seat's claim the same day should not repeat the notice, got:\n%s", out)
	}

	h.must("", "release")

	if out := h.must("", "claim", "main"); strings.Contains(out, "is out") {
		t.Errorf("a second claim the same day should not repeat the notice, got:\n%s", out)
	}

	h.must("", "release")
	h.clock.Advance(15 * time.Hour)

	if out := h.must("", "claim", "main"); !strings.Contains(out, releaseNotice) {
		t.Errorf("a claim the next local day should tell of the newer release again, got:\n%s", out)
	}

	if asked := len(h.releases.Requests()); asked != 1 {
		t.Errorf("GitHub should be asked once in 24 hours whatever claims, asked %d times", asked)
	}
}

func TestAClaimOverMCPTellsOfANewerRelease(t *testing.T) {
	h := newHarness(t).onStaleRelease()

	message, _ := decoded(t, mcpText(t, h.mcp(`{"name":"claim","arguments":{"name":"main"}}`)))["message"].(string)
	if !strings.Contains(message, releaseNotice) || !strings.Contains(message, autoSnippet) {
		t.Errorf("a claim over MCP should tell of the newer release as the CLI's does, got:\n%s", message)
	}
}

func TestAResumeTellsOfNoNewerRelease(t *testing.T) {
	path := sessionTranscript(t, "parent.jsonl")
	parent := seatedParent(t, path)
	parent.processes.Kill(test.OwnerPID)

	resumed := resumedOrForked(parent, test.Session).onStaleRelease()

	out := resumed.must(resumed.startHook("resume", path), "session-start")
	if !strings.HasPrefix(out, "agentic-memory: seat main, ") || strings.Contains(out, "is out") {
		t.Errorf("a resumed session taking its seat back should not tell of a newer release, got:\n%s", out)
	}

	if asked := len(resumed.releases.Requests()); asked != 0 {
		t.Errorf("a resume should not ask GitHub, asked %d times", asked)
	}

	if out := resumed.must("", "claim", "main"); !strings.Contains(out, releaseNotice) {
		t.Errorf("the session's own claim after a resume should still tell of the newer release, got:\n%s", out)
	}
}

func TestAClaimForAnotherSessionTellsOfNoNewerRelease(t *testing.T) {
	board := newHarness(t).onStaleRelease()

	if out := board.must("", "claim", "impl", "--session", test.OtherSession); strings.Contains(out, "is out") {
		t.Errorf("a claim for another session should not tell of a newer release, got:\n%s", out)
	}

	if asked := len(board.releases.Requests()); asked != 0 {
		t.Errorf("a claim for another session should not ask GitHub, asked %d times", asked)
	}

	if out := board.must("", "claim", "main"); !strings.Contains(out, releaseNotice) {
		t.Errorf("the caller's own claim should still tell of the newer release, got:\n%s", out)
	}
}

func TestAClaimWithoutAReleaseClientAsksNothing(t *testing.T) {
	h := newHarness(t)
	h.version = "2.17.0"

	if out := h.must("", "claim", "main"); strings.Contains(out, "is out") {
		t.Errorf("a run given no way to ask GitHub should claim without a notice, got:\n%s", out)
	}
}

func TestAClaimTellsOfNoReleaseClaudeCodeInstallsItselfOrTheUserSilenced(t *testing.T) {
	cases := []struct {
		name    string
		place   func(h *harness) string
		fixture string
		version string
		err     error
		asks    bool
	}{
		{name: "update_notice set to false", place: userSettings, fixture: "memory/settings-update-notice-off.json"},
		{name: "auto-update on in the user settings", place: userSettings, fixture: "updates/settings-auto-update-on.json"},
		{name: "auto-update on in the project settings", place: projectSettings, fixture: "updates/settings-auto-update-on.json"},
		{name: "auto-update on in the project's local settings", place: localSettings, fixture: "updates/settings-auto-update-on.json"},
		{name: "auto-update on in known_marketplaces.json", place: knownMarketplaces, fixture: "updates/known-auto-update-on.json"},
		{name: "a marketplace pinned to a ref", place: knownMarketplaces, fixture: "updates/known-pinned.json"},
		{name: "a go install build", version: "dev"},
		{name: "an equal running version", version: "2.18.0", asks: true},
		{name: "a newer running version", version: "2.19.0", asks: true},
		{name: "a failed check", err: errors.New("dial tcp: no route to host"), asks: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t).onStaleRelease()
			h.releases.Err = tc.err

			if tc.version != "" {
				h.version = tc.version
			}

			if tc.place != nil {
				test.WriteFile(t, tc.place(h), test.Fixture(t, tc.fixture))
			}

			if out := h.must("", "claim", "main"); strings.Contains(out, "is out") {
				t.Errorf("a claim should tell of a newer release only where it waits for the user, got:\n%s", out)
			}

			if asked := len(h.releases.Requests()) > 0; asked != tc.asks {
				t.Errorf("GitHub should be asked only where its answer can lead to a notice, asked %v", asked)
			}
		})
	}
}

func userSettings(h *harness) string { return h.settings }

func projectSettings(h *harness) string { return filepath.Join(h.repo, ".claude", "settings.json") }

func localSettings(h *harness) string { return filepath.Join(h.repo, ".claude", "settings.local.json") }

func knownMarketplaces(h *harness) string {
	return filepath.Join(h.home, ".claude", "plugins", "known_marketplaces.json")
}
