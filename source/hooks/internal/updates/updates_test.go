package updates_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/updates"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	updateCommand = "claude plugin update agentic-helpers@agentic-helpers"
	snippet       = `{"extraKnownMarketplaces": {"agentic-helpers": {"source": {"source": "github", "repo": "fza/agentic-helpers"}, "autoUpdate": true}}}`
)

// berlin keeps the local day apart from the UTC one: 23:30 there is still
// the same day in UTC, 00:30 the next local day is not.
var berlin = time.FixedZone("CET", 3600)

type machine struct {
	dir      string
	managed  string
	user     string
	project  string
	local    string
	known    string
	now      time.Time
	releases *test.Releases
	running  string
}

func newMachine(t *testing.T) *machine {
	t.Helper()

	dir := t.TempDir()

	return &machine{
		dir:      dir,
		managed:  filepath.Join(dir, "managed", "managed-settings.json"),
		user:     filepath.Join(dir, "config", "settings.json"),
		project:  filepath.Join(dir, "repo", ".claude", "settings.json"),
		local:    filepath.Join(dir, "repo", ".claude", "settings.local.json"),
		known:    updates.KnownMarketplaces(filepath.Join(dir, "config")),
		now:      time.Date(2026, 10, 30, 21, 0, 0, 0, berlin),
		releases: &test.Releases{Tag: "v2.18.0"},
		running:  "2.17.0",
	}
}

func (m *machine) notice() []string {
	check := updates.Check{
		Client: m.releases, Data: filepath.Join(m.dir, "data"), Running: m.running,
		Settings: []string{m.managed, m.user, m.project, m.local}, Known: m.known,
		Now: func() time.Time { return m.now },
	}

	return check.Notice(context.Background())
}

func TestANewerReleaseShowsOncePerLocalDay(t *testing.T) {
	m := newMachine(t)
	test.WriteFile(t, m.known, test.Fixture(t, "updates/known-manual.json"))

	shown := strings.Join(m.notice(), "\n")
	for _, part := range []string{"2.18.0", "runs 2.17.0", updateCommand, snippet, "~/.claude/settings.json", "update_notice: false", "tell the user"} {
		if !strings.Contains(shown, part) {
			t.Errorf("the notice should name both versions, the update command, the snippet and the option, missing %q in:\n%s", part, shown)
		}
	}

	m.now = m.now.Add(150 * time.Minute)

	if again := m.notice(); again != nil {
		t.Errorf("a second claim the same local day should show nothing, got %v", again)
	}

	m.now = m.now.Add(time.Hour)

	if next := m.notice(); next == nil {
		t.Error("the next local day should show the notice again")
	}

	if got := len(m.releases.Requests()); got != 1 {
		t.Errorf("a cached answer younger than a day should be used without asking again, asked %d times", got)
	}
}

func TestGitHubIsAskedAtMostOncePerDayAndAFailureWaitsForTheNext(t *testing.T) {
	m := newMachine(t)
	m.releases.Err = errors.New("dial tcp: no route to host")

	if shown := m.notice(); shown != nil {
		t.Errorf("a failed check should show nothing, got %v", shown)
	}

	m.releases.Err = nil
	m.now = m.now.Add(23 * time.Hour)

	if shown := m.notice(); shown != nil {
		t.Errorf("a failed check should not be retried before a day has passed, got %v", shown)
	}

	if got := len(m.releases.Requests()); got != 1 {
		t.Errorf("GitHub should be asked once within a day, asked %d times", got)
	}

	m.now = m.now.Add(time.Hour)

	if shown := m.notice(); shown == nil {
		t.Error("a day after the failed check, a newer release should show")
	}

	if got := len(m.releases.Requests()); got != 2 {
		t.Errorf("a day after the last check GitHub should be asked again, asked %d times", got)
	}
}

func TestTheCheckAsksForTheLatestReleaseWithinThreeSeconds(t *testing.T) {
	m := newMachine(t)
	before := time.Now()

	m.notice()

	requests := m.releases.Requests()
	if len(requests) != 1 {
		t.Fatalf("one request should be sent, got %d", len(requests))
	}

	request := requests[0]
	if request.Method != http.MethodGet || request.URL.String() != updates.LatestURL {
		t.Errorf("the request should get the latest release of fza/agentic-helpers, got %s %s", request.Method, request.URL)
	}

	if !strings.HasPrefix(request.Header.Get("User-Agent"), "agentic-helpers/") {
		t.Errorf("the request should name agentic-helpers as its user agent, which GitHub's API requires, got %q", request.Header.Get("User-Agent"))
	}

	deadline, bounded := request.Context().Deadline()
	if !bounded || deadline.Sub(before) > updates.RequestTimeout+time.Second {
		t.Errorf("the request should be bounded by the check's timeout, deadline %v set %v", deadline, bounded)
	}
}

func TestTheCacheLivesInTheDataHome(t *testing.T) {
	m := newMachine(t)
	m.notice()

	_, err := os.Stat(filepath.Join(m.dir, "data", updates.CacheName))
	if err != nil {
		t.Errorf("the check should cache its answer in the data home: %v", err)
	}
}

func TestAClaimDuringAnotherClaimsCheckShowsNothing(t *testing.T) {
	m := newMachine(t)
	path := filepath.Join(m.dir, "data", updates.LockName)
	test.WriteFile(t, path, "")

	other, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening the lock: %v", err)
	}
	defer other.Close()

	err = filelock.Exclusive(other)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}

	if shown := m.notice(); shown != nil {
		t.Errorf("a claim while another checks should leave the notice to that claim, got %v", shown)
	}

	if asked := len(m.releases.Requests()); asked != 0 {
		t.Errorf("a claim while another checks should not ask GitHub as well, asked %d times", asked)
	}

	_ = filelock.Unlock(other)

	if shown := m.notice(); shown == nil {
		t.Error("once the other claim is done, a claim should check again")
	}
}

func TestWhenTheNoticeShows(t *testing.T) {
	cases := []struct {
		name    string
		place   func(m *machine) string
		fixture string
		running string
		tag     string
		status  int
		body    string
		err     error
		shows   bool
		asks    bool
	}{
		{name: "nothing configured", shows: true, asks: true},
		{name: "auto-update off in the user settings", place: user, fixture: "settings-auto-update-off.json", shows: true, asks: true},
		{name: "auto-update on for another marketplace", place: user, fixture: "settings-other-auto-update-on.json", shows: true, asks: true},
		{name: "known marketplace without auto-update", place: known, fixture: "known-manual.json", shows: true, asks: true},
		{name: "a running version with a v", running: "v2.17.0", shows: true, asks: true},
		{name: "auto-update on in the managed settings", place: managed, fixture: "settings-auto-update-on.json"},
		{name: "auto-update on in the user settings", place: user, fixture: "settings-auto-update-on.json"},
		{name: "auto-update on in the project settings", place: project, fixture: "settings-auto-update-on.json"},
		{name: "auto-update on in the project's local settings", place: local, fixture: "settings-auto-update-on.json"},
		{name: "auto-update on in known_marketplaces.json", place: known, fixture: "known-auto-update-on.json"},
		{name: "a marketplace pinned to a ref", place: known, fixture: "known-pinned.json"},
		{name: "a marketplace pinned to a commit", place: user, fixture: "settings-pinned-sha.json"},
		{name: "a go install build", running: "dev"},
		{name: "a pre-release build", running: "2.17.0-rc.1"},
		{name: "a build of two numbers", running: "2.17"},
		{name: "a build with a signed number", running: "2.+17.0"},
		{name: "an equal running version", tag: "v2.17.0", asks: true},
		{name: "a newer running version", running: "2.18.1", asks: true},
		{name: "a newer running major", running: "3.0.0", tag: "v2.99.99", asks: true},
		{name: "a failed request", err: errors.New("connection refused"), asks: true},
		{name: "a slow request", err: context.DeadlineExceeded, asks: true},
		{name: "a refusal by GitHub", status: http.StatusForbidden, asks: true},
		{name: "an answer that is no JSON", body: "<html>", asks: true},
		{name: "a tag that is no version", tag: "nightly", asks: true},
		{name: "an answer past the size bound", body: `{"tag_name":"v2.18.0","body":"` + strings.Repeat("a", 2<<20) + `"}`, asks: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			m.releases = &test.Releases{Tag: "v2.18.0", Status: tc.status, Body: tc.body, Err: tc.err}

			if tc.tag != "" {
				m.releases.Tag = tc.tag
			}

			if tc.running != "" {
				m.running = tc.running
			}

			if tc.place != nil {
				test.WriteFile(t, tc.place(m), test.Fixture(t, "updates/"+tc.fixture))
			}

			shown := m.notice()
			if (shown != nil) != tc.shows {
				t.Errorf("the notice should show only for a newer release Claude Code will not install itself, got %v", shown)
			}

			if asked := len(m.releases.Requests()) > 0; asked != tc.asks {
				t.Errorf("GitHub should be asked only where the answer can lead to a notice, asked %v", asked)
			}
		})
	}
}

func managed(m *machine) string { return m.managed }
func user(m *machine) string    { return m.user }
func project(m *machine) string { return m.project }
func local(m *machine) string   { return m.local }
func known(m *machine) string   { return m.known }
