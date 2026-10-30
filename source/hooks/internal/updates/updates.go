// Package updates tells a seat claim that a newer agentic-helpers release
// exists which Claude Code will not install by itself: Claude Code leaves
// plugin auto-update off for every third-party marketplace, and a
// marketplace's own manifest cannot turn it on. GitHub is asked at most once
// per CheckEvery per machine, and the notice shows at most once per local day;
// both are kept in CacheName in the data home.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const (
	LatestURL      = "https://api.github.com/repos/fza/agentic-helpers/releases/latest"
	CacheName      = "update-check.json"
	Marketplace    = "agentic-helpers"
	CheckEvery     = 24 * time.Hour
	RequestTimeout = 3 * time.Second
)

// LockName is held while a claim checks, beside the cache it guards: the
// cache is replaced whole, so a lock on it would hold the replaced file.
const LockName = ".update-check.lock"

// maxAnswer bounds the bytes read of GitHub's answer; a release's JSON stays
// far below it.
const maxAnswer = 1 << 20

var ErrAnswer = errors.New("no release in GitHub's answer")

type Client interface {
	Do(request *http.Request) (*http.Response, error)
}

// Check is one claim's look for a newer release. Settings are the Claude Code
// settings files whose `extraKnownMarketplaces` may turn auto-update on or pin
// the marketplace (managed, user, project); Known is Claude Code's
// `known_marketplaces.json`; Data is the data home.
type Check struct {
	Client   Client
	Data     string
	Running  string
	Settings []string
	Known    string
	Now      func() time.Time
}

type cache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest,omitempty"`
	Shown   string    `json:"shown,omitempty"`
}

// KnownMarketplaces is the file Claude Code lists every added marketplace in,
// under its configuration folder.
func KnownMarketplaces(configDir string) string {
	return filepath.Join(configDir, "plugins", "known_marketplaces.json")
}

// Notice is the lines a claim appends, nothing where the running build is no
// release, the marketplace updates itself or is pinned, the notice showed
// today already, or no newer release is known. Nothing here fails a claim: an
// unreadable cache or a failed request reads as no newer release. A claim
// finding another claim of the machine inside the check shows nothing, so two
// claims at once neither ask twice nor both show the notice.
func (check Check) Notice(ctx context.Context) []string {
	running, release := parse(check.Running)
	if !release || !check.manual() {
		return nil
	}

	folder, locked := check.lock()
	if !locked {
		return nil
	}
	defer folder.unlock()

	now := check.Now()
	today := now.Format(time.DateOnly)
	state := readCache(filepath.Join(check.Data, CacheName))

	if state.Shown == today {
		return nil
	}

	if now.Sub(state.Checked) >= CheckEvery {
		latest, _ := check.latest(ctx)
		state.Checked, state.Latest = now, latest
		folder.write(state)
	}

	latest, release := parse(state.Latest)
	if !release || !newer(latest, running) {
		return nil
	}

	state.Shown = today
	folder.write(state)

	return lines(strings.TrimPrefix(state.Latest, "v"), strings.TrimPrefix(check.Running, "v"))
}

func lines(latest string, running string) []string {
	return []string{
		fmt.Sprintf("agentic-helpers: release %s is out, this machine runs %s; tell the user in prose, offering both ways:", latest, running),
		"  update now: claude plugin update agentic-helpers@agentic-helpers (takes effect next session)",
		`  or turn auto-update on: merge {"extraKnownMarketplaces": {"agentic-helpers": {"source": {"source": "github", "repo": "fza/agentic-helpers"}, "autoUpdate": true}}} into ~/.claude/settings.json`,
		"  plugin option update_notice: false silences this notice",
	}
}

type lockedData struct {
	dir  *safefs.Dir
	file *os.File
}

func (check Check) lock() (lockedData, bool) {
	dir, err := safefs.EnsureRoot(check.Data)
	if err != nil {
		return lockedData{}, false
	}

	file, err := dir.Lock(LockName, 0o600)
	if err != nil {
		dir.Close()

		return lockedData{}, false
	}

	taken, err := filelock.TryExclusive(file)
	if err != nil || !taken {
		_ = file.Close()
		dir.Close()

		return lockedData{}, false
	}

	return lockedData{dir: dir, file: file}, true
}

func (folder lockedData) unlock() {
	_ = filelock.Unlock(folder.file)
	_ = folder.file.Close()
	folder.dir.Close()
}

func (folder lockedData) write(state cache) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}

	_ = folder.dir.Replace(CacheName, append(data, '\n'), 0o600)
}

func readCache(path string) cache {
	var state cache

	raw, err := os.ReadFile(path)
	if err != nil {
		return cache{}
	}

	err = json.Unmarshal(raw, &state)
	if err != nil {
		return cache{}
	}

	return state
}

func (check Check) latest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return "", fmt.Errorf("asking for the latest release: %w", err)
	}

	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "agentic-helpers/"+check.Running)

	response, err := check.Client.Do(request)
	if err != nil {
		return "", fmt.Errorf("asking for the latest release: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asking for the latest release: %w: status %d", ErrAnswer, response.StatusCode)
	}

	var answer struct {
		TagName string `json:"tag_name"`
	}

	err = json.NewDecoder(io.LimitReader(response.Body, maxAnswer)).Decode(&answer)
	if err != nil {
		return "", fmt.Errorf("reading the latest release: %w", err)
	}

	return answer.TagName, nil
}

type marketplace struct {
	Source struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"source"`
	AutoUpdate bool `json:"autoUpdate"`
}

// manual says auto-update is off for the marketplace and nothing pins it:
// only then does a newer release wait for the user. An entry whose source
// names a ref or a commit stays on what it names whatever is released. Any listed place turning
// auto-update on counts, as Claude Code reads managed, user and project
// settings, then `known_marketplaces.json`.
func (check Check) manual() bool {
	entries := []marketplace{}

	for _, path := range check.Settings {
		var settings struct {
			Extra map[string]marketplace `json:"extraKnownMarketplaces"`
		}

		if readJSON(path, &settings) {
			entry, found := settings.Extra[Marketplace]
			if found {
				entries = append(entries, entry)
			}
		}
	}

	var known map[string]marketplace

	if readJSON(check.Known, &known) {
		entry, found := known[Marketplace]
		if found {
			entries = append(entries, entry)
		}
	}

	for _, entry := range entries {
		if entry.AutoUpdate || entry.Source.Ref != "" || entry.Source.SHA != "" {
			return false
		}
	}

	return true
}

func readJSON(path string, target any) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	return json.Unmarshal(raw, target) == nil
}

type version [3]int

// parse reads a release version, `v` and three numbers; a `go install` build
// (`dev`) or a pre-release is none.
func parse(text string) (version, bool) {
	parts := strings.Split(strings.TrimPrefix(text, "v"), ".")
	if len(parts) != 3 {
		return version{}, false
	}

	var parsed version

	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || strings.Trim(part, "0123456789") != "" {
			return version{}, false
		}

		parsed[index] = number
	}

	return parsed, true
}

func newer(latest version, running version) bool {
	for index := range latest {
		if latest[index] != running[index] {
			return latest[index] > running[index]
		}
	}

	return false
}
