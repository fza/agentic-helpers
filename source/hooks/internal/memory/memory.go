// Package memory is agentic-memory: a session holding a seat files entries as
// it works, and the tool stamps, indexes and replays them. Nothing is ever
// rewritten: entries are retired or superseded, and the tool, not the agent,
// chooses what returns to a fresh context.
//
// The log is the truth, under `~/.claude/agentic-memory/<project>/`; vectors,
// heat and per-session state are a cache under `~/.cache/agentic-memory/`.
// Every agent-facing subcommand is also a tool of the `memory` MCP server.
package memory

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

const self = "agentic-memory"

var (
	ErrUsage   = errors.New("usage: " + self + " <subcommand>; " + self + " help lists them")
	ErrNoSeat  = errors.New("claim a seat first: " + self + " claim <seat>")
	ErrNoEntry = errors.New("no such entry")
)

type Git interface {
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

type EmbedderFactory func(config embed.Config) (embed.Embedder, error)

// Env is everything a run reads from outside the store. Base is the directory
// the session works in; Home and CacheRoot locate the store and its cache.
type Env struct {
	Base          string
	Home          string
	CacheRoot     string
	OwnerName     string
	SessionID     string
	CompactWindow int
	Settings      []string
	Getenv        func(string) string
	Now           func() time.Time
	Random        io.Reader
	Processes     seat.Processes
	Checkout      project.CommonDirLookup
	Git           Git
	Embedders     EmbedderFactory
	Version       string
	// WaitPoll and WaitDeadline pace the idle waiter; zero takes every 5
	// seconds for 30 minutes.
	WaitPoll     time.Duration
	WaitDeadline time.Duration
}

type memory struct {
	env     Env
	streams hookio.Streams
	key     string
	// outsideGit is set where the session works outside a git work tree and
	// the plugin keeps memory to git: nothing is read and nothing is created.
	outsideGit bool

	seatRegistry  *seat.Registry
	registryFound bool

	// unreadable says why the store is of a format newer than this release
	// reads: nothing is read or written then.
	unreadable string
}

func (mem *memory) root() string      { return seat.StoreRoot(mem.env.Home) }
func (mem *memory) store() string     { return projectkey.Dir(mem.root(), mem.key) }
func (mem *memory) logDir() string    { return filepath.Join(mem.store(), "log") }
func (mem *memory) seatsDir() string  { return seat.Dir(mem.store()) }
func (mem *memory) sensorDir() string { return filepath.Join(mem.root(), "sessions") }
func (mem *memory) cacheDir() string {
	return projectkey.Dir(filepath.Join(mem.env.CacheRoot, self), mem.key)
}

func (mem *memory) storeExists() bool {
	info, err := os.Stat(mem.store())

	return err == nil && info.IsDir()
}

// locate keys the store: the project's own key inside a git work tree, and
// outside one the directory's own key where the plugin allows memory there.
func (mem *memory) locate(ctx context.Context) {
	key, err := projectkey.Key(ctx, mem.env.Base, mem.env.Checkout)
	if err == nil && mem.insideWorkTree(ctx) {
		mem.key = key

		return
	}

	if !mem.options().MemoryRequireGit {
		mem.key = projectkey.DirectoryKey(mem.env.Base)

		return
	}

	mem.outsideGit = true
}

func (mem *memory) insideWorkTree(ctx context.Context) bool {
	answer, err := mem.env.Git.Output(ctx, mem.env.Base, "rev-parse", "--is-inside-work-tree")

	return err == nil && strings.TrimSpace(answer) == "true"
}

// storeRefusal is why no call may touch the store, or empty where one may.
func (mem *memory) storeRefusal() string {
	switch {
	case mem.outsideGit:
		return mem.outsideGitRefusal()
	case mem.unreadable != "":
		return mem.unreadable
	}

	return ""
}

// reportFailedMigration says once per session that the store stayed at its
// earlier format; every call then runs on it as it is.
func (mem *memory) reportFailedMigration(err error) {
	marker := filepath.Join(mem.cacheDir(), "migration-failed", cmp.Or(mem.env.SessionID, "no-session"))
	if _, statErr := os.Stat(marker); statErr == nil {
		return
	}

	_, _ = fmt.Fprintln(mem.streams.Err, self+": "+err.Error())
	_ = os.MkdirAll(filepath.Dir(marker), 0o700)
	_ = os.WriteFile(marker, nil, 0o600)
}

func (mem *memory) outsideGitRefusal() string {
	return fmt.Sprintf("%s is not inside a git work tree; memory works only in one", mem.env.Base)
}

// now is in UTC, so every time the store and its cache write reads the same
// whatever zone the machine runs in; printed stamps turn it local.
func (mem *memory) now() time.Time {
	return mem.env.Now().UTC()
}

func (mem *memory) say(text string) {
	if text != "" {
		_, _ = fmt.Fprintln(mem.streams.Out, strings.TrimRight(text, "\n"))
	}
}

func (mem *memory) refuse(err error) int {
	_, _ = fmt.Fprintln(mem.streams.Err, self+": "+err.Error())

	return 1
}

func (mem *memory) options() pluginconfig.Options {
	options, err := pluginconfig.Read(mem.env.Settings)
	if err != nil {
		return pluginconfig.Defaults()
	}

	return options
}

func (mem *memory) retiredKeep() time.Duration {
	return time.Duration(mem.options().RetiredBodyDays) * 24 * time.Hour
}

// ownSeat is the name of the seat the calling process holds, or empty.
func (mem *memory) ownSeat(ctx context.Context) (string, seat.Claim) {
	key, held := seat.Of(ctx, mem.seatsDir(), mem.env.Processes, mem.env.OwnerName)
	if key == "" {
		return "", held
	}

	return mem.seatName(key), held
}

func (mem *memory) openLog() (*memorylog.Log, error) {
	log, err := memorylog.Open(mem.logDir())
	if err != nil {
		return nil, fmt.Errorf("opening the log: %w", err)
	}

	return log, nil
}

// load folds the log as it stands. A store with no log yet folds empty.
func (mem *memory) load(ctx context.Context) (*state, error) {
	_, err := os.Stat(mem.logDir())
	if errors.Is(err, fs.ErrNotExist) {
		return &state{entries: map[string]*entry{}}, nil
	}

	log, err := mem.openLog()
	if err != nil {
		return nil, err
	}

	snapshot, err := log.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the log: %w", err)
	}

	defer func() { _ = snapshot.Close() }()

	folded, err := fold(snapshot, mem.seatName)
	if err != nil {
		return nil, err
	}

	mem.annotate(folded)

	return folded, nil
}

func (mem *memory) appendEvents(ctx context.Context, events ...memorylog.Event) error {
	log, err := mem.openLog()
	if err != nil {
		return err
	}

	stored := make([]memorylog.Event, len(events))
	for index, event := range events {
		stored[index] = event
		if event.Seat != "" {
			stored[index].Seat = cmp.Or(mem.seatKey(event.Seat), event.Seat)
		}
	}

	err = log.AppendWith(ctx, mergePolicy(mem.now(), mem.retiredKeep()), stored...)
	if err != nil {
		return fmt.Errorf("writing to the log: %w", err)
	}

	return nil
}

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func (mem *memory) newID(folded *state) (string, error) {
	length := mem.options().EntryIDLength

	for range 64 {
		id, err := drawID(mem.env.Random, length)
		if err != nil {
			return "", err
		}

		_, taken := folded.entries[id]
		if !taken {
			return id, nil
		}
	}

	return "", errors.New("no free entry id after 64 draws")
}

// drawID takes each character from a random byte below 252, the largest
// multiple of the alphabet's 36 a byte holds, so every character is equally
// likely; a byte above it is drawn again.
func drawID(random io.Reader, length int) (string, error) {
	unbiased := 256 / len(idAlphabet) * len(idAlphabet)
	id := []byte("m-")
	drawn := make([]byte, 1)

	for len(id) < len("m-")+length {
		_, err := io.ReadFull(random, drawn)
		if err != nil {
			return "", fmt.Errorf("drawing an entry id: %w", err)
		}

		if int(drawn[0]) >= unbiased {
			continue
		}

		id = append(id, idAlphabet[int(drawn[0])%len(idAlphabet)])
	}

	return string(id), nil
}

// Main runs one subcommand. A hook event in a project without a store stays
// silent and reads nothing.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if env.Random == nil {
		env.Random = rand.Reader
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	mem := &memory{env: env, streams: streams}
	mem.locate(ctx)

	// who reads every store on the machine and changes none, so it neither
	// needs a git work tree nor migrates the caller's store.
	if args[0] == "who" {
		mem.say(mem.who(ctx, request{}).text)

		return 0
	}

	err := mem.prepare(ctx)
	if errors.Is(err, ErrNewerStore) {
		mem.unreadable = strings.TrimPrefix(err.Error(), ErrNewerStore.Error()+": ")
	} else if err != nil {
		mem.reportFailedMigration(err)
	}

	name, rest := args[0], args[1:]

	hookEvent, isHook := hookEvents[name]
	if isHook {
		if mem.outsideGit || mem.unreadable != "" {
			return 0
		}

		return hookEvent(mem, ctx, rest)
	}

	if name == "mcp" {
		return mem.serveMCP(ctx)
	}

	refusal := mem.storeRefusal()
	if refusal != "" && name != "help" && name != "--help" && name != "-h" {
		_, _ = fmt.Fprintln(streams.Err, self+": "+refusal)

		return 1
	}

	if name == "dump" {
		return mem.dump(ctx, rest)
	}

	if name == "help" || name == "--help" || name == "-h" {
		mem.say(helpText())

		return 0
	}

	command, found := commandByName(name)
	if !found {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	request, err := command.parse(rest, streams.In)
	if err != nil {
		return mem.refuse(err)
	}

	request.normalize()

	result := command.run(mem, ctx, request)
	if result.failed {
		_, _ = fmt.Fprintln(streams.Err, self+": "+result.text)

		return 1
	}

	mem.say(result.text)

	return result.code
}

func (mem *memory) dump(ctx context.Context, args []string) int {
	if len(args) != 1 {
		return mem.refuse(errors.New("usage: " + self + " dump <segment>|--tail"))
	}

	path := args[0]
	if path == "--tail" {
		path = filepath.Join(mem.logDir(), "tail.jsonl")
	} else if !strings.Contains(path, string(filepath.Separator)) {
		path = filepath.Join(mem.logDir(), path)
	}

	err := memorylog.Dump(ctx, mem.streams.Out, path)
	if err != nil {
		return mem.refuse(err)
	}

	return 0
}
