package migration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/migration"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// oldMachine is a home as the releases before the agent-neutral home left
// it: memory stores, tool libraries and writers lists under `.claude`, caches
// under the cache root, and the checkouts they name.
type oldMachine struct {
	t      *testing.T
	root   string
	env    migration.Env
	starts *test.Starts
	now    time.Time
}

func newOldMachine(t *testing.T) *oldMachine {
	t.Helper()

	root := test.OldHome(t)
	userHome := filepath.Join(root, "home")

	m := &oldMachine{t: t, root: root, starts: &test.Starts{}, now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}
	m.env = migration.Env{
		UserHome:   userHome,
		Roots:      test.Roots(root),
		Lookup:     test.GitDirOf{},
		Processes:  test.Executables{},
		Starter:    m.starts,
		Executable: filepath.Join(root, "bin", "agentic-memory"),
		SessionID:  test.Session,
		Now:        func() time.Time { return m.now },
		MountRoots: []string{filepath.Join(root, "Volumes")},
	}

	return m
}

func (m *oldMachine) claude(parts ...string) string {
	return filepath.Join(append([]string{m.env.UserHome, ".claude"}, parts...)...)
}

func (m *oldMachine) checkout(name string) string { return filepath.Join(m.root, "checkouts", name) }

func (m *oldMachine) prepare() string {
	m.t.Helper()

	var stderr bytes.Buffer

	migration.Prepare(context.Background(), m.env, &stderr)

	return stderr.String()
}

// storeOf is the store, ordinary or unclaimed, holding the legacy key.
func (m *oldMachine) storeOf(key string) (home.Identity, bool) {
	m.t.Helper()

	for _, root := range []string{m.env.Roots.Stores(), filepath.Join(m.env.Roots.Data, "unclaimed")} {
		listed, _ := os.ReadDir(root)

		for _, entry := range listed {
			var identity home.Identity

			err := json.Unmarshal([]byte(test.ReadFile(m.t, filepath.Join(root, entry.Name(), "identity.json"))), &identity)
			if err != nil {
				m.t.Fatalf("reading the identity of %s: %v", entry.Name(), err)
			}

			if slices.Contains(identity.LegacyKeys, key) {
				return identity, true
			}
		}
	}

	return home.Identity{}, false
}

func (m *oldMachine) stores() int {
	listed, _ := os.ReadDir(m.env.Roots.Stores())
	unclaimed, _ := os.ReadDir(filepath.Join(m.env.Roots.Data, "unclaimed"))

	return len(listed) + len(unclaimed)
}

func (m *oldMachine) pointer(checkout string) string {
	data, err := os.ReadFile(filepath.Join(checkout, ".git", home.PointerName))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

func (m *oldMachine) find(dir string) home.Resolution {
	m.t.Helper()

	checkout, err := home.Inspect(context.Background(), dir, test.GitDirOf{}, nil)
	if err != nil {
		m.t.Fatalf("inspecting %s: %v", dir, err)
	}

	resolution, err := home.Resolver{Roots: m.env.Roots}.Find(context.Background(), checkout)
	if err != nil {
		m.t.Fatalf("finding the store of %s: %v", dir, err)
	}

	return resolution
}

// ordinary and unclaimed are the stores the fixture's keys become.
const (
	ordinaryStores  = 3
	unclaimedStores = 4
)

func TestOneCallMovesEveryOldProjectIntoTheHome(t *testing.T) {
	m := newOldMachine(t)
	roots := m.env.Roots

	leftover := m.claude("agentic-memory", "github.com", "acme", ".app.20261028T000000.000000000Z")
	test.WriteFile(t, filepath.Join(leftover, "format.json"), `{"format":5}`+"\n")

	output := m.prepare()
	if output != "" {
		t.Errorf("a migration should ask and say nothing, got %q", output)
	}

	app, found := m.storeOf("github.com/acme/app")
	if !found || app.Unclaimed != nil || !slices.Contains(app.Paths(), m.checkout("app")) {
		t.Fatalf("a key whose checkout exists should become an ordinary store admitting it, got %+v", app)
	}

	if app.Checkouts[0].Volume != "" && app.Checkouts[0].Inode == 0 {
		t.Error("an existing checkout should be admitted with its volume and inode")
	}

	if m.pointer(m.checkout("app")) != app.ID {
		t.Error("an existing checkout should carry the pointer to its store")
	}

	for _, moved := range []string{
		filepath.Join(roots.Store(app.ID, home.Memory), "seats", "main.json"),
		filepath.Join(roots.Store(app.ID, home.Memory), "backup", "format-1-20261020T100000.000000000Z", "seats.json"),
		filepath.Join(roots.Store(app.ID, home.Tools), "word-count", "current"),
	} {
		if !test.Exists(t, moved) {
			t.Errorf("a key's memory, seats, backups and tools should move into one store, missing %s", moved)
		}
	}

	plain, found := m.storeOf("local/0123456789ab")
	if !found || plain.Unclaimed != nil || m.pointer(m.checkout("plain")) != plain.ID ||
		!test.Exists(t, filepath.Join(roots.Store(plain.ID, home.Tools), "moved-sentences", "current")) {
		t.Errorf("a local key listed by its library's own writers list should become an ordinary store, got %+v", plain)
	}

	offline, found := m.storeOf("github.com/acme/offline")
	offlinePath := filepath.Join(m.root, "Volumes", "External", "offline")

	if !found || offline.Unclaimed != nil || !slices.Contains(offline.Paths(), offlinePath) ||
		!slices.Contains(offline.RepoIDs, "github.com/acme/offline") {
		t.Errorf("a key whose checkout lies on a volume not mounted should become an ordinary store, got %+v", offline)
	}

	if test.Exists(t, filepath.Join(m.root, "Volumes", "External")) {
		t.Error("a volume not mounted should not be created to hold a pointer")
	}

	gone, found := m.storeOf("github.com/acme/gone")
	if !found || gone.Unclaimed == nil || !gone.Unclaimed.Equal(m.now) ||
		!test.Exists(t, filepath.Join(roots.UnclaimedStore(gone.ID, home.Tools), "index.json")) ||
		!test.Exists(t, filepath.Join(roots.UnclaimedStore(gone.ID, home.Memory), "format.json")) {
		t.Errorf("a key whose checkout vanished should become an unclaimed store stamped with the migration, got %+v", gone)
	}

	if len(gone.Checkouts) != 1 || gone.Checkouts[0].CommonDir != filepath.Join(m.checkout("gone"), ".git") {
		t.Errorf("an unclaimed store should know the clean checkouts its writers list named, got %+v", gone.Checkouts)
	}

	for _, key := range []string{"aaa.example/acme/app", "local/ffffffffffff"} {
		other, found := m.storeOf(key)
		if !found || other.Unclaimed == nil {
			t.Errorf("a key whose checkout belongs to another key should be left unclaimed, got %+v", other)
		}
	}

	if _, found := m.storeOf("github.com/acme/empty"); found {
		t.Error("a key holding nothing but a writers list should become no store")
	}

	directory, found := m.storeOf("local/fedcba987654")
	if !found || directory.Unclaimed == nil {
		t.Errorf("a key with no writers list should become an unclaimed store, got %+v", directory)
	}

	if m.stores() != ordinaryStores+unclaimedStores {
		t.Errorf("every key should become exactly one store, got %d", m.stores())
	}

	if !test.Exists(t, filepath.Join(roots.Sessions("claude"), test.Session, "context.json")) ||
		!test.Exists(t, filepath.Join(roots.Shared("attachments"), "ab", "abcdef0123456789")) {
		t.Error("the session tree and the attachments should move into the home")
	}

	backups, _ := filepath.Glob(filepath.Join(roots.Data, "backup", "legacy-*"))
	if len(backups) != 1 {
		t.Fatalf("one legacy backup should hold the old metadata, got %v", backups)
	}

	for _, saved := range []string{
		"agentic-helpers/github.com/acme/app/writers.json",
		"agentic-tools/local/0123456789ab/writers.json",
		"agentic-memory/local/0123456789ab/seats.json",
		"agentic-memory/local/0123456789ab/shelves.json",
		"agentic-memory/github.com/acme/gone/format.json",
	} {
		if !test.Exists(t, filepath.Join(backups[0], saved)) {
			t.Errorf("every writers list, registry, shelf list and format should be backed up, missing %s", saved)
		}
	}

	if test.Exists(t, filepath.Join(backups[0], "agentic-memory", "github.com", "acme", "app", "log", "tail.jsonl")) {
		t.Error("bulk data should not be copied into the backup")
	}

	if test.Exists(t, filepath.Join(backups[0], "agentic-memory", "github.com", "acme", filepath.Base(leftover))) ||
		!test.Exists(t, leftover) {
		t.Error("a hidden folder left beside the old stores should be neither backed up nor moved")
	}

	if test.Exists(t, filepath.Join(roots.Data, ".migration.journal")) {
		t.Error("a completed migration should remove its journal")
	}

	formatInfo, err := os.Stat(roots.FormatFile())
	if err != nil {
		t.Fatalf("a completed migration should write the home's format: %v", err)
	}

	_ = filepath.WalkDir(roots.Data, func(path string, entry os.DirEntry, err error) error {
		info, infoErr := entry.Info()
		if err == nil && infoErr == nil && !entry.IsDir() && info.ModTime().After(formatInfo.ModTime()) {
			t.Errorf("the home's format should be written last, %s is newer", path)
		}

		return nil
	})

	listed, _ := os.ReadDir(m.claude())

	var names []string
	for _, entry := range listed {
		names = append(names, entry.Name())
	}

	if !slices.Equal(names, []string{"agentic-helpers", "agentic-memory", "agentic-tools"}) {
		t.Errorf("nothing set aside should stay beside the old trees, got %v", names)
	}

	for _, key := range []string{"github.com/acme/app", "github.com/acme/gone", "local/fedcba987654"} {
		stub := m.claude("agentic-memory", filepath.FromSlash(key))

		entries, _ := os.ReadDir(stub)
		if len(entries) != 1 || test.ReadFile(t, filepath.Join(stub, "format.json")) != `{"format":999}`+"\n" {
			t.Errorf("each old memory store should be left as a stub holding only its format, got %v in %s", entries, key)
		}
	}

	for _, fenced := range []string{"agentic-tools", "agentic-helpers"} {
		if !strings.HasPrefix(test.ReadFile(t, m.claude(fenced)), migration.FencePrefix+roots.Data) {
			t.Errorf("the old %s tree should be replaced by a file naming the home", fenced)
		}
	}

	if !test.Exists(t, filepath.Join(m.root, "cache", "agentic-memory")) {
		t.Error("the old caches should stay until the sweep")
	}
}

func TestMovedProjectsResolveSilentlyOnTheirFirstVisit(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	app, _ := m.storeOf("github.com/acme/app")
	if got := m.find(m.checkout("app")); got.Store != app.ID || got.Question != nil {
		t.Errorf("a migrated checkout should resolve to its store without a question, got %+v", got)
	}

	offlinePath := filepath.Join(m.root, "Volumes", "External", "offline")
	test.WriteFile(t, filepath.Join(offlinePath, ".git", "HEAD"), "ref: refs/heads/main\n")

	offline, _ := m.storeOf("github.com/acme/offline")
	if got := m.find(offlinePath); got.Store != offline.ID || got.Question != nil || m.pointer(offlinePath) != offline.ID {
		t.Errorf("a checkout back on its mounted volume should resolve silently and get its pointer, got %+v", got)
	}

	gone, _ := m.storeOf("github.com/acme/gone")
	test.WriteFile(t, filepath.Join(m.checkout("gone"), ".git", "HEAD"), "ref: refs/heads/main\n")
	test.WriteFile(t, filepath.Join(m.checkout("gone"), ".sdd", "config.yaml"), "repo_id: github.com/acme/gone\n")

	if got := m.find(m.checkout("gone")); got.Store != gone.ID || got.Question != nil {
		t.Errorf("a checkout of an unclaimed store's legacy key should get it silently, got %+v", got)
	}
}

func TestAFailedMigrationLeavesTheOldHomeAsItWas(t *testing.T) {
	cases := []struct {
		name string
		// fault places what makes the run fail and answers what clears it.
		fault func(m *oldMachine) string
	}{
		{name: "the backup cannot be written", fault: func(m *oldMachine) string {
			test.WriteFile(m.t, filepath.Join(m.env.Roots.Data, "backup"), "taken")

			return filepath.Join(m.env.Roots.Data, "backup")
		}},
		{name: "the session tree has no place", fault: func(m *oldMachine) string {
			test.WriteFile(m.t, filepath.Join(m.env.Roots.Sessions("claude"), "other", "context.json"), "taken")

			return filepath.Join(m.env.Roots.Data, "sessions")
		}},
		{name: "the attachments have no place", fault: func(m *oldMachine) string {
			test.WriteFile(m.t, filepath.Join(m.env.Roots.Shared("attachments"), "blocker"), "taken")

			return m.env.Roots.Shared("attachments")
		}},
		{name: "a file stands where the tool library was", fault: func(m *oldMachine) string {
			err := os.Rename(m.claude("agentic-tools"), filepath.Join(m.root, "tools-elsewhere"))
			if err != nil {
				m.t.Fatalf("moving the library away: %v", err)
			}

			test.WriteFile(m.t, m.claude("agentic-tools"), "someone else's file\n")

			return m.claude("agentic-tools")
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newOldMachine(t)
			cleared := tc.fault(m)

			before := test.Snapshot(t, m.claude())

			output := m.prepare()

			if test.Snapshot(t, m.claude()) != before {
				t.Error("a failed migration should leave the old home byte-identical")
			}

			if m.pointer(m.checkout("app")) != "" || m.pointer(m.checkout("plain")) != "" {
				t.Error("a failed migration should leave no pointer")
			}

			if test.Exists(t, m.env.Roots.FormatFile()) || m.stores() != 0 ||
				test.Exists(t, filepath.Join(m.env.Roots.Data, ".migration.journal")) {
				t.Error("a failed migration should leave no store, no format and no journal once undone")
			}

			if !test.Exists(t, filepath.Join(m.env.Roots.Data, ".migration.lock")) || !test.Exists(t, cleared) {
				t.Error("a failed migration should keep the lock and everything that stood before it")
			}

			prefix := "agentic-helpers: migrating to " + m.env.Roots.Data + " failed: "
			suffix := "; nothing moved, and the next call tries again\n"

			if !strings.HasPrefix(output, prefix) || !strings.HasSuffix(output, suffix) || strings.Count(output, "\n") != 1 {
				t.Errorf("a failed migration should say so in one line, got %q", output)
			}

			if again := m.prepare(); again != "" {
				t.Errorf("the failure should be said once per session, got %q", again)
			}

			err := os.RemoveAll(cleared)
			if err != nil {
				t.Fatalf("clearing the fault: %v", err)
			}

			if again := m.prepare(); again != "" || !test.Exists(t, m.env.Roots.FormatFile()) {
				t.Errorf("the next call should migrate, got %q", again)
			}
		})
	}
}

func TestTwoCallsOnOneUnmigratedHomeMigrateItOnce(t *testing.T) {
	m := newOldMachine(t)

	err := os.MkdirAll(m.env.Roots.Data, 0o700)
	if err != nil {
		t.Fatalf("creating the data root: %v", err)
	}

	holder, err := os.OpenFile(filepath.Join(m.env.Roots.Data, ".migration.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the migration lock: %v", err)
	}

	defer func() { _ = holder.Close() }()

	err = filelock.Exclusive(holder)
	if err != nil {
		t.Fatalf("holding the migration lock: %v", err)
	}

	var group sync.WaitGroup

	migrated := make(chan bool, 2)
	failures := make(chan error, 2)

	for range 2 {
		group.Go(func() {
			done, runErr := migration.Run(context.Background(), m.env)
			if runErr != nil {
				failures <- runErr
			}

			migrated <- done
		})
	}

	time.Sleep(100 * time.Millisecond)

	if len(migrated) != 0 {
		t.Error("a call should wait while the lock is held")
	}

	_ = filelock.Unlock(holder)

	group.Wait()
	close(migrated)
	close(failures)

	for runErr := range failures {
		t.Errorf("neither call should fail, got %v", runErr)
	}

	count := 0

	for done := range migrated {
		if done {
			count++
		}
	}

	if count != 1 || m.stores() != ordinaryStores+unclaimedStores {
		t.Errorf("one call should migrate and the other wait for it, got %d migrating and %d stores", count, m.stores())
	}
}

func TestACallFindingNothingOldDoesNothing(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *oldMachine)
	}{
		{name: "no old tree", setup: func(m *oldMachine) {
			_ = os.RemoveAll(m.claude())
		}},
		{name: "a home already migrated", setup: func(m *oldMachine) {
			test.WriteFile(m.t, m.env.Roots.FormatFile(), `{"format":1}`+"\n")
		}},
		{name: "no home folder known", setup: func(m *oldMachine) {
			m.t.Chdir(m.env.UserHome)
			m.env.UserHome = ""
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newOldMachine(t)
			tc.setup(m)

			done, err := migration.Run(context.Background(), m.env)
			if done || err != nil || m.stores() != 0 {
				t.Errorf("a call with nothing to migrate should move nothing, got %v %v", done, err)
			}
		})
	}
}

func TestAHomeWhoseDataRootWentMissingMigratesNothingTwice(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	err := os.RemoveAll(m.env.Roots.Data)
	if err != nil {
		t.Fatalf("removing the data root: %v", err)
	}

	before := test.Snapshot(t, m.claude())

	if output := m.prepare(); output != "" || !test.Exists(t, m.env.Roots.FormatFile()) {
		t.Errorf("a home left with stubs and fences only should complete silently, got %q", output)
	}

	if m.stores() != 0 || test.Snapshot(t, m.claude()) != before {
		t.Error("stubs and fences should never be moved as data")
	}
}

// newestReleasedStoreFormat is the newest memory store format any release
// reads: every release before the home refuses a store of a newer one as a
// newer store, and its hooks stay silent on it.
const newestReleasedStoreFormat = 5

func TestAMigratedHomeKeepsAnEarlierReleaseFromWriting(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	var stub struct {
		Format int `json:"format"`
	}

	err := json.Unmarshal([]byte(test.ReadFile(t, m.claude("agentic-memory", "github.com", "acme", "app", "format.json"))), &stub)
	if err != nil || stub.Format <= newestReleasedStoreFormat {
		t.Errorf("a stub should declare a format no released version reads, got %d %v", stub.Format, err)
	}

	// An earlier release opens its writers lists and its tool library below
	// these roots, refusing a root that is a link or no folder, and creates a
	// project's folder there before it claims one.
	for _, fenced := range []string{"agentic-helpers", "agentic-tools"} {
		info, err := os.Lstat(m.claude(fenced))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("the old %s root should be a plain file, got %v %v", fenced, info, err)
		}

		err = os.MkdirAll(m.claude(fenced, "github.com", "acme", "new"), 0o700)
		if err == nil {
			t.Errorf("no project folder should be creatable below the old %s root", fenced)
		}
	}
}

func TestTheSweepWaitsForEveryOlderRelease(t *testing.T) {
	cases := []struct {
		name    string
		running test.Executables
		swept   bool
	}{
		{name: "only this release runs", swept: true, running: test.Executables{Paths: []string{"@BIN@/agentic-memory"}}},
		{name: "nothing runs", swept: true},
		{name: "an older release runs", running: test.Executables{Paths: []string{"/plugins/data/2.15.0/agentic-tools"}}},
		{name: "the table cannot be read", running: test.Executables{Err: os.ErrPermission}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newOldMachine(t)
			m.prepare()

			for index, path := range tc.running.Paths {
				tc.running.Paths[index] = strings.ReplaceAll(path, "@BIN@", filepath.Dir(m.env.Executable))
			}

			m.env.Processes = tc.running

			test.WriteFile(t, filepath.Join(m.env.Roots.Cache, "state.json"), "{}")

			swept, err := migration.Sweep(context.Background(), m.env)
			if err != nil || swept != tc.swept {
				t.Fatalf("the sweep should run only once no older release runs, got %v %v", swept, err)
			}

			for _, left := range []string{
				filepath.Join(m.root, "cache", "agentic-memory"), filepath.Join(m.root, "cache", "agentic-tools"),
				m.claude("agentic-tools"), m.claude("agentic-helpers"), m.claude("agentic-memory"),
			} {
				if test.Exists(t, left) == tc.swept {
					t.Errorf("caches, fences and stubs should be gone exactly when the sweep ran, %s", left)
				}
			}

			if !test.Exists(t, filepath.Join(m.env.Roots.Cache, "state.json")) {
				t.Error("the sweep should leave the home's own cache alone")
			}

			if migration.Owed(m.env) == tc.swept {
				t.Error("a sweep should be owed exactly while it has not run")
			}
		})
	}
}

func TestTheSweepLeavesWhatItDidNotPlace(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	late := m.claude("agentic-memory", "github.com", "acme", "app", "late.json")
	test.WriteFile(t, late, "{}")

	reading := m.claude("agentic-memory", "sessions", test.OtherSession+".ctx")
	test.WriteFile(t, reading, "{}")

	foreign := m.claude("agentic-tools")
	test.WriteFile(t, foreign, "someone else's file\n")

	swept, err := migration.Sweep(context.Background(), m.env)
	if err != nil || !swept {
		t.Fatalf("the sweep should run, got %v %v", swept, err)
	}

	if !test.Exists(t, late) || test.Exists(t, m.claude("agentic-memory", "local")) || !test.Exists(t, foreign) {
		t.Error("the sweep should remove every stub, fence and empty folder, and nothing else")
	}

	if test.Exists(t, reading) {
		t.Error("the sweep should remove the context readings older sessions kept writing")
	}
}

func TestASweepIsOwedWhileAnyStubRemains(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	for _, done := range []string{
		filepath.Join(m.root, "cache", "agentic-memory"), filepath.Join(m.root, "cache", "agentic-tools"),
		m.claude("agentic-tools"), m.claude("agentic-helpers"),
	} {
		err := os.RemoveAll(done)
		if err != nil {
			t.Fatalf("removing %s: %v", done, err)
		}
	}

	if !migration.Owed(m.env) {
		t.Error("a sweep should be owed while the old memory root still holds stubs")
	}
}

func TestTheSweepWaitsForTheMigrationAndForAHeldLock(t *testing.T) {
	m := newOldMachine(t)

	swept, err := migration.Sweep(context.Background(), m.env)
	if err != nil || swept || migration.Owed(m.env) {
		t.Errorf("nothing should be swept before the migration ran, got %v %v", swept, err)
	}

	m.prepare()

	lock, err := os.OpenFile(filepath.Join(m.env.Roots.Data, ".migration.lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the migration lock: %v", err)
	}

	defer func() { _ = lock.Close() }()

	err = filelock.Shared(lock)
	if err != nil {
		t.Fatalf("holding the migration lock: %v", err)
	}

	swept, err = migration.Sweep(context.Background(), m.env)
	if err != nil || swept || !test.Exists(t, m.claude("agentic-tools")) {
		t.Errorf("a sweep should leave its work to a later one while the lock is held, got %v %v", swept, err)
	}
}

func TestASessionEndStartsTheSweepOnlyWhileOneIsOwed(t *testing.T) {
	m := newOldMachine(t)

	err := migration.StartSweep(context.Background(), m.env)
	if err != nil || len(m.starts.Started()) != 0 {
		t.Errorf("no sweep should start before the migration, got %v %v", m.starts.Started(), err)
	}

	m.prepare()

	err = migration.StartSweep(context.Background(), m.env)

	want := []string{filepath.Join(filepath.Dir(m.env.Executable), "agentic-helpers"), "sweep"}
	if err != nil || len(m.starts.Started()) != 1 || !slices.Equal(m.starts.Started()[0], want) {
		t.Errorf("an owed sweep should start as this release's agentic-helpers sweep, got %v %v", m.starts.Started(), err)
	}
}

func TestAnIdleServerSweepsSilently(t *testing.T) {
	m := newOldMachine(t)
	m.prepare()

	idle := migration.Idle(m.env)
	if idle.After < time.Minute {
		t.Error("a server should sweep only once its client has been quiet a while")
	}

	idle.Run(context.Background())

	if migration.Owed(m.env) || test.Exists(t, m.claude("agentic-memory")) {
		t.Error("an idle server's sweep should remove what the migration left")
	}
}
