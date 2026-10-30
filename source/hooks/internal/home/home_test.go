package home_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// machine is a test's home and the checkouts on it, each below one root.
type machine struct {
	t         *testing.T
	root      string
	roots     home.Roots
	worktrees map[string]string
	remotes   map[string]string
	now       time.Time
}

func newMachine(t *testing.T) *machine {
	t.Helper()

	root := t.TempDir()

	return &machine{
		t: t, root: root, roots: test.Roots(root), worktrees: map[string]string{}, remotes: map[string]string{},
		now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC),
	}
}

func (m *machine) resolver() home.Resolver {
	return home.Resolver{Roots: m.roots, Now: func() time.Time { return m.now }}
}

// checkout makes a git checkout at name declaring repoID, none where empty,
// with origin as its remote, none where empty.
func (m *machine) checkout(name string, repoID string, origin string) string {
	m.t.Helper()

	dir := filepath.Join(m.root, name)
	test.WriteFile(m.t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")

	if repoID != "" {
		test.WriteFile(m.t, filepath.Join(dir, ".sdd", "config.yaml"), "repo_id: "+repoID+"\n")
	}

	if origin != "" {
		m.remotes[dir] = "remote.origin.url " + origin + "\n"
	}

	return dir
}

func (m *machine) inspect(dir string) home.Checkout {
	m.t.Helper()

	git := &remoteGit{remotes: m.remotes}

	checkout, err := home.Inspect(context.Background(), dir, test.GitDirOf{Worktrees: m.worktrees}, git)
	if err != nil {
		m.t.Fatalf("inspecting %s: %v", dir, err)
	}

	return checkout
}

func (m *machine) find(dir string) home.Resolution {
	m.t.Helper()

	resolution, err := m.resolver().Find(context.Background(), m.inspect(dir))
	if err != nil {
		m.t.Fatalf("finding the store of %s: %v", dir, err)
	}

	return resolution
}

func (m *machine) ensure(dir string) home.Resolution {
	m.t.Helper()

	resolution, err := m.resolver().Ensure(context.Background(), m.inspect(dir))
	if err != nil {
		m.t.Fatalf("ensuring the store of %s: %v", dir, err)
	}

	return resolution
}

// bound is the store a checkout resolves to with no question, bound first.
func (m *machine) bound(dir string) string {
	m.t.Helper()

	resolution := m.ensure(dir)
	if resolution.Question != nil || resolution.Store == "" || resolution.New {
		m.t.Fatalf("%s should resolve to a store silently, got %+v", dir, resolution)
	}

	return resolution.Store
}

// remoteGit answers the one question the home asks git: every remote's URL,
// per checkout.
type remoteGit struct {
	remotes map[string]string
}

func (git *remoteGit) Output(_ context.Context, dir string, args ...string) (string, error) {
	if strings.Join(args, " ") != `config --get-regexp ^remote\..*\.url$` || git.remotes[dir] == "" {
		return "", errors.New("exit status 1")
	}

	return git.remotes[dir], nil
}

func TestLocate(t *testing.T) {
	cases := []struct {
		name      string
		variables map[string]string
		data      string
		cache     string
	}{
		{name: "nothing set", data: "/home/dev/.local/share/agentic-helpers", cache: "/home/dev/.cache/agentic-helpers"},
		{
			name: "XDG roots set", variables: map[string]string{"XDG_DATA_HOME": "/xdg/data", "XDG_CACHE_HOME": "/xdg/cache"},
			data: "/xdg/data/agentic-helpers", cache: "/xdg/cache/agentic-helpers",
		},
		{
			name:      "AGENTIC_HELPERS_HOME wins over XDG_DATA_HOME",
			variables: map[string]string{"AGENTIC_HELPERS_HOME": "/srv/agents", "XDG_DATA_HOME": "/xdg/data"},
			data:      "/srv/agents", cache: "/home/dev/.cache/agentic-helpers",
		},
		{
			name:      "relative values are ignored",
			variables: map[string]string{"AGENTIC_HELPERS_HOME": "agents", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache"},
			data:      "/home/dev/.local/share/agentic-helpers", cache: "/home/dev/.cache/agentic-helpers",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roots := home.Locate("/home/dev", func(key string) string { return tc.variables[key] })
			if roots.Data != tc.data || roots.Cache != tc.cache {
				t.Errorf("the roots should follow the variables in their precedence, got %+v", roots)
			}
		})
	}
}

func TestNormalRemoteKeepsNoCredential(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "https with a token", raw: "https://x-access-token:ghp_secret123@github.com/acme/app.git", want: "github.com/acme/app"},
		{name: "https with a user only", raw: "https://felix@GitHub.com/acme/app", want: "github.com/acme/app"},
		{name: "a query carrying a token", raw: "https://host.example/acme/app.git?private_token=glpat-secret#frag", want: "host.example/acme/app"},
		{name: "ssh URL with a port", raw: "ssh://git@host.example:2222/acme/app.git", want: "host.example/acme/app"},
		{name: "scp form", raw: "git@github.com:acme/app.git", want: "github.com/acme/app"},
		{name: "scp form with a password", raw: "user:hunter2@github.com:acme/app.git", want: "github.com/acme/app"},
		{name: "scp form with a query", raw: "git@github.com:acme/app.git?token=abc", want: "github.com/acme/app"},
		{name: "scp form with a fragment", raw: "git@github.com:acme/app.git#main", want: "github.com/acme/app"},
		{name: "a local path", raw: "/srv/git/app.git", want: "/srv/git/app.git"},
		{name: "a file URL", raw: "file:///srv/git/app.git", want: "/srv/git/app.git"},
		{name: "nothing", raw: "  ", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := home.NormalRemote(tc.raw); got != tc.want {
				t.Errorf("a remote should read as host and path alone, got %q", got)
			}
		})
	}
}

func TestGrace(t *testing.T) {
	cases := []struct {
		text  string
		want  time.Duration
		valid bool
	}{
		{text: "", want: 30 * 24 * time.Hour, valid: true},
		{text: "0", want: 0, valid: true},
		{text: "90m", want: 90 * time.Minute, valid: true},
		{text: "12h", want: 12 * time.Hour, valid: true},
		{text: "7d", want: 7 * 24 * time.Hour, valid: true},
		{text: "soon", want: 30 * 24 * time.Hour, valid: false},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got, valid := home.Grace(tc.text)
			if got != tc.want || valid != tc.valid {
				t.Errorf("a grace should read as a span, the default where empty or unreadable, got %v %t", got, valid)
			}
		})
	}
}

func TestTheGraceDefaultIsTheOneTheOptionListingShows(t *testing.T) {
	if home.DefaultGrace != pluginconfig.Defaults().OrphanStoreGrace {
		t.Error("the grace a store waits should be the built-in value `agentic-helpers config` shows")
	}
}

func TestAStoreIDIsAUUIDv7AndASuffix(t *testing.T) {
	m := newMachine(t)
	id := m.bound(m.checkout("app", "github.com/acme/app", ""))

	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}-app$`).MatchString(id) {
		t.Errorf("a store id should be a UUID v7 and the repo_id's last part, got %s", id)
	}

	for _, invalid := range []string{"", "../x", "0193f5c2-0000-7000-8000-000000000000/..", "not-an-id"} {
		if home.ValidID(invalid) {
			t.Errorf("%q should be no store id", invalid)
		}
	}
}

func TestAFirstCheckoutGetsAStoreSilently(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "github.com/acme/app", "")

	found := m.find(app)
	if !found.New || found.Question != nil || test.Exists(t, m.roots.Data) {
		t.Fatalf("a checkout matching nothing should be found unbound, creating nothing, got %+v", found)
	}

	id := m.bound(app)

	pointer := strings.TrimSpace(test.ReadFile(t, filepath.Join(app, ".git", home.PointerName)))
	if pointer != id {
		t.Errorf("binding should write the store id into the git common dir, got %q", pointer)
	}

	identity, err := m.roots.Identity(id)
	if err != nil || len(identity.Checkouts) != 1 || identity.Checkouts[0].CommonDir != filepath.Join(app, ".git") ||
		identity.Name() != "github.com/acme/app" {
		t.Errorf("the identity should admit the checkout and carry its repo_id, got %+v: %v", identity, err)
	}

	if !test.Exists(t, filepath.Join(m.roots.Data, "format.json")) || !test.Exists(t, filepath.Join(m.roots.Data, "index.json")) {
		t.Error("binding should write the home's format and index")
	}

	if again := m.find(app); again.Store != id || again.New || again.Question != nil {
		t.Errorf("the checkout should resolve to its store again, got %+v", again)
	}

	other := m.checkout("lib", "github.com/acme/lib", "")
	if found := m.find(other); !found.New || found.Question != nil {
		t.Errorf("a second checkout matching nothing should be found unbound, got %+v", found)
	}

	if listed, _ := os.ReadDir(m.roots.Stores()); len(listed) != 1 {
		t.Error("finding a checkout in a home holding stores should create no store")
	}
}

func TestAWorktreeAndAMovedCheckoutKeepTheStore(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "github.com/acme/app", "")
	id := m.bound(app)

	worktree := filepath.Join(m.root, "app-feature")
	test.WriteFile(t, filepath.Join(worktree, "README.md"), "# app\n")
	m.worktrees[worktree] = app

	if got := m.find(worktree); got.Store != id || got.Question != nil {
		t.Errorf("a worktree should resolve to its main checkout's store through the pointer, got %+v", got)
	}

	moved := filepath.Join(m.root, "elsewhere", "app")

	err := os.MkdirAll(filepath.Dir(moved), 0o755)
	if err == nil {
		err = os.Rename(app, moved)
	}

	if err != nil {
		t.Fatalf("moving the checkout: %v", err)
	}

	if got := m.find(moved); got.Store != id || got.Question != nil {
		t.Errorf("a moved checkout should keep its store, got %+v", got)
	}

	identity, _ := m.roots.Identity(id)
	if len(identity.Checkouts) != 1 || identity.Checkouts[0].Path != moved {
		t.Errorf("the store should learn the moved path in place of the old one, got %+v", identity.Checkouts)
	}
}

func TestACopiedCheckoutIsAskedAbout(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "", "")
	id := m.bound(app)

	copied := m.checkout("copy", "", "")
	test.WriteFile(t, filepath.Join(copied, ".git", home.PointerName), id+"\n")

	got := m.ensure(copied)
	if got.Question == nil || got.Question.Reason != home.ReasonCopied || got.Store != "" {
		t.Errorf("a checkout carrying another checkout's pointer should be asked about, got %+v", got)
	}
}

func TestAFreshCloneOfAKnownOriginAsksOnceThenResolvesSilently(t *testing.T) {
	m := newMachine(t)
	first := m.checkout("app", "", "git@github.com:acme/app.git")
	id := m.bound(first)

	second := m.checkout("clone", "", "https://github.com/acme/app")

	asked := m.ensure(second)
	if asked.Question == nil || asked.Question.Reason != home.ReasonKnown || asked.Store != "" ||
		len(asked.Question.Stores) != 1 || asked.Question.Stores[0].ID != id {
		t.Fatalf("a second clone of a known origin should be asked about, naming the store, got %+v", asked)
	}

	text := asked.Question.Text("agentic-memory")
	for _, want := range []string{
		"no store is bound to the checkout " + second + " yet: it is another checkout of a repository a store already holds.",
		"Ask the user which store it belongs to, then run `agentic-memory bind <store>` or `agentic-memory bind --new` from this checkout.",
		"  " + id + ": github.com/acme/app; checkouts " + first,
		"  remotes of this checkout: github.com/acme/app",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the question should say %q, got:\n%s", want, text)
		}
	}

	_, err := m.resolver().Bind(context.Background(), m.inspect(second), id)
	if err != nil {
		t.Fatalf("binding the clone: %v", err)
	}

	if got := m.find(second); got.Store != id || got.Question != nil {
		t.Errorf("once bound, the clone should resolve silently, got %+v", got)
	}

	third := m.checkout("third", "", "https://github.com/acme/app")

	bound, err := m.resolver().BindNew(context.Background(), m.inspect(third))
	if err != nil || bound.Store.ID == id {
		t.Fatalf("binding a clone to a new store should create one: %v", err)
	}

	if len(bound.Moved) != 1 || bound.Moved[0].From.ID != id || !strings.Contains(strings.Join(bound.Moved[0].Identifiers, " "), "origin github.com/acme/app") {
		t.Errorf("binding should move the origin the clone shares, saying from where, got %+v", bound.Moved)
	}

	if got := m.find(third); got.Store != bound.Store.ID || got.Question != nil {
		t.Errorf("a clone bound to a new store should resolve to it silently, got %+v", got)
	}

	err = os.Remove(filepath.Join(first, ".git", home.PointerName))
	if err != nil {
		t.Fatalf("removing the first checkout's pointer: %v", err)
	}

	if got := m.ensure(first); got.Store != id || got.Question != nil {
		t.Errorf("the first checkout should keep its store, got %+v", got)
	}

	if held, _ := m.roots.Identity(id); len(held.Origins) != 0 {
		t.Errorf("a store matched by path should learn no identifier another store holds, got %+v", held.Origins)
	}

	if held, _ := m.roots.Identity(bound.Store.ID); len(held.Origins) != 1 {
		t.Errorf("the store holding the origin should keep it, got %+v", held.Origins)
	}
}

func TestAReusedPathAsksOnlyWhereItsIdentityContradicts(t *testing.T) {
	cases := []struct {
		name   string
		repoID string
		asks   bool
	}{
		{name: "another repo_id", repoID: "github.com/acme/other", asks: true},
		{name: "the same repo_id", repoID: "github.com/acme/app", asks: false},
		{name: "no repo_id", repoID: "", asks: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			app := m.checkout("app", "github.com/acme/app", "")
			id := m.bound(app)

			err := os.RemoveAll(app)
			if err != nil {
				t.Fatalf("removing the checkout: %v", err)
			}

			m.checkout("app", tc.repoID, "")

			got := m.ensure(app)
			if (got.Question != nil && got.Question.Reason == home.ReasonReused) != tc.asks {
				t.Fatalf("a reused path should ask exactly where its repo_id contradicts the store, got %+v", got)
			}

			if !tc.asks && got.Store != id {
				t.Errorf("a reused path agreeing with its store should bind to it silently, got %+v", got)
			}
		})
	}
}

// A removed directory's inode goes to the next one created, as linux does, so
// an inode match is checked against the store's identity as a path match is.
func TestARecycledInodeAsksOnlyWhereItsIdentityContradicts(t *testing.T) {
	cases := []struct {
		name   string
		repoID string
		asks   bool
	}{
		{name: "another repo_id", repoID: "github.com/acme/other", asks: true},
		{name: "the same repo_id", repoID: "github.com/acme/app", asks: false},
		{name: "no repo_id", repoID: "", asks: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)

			app := m.inspect(m.checkout("app", "github.com/acme/app", ""))
			app.Volume, app.Inode = "test-volume", 4242

			bound, err := m.resolver().Ensure(context.Background(), app)
			if err != nil || bound.Question != nil || bound.Store == "" {
				t.Fatalf("the first checkout should get a store silently, got %+v, %v", bound, err)
			}

			recycled := m.inspect(m.checkout("next", tc.repoID, ""))
			recycled.Volume, recycled.Inode = app.Volume, app.Inode

			got, err := m.resolver().Find(context.Background(), recycled)
			if err != nil {
				t.Fatalf("finding the store: %v", err)
			}

			if (got.Question != nil && got.Question.Reason == home.ReasonReused) != tc.asks {
				t.Fatalf("an inode match should ask exactly where its repo_id contradicts the store, got %+v", got)
			}

			if !tc.asks && got.Store != bound.Store {
				t.Errorf("an inode match agreeing with its store should keep it, got %+v", got)
			}
		})
	}
}

func TestAnUnclaimedStoreIsPromotedByItsLegacyKey(t *testing.T) {
	m := newMachine(t)

	id, err := m.resolver().Unclaim(context.Background(), "github.com/acme/app", []string{"/gone/app/.git"}, m.now)
	if err != nil {
		t.Fatalf("leaving an unclaimed store: %v", err)
	}

	test.WriteFile(t, filepath.Join(m.roots.UnclaimedStore(id, home.Memory), "format.json"), `{"format": 5}`)

	app := m.checkout("app", "github.com/acme/app", "")

	got := m.find(app)
	if got.Store != id || got.Question != nil || got.New {
		t.Fatalf("a checkout whose legacy key names an unclaimed store should get it, silently, got %+v", got)
	}

	if !test.Exists(t, filepath.Join(m.roots.Store(id, home.Memory), "format.json")) {
		t.Error("the promoted store's data should move under stores/")
	}

	identity, _ := m.roots.Identity(id)
	if identity.Unclaimed != nil {
		t.Error("a promoted store should be unclaimed no longer")
	}
}

func TestAnUnclaimedStoreIsDeletedOnceItsGraceHasPassed(t *testing.T) {
	m := newMachine(t)
	gone, _ := m.resolver().Unclaim(context.Background(), "local/0123456789ab", []string{"/gone/app/.git"}, m.now)

	living := m.checkout("mounted", "", "")
	waiting, _ := m.resolver().Unclaim(context.Background(), "local/ba9876543210", []string{filepath.Join(living, ".git")}, m.now)

	removed, err := m.resolver().SweepUnclaimed(context.Background(), 30*24*time.Hour)
	if err != nil || len(removed) != 0 {
		t.Fatalf("an unclaimed store within its grace should stay, got %v: %v", removed, err)
	}

	m.now = m.now.Add(31 * 24 * time.Hour)

	removed, err = m.resolver().SweepUnclaimed(context.Background(), 30*24*time.Hour)
	if err != nil || len(removed) != 1 || removed[0] != gone {
		t.Errorf("an unclaimed store past its grace should be deleted, got %v: %v", removed, err)
	}

	if !test.Exists(t, m.roots.UnclaimedStore(waiting, "")) {
		t.Error("an unclaimed store whose checkout exists again should never be deleted")
	}
}

func TestASplitLayoutJoinsTheGraphRepositorysStoreOnceItAdmitsTheCheckout(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{name: "the fork's project_dir", config: "project_dir: ../app-sdd\n"},
		{name: "stock sdd's graph_dir outside the checkout", config: "graph_dir: ../app-sdd/.sdd/graph\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			graph := m.checkout("app-sdd", "github.com/acme/app-sdd", "")
			test.WriteFile(t, filepath.Join(graph, ".sdd", "graph", ".keep"), "")
			id := m.bound(graph)

			code := m.checkout("app", "", "")
			test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), tc.config)

			asked := m.ensure(code)
			if asked.Store != "" || asked.Question == nil || asked.Question.Reason != home.ReasonSplit ||
				!asked.Question.Offers(id) {
				t.Fatalf("a graph store not admitting the code repository should be asked about, got %+v", asked)
			}

			text := asked.Question.Text("agentic-memory")
			if !strings.HasPrefix(text, "no store is bound to the checkout "+code+" yet: its `.sdd/` names the graph "+
				"repository "+graph+", whose store does not admit it. Ask the user which store it belongs to, then run "+
				"`agentic-memory bind <store>` or `agentic-memory bind --new` from this checkout.\n  "+id+": ") {
				t.Errorf("the question should name the checkout, the graph folder, the store and both commands, got:\n%s", text)
			}

			if test.Exists(t, filepath.Join(code, ".git", home.PointerName)) {
				t.Error("an unanswered question should write no pointer")
			}

			identity, err := m.roots.Identity(id)
			if err != nil || len(identity.Checkouts) != 1 {
				t.Errorf("an unanswered question should leave the graph store admitting the graph repository alone, got %+v: %v",
					identity.Checkouts, err)
			}

			_, err = m.resolver().Bind(context.Background(), m.inspect(code), id)
			if err != nil {
				t.Fatalf("binding to the graph repository's store: %v", err)
			}

			got := m.find(code)
			if got.Store != id || got.Question != nil {
				t.Fatalf("after the bind the code repository should join the graph repository's store silently, got %+v", got)
			}

			for _, dir := range []string{graph, code} {
				if pointer := strings.TrimSpace(test.ReadFile(t, filepath.Join(dir, ".git", home.PointerName))); pointer != id {
					t.Errorf("the pointer in %s should name the graph repository's store, got %q", dir, pointer)
				}
			}
		})
	}
}

// TestASplitLayoutJoinsSilentlyWhereTheGraphStoreAdmitsTheCheckout reaches the
// join itself, past the lookup that finds an admitted checkout first: the
// code repository's pointer is gone and the index knows nothing of it.
func TestASplitLayoutJoinsSilentlyWhereTheGraphStoreAdmitsTheCheckout(t *testing.T) {
	cases := []struct {
		name string
		// moved is where the code repository is moved after the bind, empty
		// where it stays: the store then knows it by volume and inode alone.
		moved string
		// recreated gives the code repository a new git directory at the
		// same path: the store then knows it by path alone.
		recreated bool
	}{
		{name: "admitted by path", recreated: true},
		{name: "admitted by volume and inode", moved: "app-moved"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			graph := m.checkout("app-sdd", "github.com/acme/app-sdd", "")
			id := m.bound(graph)

			code := m.checkout("app", "", "")
			test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), "project_dir: ../app-sdd\n")

			_, err := m.resolver().Bind(context.Background(), m.inspect(code), id)
			if err != nil {
				t.Fatalf("binding to the graph repository's store: %v", err)
			}

			if tc.moved != "" {
				moved := filepath.Join(m.root, tc.moved)

				err = os.Rename(code, moved)
				if err != nil {
					t.Fatalf("moving the code repository: %v", err)
				}

				code = moved
			}

			if m.inspect(code).Volume == "" && tc.moved != "" {
				t.Skip("no volume id on this platform")
			}

			err = os.Remove(filepath.Join(code, ".git", home.PointerName))
			if err != nil {
				t.Fatalf("removing the pointer: %v", err)
			}

			if tc.recreated {
				err = os.Rename(filepath.Join(code, ".git"), filepath.Join(code, ".git-old"))
				if err != nil {
					t.Fatalf("setting the git directory aside: %v", err)
				}

				test.WriteFile(t, filepath.Join(code, ".git", "HEAD"), "ref: refs/heads/main\n")
			}

			test.WriteFile(t, filepath.Join(m.roots.Data, "index.json"), `{"format": 1, "identifiers": {}, "unclaimed": {}}`)

			if got := m.find(code); got.Store != id || got.Question != nil {
				t.Errorf("a checkout the graph store admits should join it silently, got %+v", got)
			}
		})
	}
}

func TestASplitLayoutOnTwoStoresAsksKeepOrMerge(t *testing.T) {
	m := newMachine(t)
	code := m.checkout("app", "", "")
	own := m.bound(code)

	graph := m.checkout("app-sdd", "github.com/acme/app-sdd", "")
	graphStore := m.bound(graph)

	test.WriteFile(t, filepath.Join(code, ".sdd", "config.yaml"), "project_dir: ../app-sdd\n")

	got := m.find(code)
	if got.Store != own || got.Question == nil || got.Question.Reason != home.ReasonSplit {
		t.Fatalf("split sides on two stores should ask, the code side working on its own store meanwhile, got %+v", got)
	}

	if text := got.Question.Text("agentic-memory"); !strings.Contains(text, "`agentic-memory bind "+own+"`") ||
		!strings.Contains(text, "`agentic-memory merge "+own+" "+graphStore+"`") {
		t.Errorf("the question should offer keep and merge, got:\n%s", text)
	}

	_, err := m.resolver().Bind(context.Background(), m.inspect(code), own)
	if err != nil {
		t.Fatalf("keeping the stores apart: %v", err)
	}

	if again := m.find(code); again.Store != own || again.Question != nil {
		t.Errorf("stores kept apart should resolve silently, got %+v", again)
	}
}

func TestMergeJoinsTwoStores(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "github.com/acme/app", "")
	into := m.bound(app)
	other := m.checkout("app-old", "github.com/acme/app-old", "")
	from := m.bound(other)

	test.WriteFile(t, filepath.Join(m.roots.Store(from, home.Memory), "log", "tail.jsonl"), "from\n")
	test.WriteFile(t, filepath.Join(m.roots.Store(into, home.Memory), "log", "tail.jsonl"), "into\n")
	test.WriteFile(t, filepath.Join(m.roots.Store(from, home.Tools), "count", "current"), "1\n")

	joined := map[string]string{}
	joiners := map[string]home.Joiner{home.Memory: func(_ context.Context, _ string, source string, target string) error {
		joined[source] = target

		return nil
	}}

	identity, err := m.resolver().Merge(context.Background(), from, into, joiners)
	if err != nil {
		t.Fatalf("merging: %v", err)
	}

	if joined[m.roots.Store(from, home.Memory)] != m.roots.Store(into, home.Memory) {
		t.Error("a component both stores hold should go through its joiner")
	}

	if !test.Exists(t, filepath.Join(m.roots.Store(into, home.Tools), "count", "current")) {
		t.Error("a component only the joined store holds should move over")
	}

	if len(identity.Checkouts) != 2 || !strings.Contains(strings.Join(identity.Merged, " "), from) {
		t.Errorf("the joined identity should hold both checkouts and the joined store's id, got %+v", identity)
	}

	if test.Exists(t, filepath.Join(m.roots.Stores(), from)) {
		t.Error("the joined store should be gone")
	}

	if got := m.find(other); got.Store != into || got.Question != nil {
		t.Errorf("the joined store's checkout should resolve to the surviving store, got %+v", got)
	}

	stale := m.inspect(app)
	stale.Pointer = from

	if got, _ := m.resolver().Find(context.Background(), stale); got.Store != into {
		t.Errorf("a pointer still naming the joined store should resolve to the surviving one, got %+v", got)
	}

	_, err = m.resolver().Merge(context.Background(), into, into, joiners)
	if !errors.Is(err, home.ErrSameStore) {
		t.Errorf("merging a store into itself should be refused, got %v", err)
	}
}

func TestMergeRefusesAComponentItCannotJoin(t *testing.T) {
	m := newMachine(t)
	into := m.bound(m.checkout("app", "github.com/acme/app", ""))
	from := m.bound(m.checkout("old", "github.com/acme/old", ""))

	test.WriteFile(t, filepath.Join(m.roots.Store(from, "notes"), "a"), "a")
	test.WriteFile(t, filepath.Join(m.roots.Store(into, "notes"), "b"), "b")

	_, err := m.resolver().Merge(context.Background(), from, into, nil)
	if !errors.Is(err, home.ErrNoJoiner) || !test.Exists(t, filepath.Join(m.roots.Stores(), from)) {
		t.Errorf("a component both hold with no joiner should refuse the merge before anything moves, got %v", err)
	}
}

func TestANewerHomeIsRefused(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "", "")
	test.WriteFile(t, filepath.Join(m.roots.Data, "format.json"), `{"format": 2}`)

	_, findErr := m.resolver().Find(context.Background(), m.inspect(app))
	_, ensureErr := m.resolver().Ensure(context.Background(), m.inspect(app))
	_, bindErr := m.resolver().BindNew(context.Background(), m.inspect(app))

	for _, err := range []error{findErr, ensureErr, bindErr} {
		if !errors.Is(err, home.ErrNewerHome) {
			t.Errorf("a home of a newer format should be refused, got %v", err)
		}
	}

	if test.Exists(t, filepath.Join(app, ".git", home.PointerName)) || test.Exists(t, m.roots.Stores()) {
		t.Error("a refused home should get nothing written")
	}
}

func TestADeletedIndexIsRebuiltWithTheSameMapping(t *testing.T) {
	m := newMachine(t)
	m.bound(m.checkout("app", "github.com/acme/app", "git@github.com:acme/app.git"))
	m.bound(m.checkout("tool", "github.com/acme/tool", ""))
	_, _ = m.resolver().Unclaim(context.Background(), "local/0123456789ab", []string{"/gone/.git"}, m.now)

	path := filepath.Join(m.roots.Data, "index.json")
	before := test.ReadFile(t, path)

	err := os.Remove(path)
	if err != nil {
		t.Fatalf("deleting the index: %v", err)
	}

	m.find(filepath.Join(m.root, "app"))

	if after := test.ReadFile(t, path); after != before {
		t.Errorf("a deleted index should be rebuilt with the same mapping, got:\n%s\nbefore:\n%s", after, before)
	}
}

func TestNoCredentialLandsInAnIdentity(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "", "https://x-access-token:ghp_0123456789abcdefghijklmnopqrstuvwxyz@github.com/acme/app.git")
	m.remotes[app] += "remote.mirror.url https://deploy:hunter2@mirror.example/acme/app.git?token=abc\n"
	id := m.bound(app)

	identity := test.ReadFile(t, filepath.Join(m.roots.Stores(), id, "identity.json"))
	for _, secret := range []string{"ghp_", "x-access-token", "hunter2", "deploy", "token=abc"} {
		if strings.Contains(identity, secret) {
			t.Errorf("identity.json should hold no part of a remote's credential, found %q in:\n%s", secret, identity)
		}
	}

	if !strings.Contains(identity, `"github.com/acme/app"`) {
		t.Errorf("identity.json should hold the origin as host and path, got:\n%s", identity)
	}
}

func TestAnIdentifierBelongsToOneStore(t *testing.T) {
	m := newMachine(t)
	first := m.bound(m.checkout("app", "github.com/acme/app", ""))
	second := m.checkout("fork", "github.com/acme/app", "")

	bound, err := m.resolver().BindNew(context.Background(), m.inspect(second))
	if err != nil {
		t.Fatalf("binding the second checkout to a new store: %v", err)
	}

	held, _ := m.roots.Identity(first)
	if len(held.RepoIDs) != 0 || len(bound.Store.RepoIDs) != 1 {
		t.Errorf("binding an identifier another store holds should move it, got %v and %v", held.RepoIDs, bound.Store.RepoIDs)
	}
}

func TestStoresLeftByAMigrationResolveWithoutAQuestion(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "github.com/acme/app", "")
	worktreeOfApp := m.checkout("app-second", "github.com/acme/app", "")

	adopted, err := m.resolver().Adopt(context.Background(), []home.Checkout{m.inspect(app), m.inspect(worktreeOfApp)},
		[]string{"github.com/acme/app"})
	if err != nil {
		t.Fatalf("adopting a store for existing checkouts: %v", err)
	}

	unclaimed, err := m.resolver().Unclaim(context.Background(), "github.com/acme/tool", []string{"/gone/tool/.git"}, m.now)
	if err != nil {
		t.Fatalf("leaving an unclaimed store: %v", err)
	}

	if test.Exists(t, m.roots.FormatFile()) {
		t.Error("adopting and unclaiming should leave the home's format to the migration, which writes it last")
	}

	err = m.roots.WriteFormat()
	if err != nil || !test.Exists(t, m.roots.FormatFile()) {
		t.Errorf("writing the home's format should place format.json, got %v", err)
	}

	for _, dir := range []string{app, worktreeOfApp} {
		if pointer := strings.TrimSpace(test.ReadFile(t, filepath.Join(dir, ".git", home.PointerName))); pointer != adopted {
			t.Errorf("adopting should write the pointer into every checkout, got %q in %s", pointer, dir)
		}

		if got := m.find(dir); got.Store != adopted || got.Question != nil {
			t.Errorf("an adopted checkout should resolve silently, got %+v", got)
		}
	}

	tool := m.checkout("tool", "github.com/acme/tool", "")
	if got := m.find(tool); got.Store != unclaimed || got.Question != nil {
		t.Errorf("the checkout an unclaimed store waits for should get it silently, got %+v", got)
	}

	clone := m.checkout("app-clone", "github.com/acme/app", "")

	asked := m.find(clone)
	if asked.Question == nil || asked.Question.Checkout != clone {
		t.Errorf("a question should only ever be about the checkout resolved, got %+v", asked.Question)
	}
}

func TestACheckoutCopiedElsewhereWhoseOriginalIsGoneKeepsItsStore(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "", "")
	id := m.bound(app)

	copied := m.checkout("copy", "", "")
	test.WriteFile(t, filepath.Join(copied, ".git", home.PointerName), id+"\n")

	err := os.RemoveAll(app)
	if err != nil {
		t.Fatalf("removing the original: %v", err)
	}

	if got := m.find(copied); got.Store != id || got.Question != nil {
		t.Errorf("a checkout moved by a copy, its original gone, should keep its store, got %+v", got)
	}
}

func TestAPointerNamingNoStoreIsPassedOver(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "", "")
	id := m.bound(app)

	outside := filepath.Join(m.root, "planted")
	test.WriteFile(t, filepath.Join(outside, "identity.json"), `{"id": "../planted"}`)

	for _, pointer := range []string{"../../planted", "0193f5c2-0000-7000-8000-000000000000"} {
		test.WriteFile(t, filepath.Join(app, ".git", home.PointerName), pointer+"\n")

		if got := m.find(app); got.Store != id || got.Question != nil {
			t.Errorf("a pointer naming no store, %q, should be passed over for the path, got %+v", pointer, got)
		}
	}
}

func TestAnIdentityNamingAnotherFolderIsIgnored(t *testing.T) {
	m := newMachine(t)
	app := m.checkout("app", "github.com/acme/app", "")
	id := m.bound(app)

	other := m.checkout("other", "github.com/acme/other", "")
	otherID := m.bound(other)

	identity := test.ReadFile(t, filepath.Join(m.roots.Stores(), otherID, "identity.json"))
	test.WriteFile(t, filepath.Join(m.roots.Stores(), otherID, "identity.json"), strings.ReplaceAll(identity, otherID, id))

	if _, err := m.roots.Identity(otherID); err == nil {
		t.Error("an identity naming another store than its folder should not be read")
	}
}

func TestAStaleIndexNeverSplitsAStore(t *testing.T) {
	cases := []struct {
		name  string
		index func(id string) string
	}{
		{name: "an index missing the store", index: func(string) string {
			return `{"format": 1, "identifiers": {}, "unclaimed": {}}`
		}},
		{name: "an index naming the path for a store that does not hold it", index: func(string) string {
			return `{"format": 1, "identifiers": {"path:PATH": ["0193f5c2-0000-7000-8000-000000000000-x"]}, "unclaimed": {}}`
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			app := m.checkout("app", "", "")
			id := m.bound(app)

			err := os.Remove(filepath.Join(app, ".git", home.PointerName))
			if err != nil {
				t.Fatalf("removing the pointer: %v", err)
			}

			stale := strings.ReplaceAll(tc.index(id), "PATH", filepath.Join(app, ".git"))
			test.WriteFile(t, filepath.Join(m.roots.Data, "index.json"), stale)

			if got := m.ensure(app); got.Store != id || got.Question != nil {
				t.Errorf("a stale index should be rebuilt rather than split the checkout from its store, got %+v", got)
			}
		})
	}
}
