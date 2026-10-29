// Package memory is agentic-memory: a session holding a seat files entries as
// it works, and the tool stamps, indexes and replays them. Nothing is ever
// rewritten: entries are retired or superseded, and the tool, not the agent,
// chooses what returns to a fresh context.
//
// The log is the truth, in the `memory` folder of the project's store in the
// agent-neutral home (internal/home); vectors, heat and per-session state are
// a cache in the store's folder under the home's cache root.
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
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
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
// the session works in; Roots locate the stores and their caches; CacheRoot is
// the machine's cache directory, where sdd keeps its own.
type Env struct {
	Base          string
	Home          string
	Roots         home.Roots
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
	// Tmp is the machine's shared temporary directory, the one place outside
	// the work tree `read --to` may write; empty takes /tmp.
	Tmp string
	// Graph asks sdd for the graph's statuses, which link flags read; Sdd says
	// whether the sdd on the PATH has `export` at all, nil taking it as given.
	Graph GraphExporter
	Sdd   *sddcaps.Probe
	// WaitPoll and WaitDeadline pace the idle waiter; zero takes every 5
	// seconds for 30 minutes.
	WaitPoll     time.Duration
	WaitDeadline time.Duration
}

// agent names the agent whose sessions memory reads, under
// `<data>/sessions/<agent>/`.
const agent = "claude"

type memory struct {
	env     Env
	streams hookio.Streams
	// key is the id of the store the checkout works on.
	key string
	// facts is the checkout as the home matches it; unbound says no store
	// matched it yet, so key names a store nothing holds until a write
	// creates it; question is the user's call standing on it.
	facts    home.Checkout
	unbound  bool
	question *home.Question
	// seen is what the calling session's context already holds of each entry,
	// for the read running now; nil where no seated session calls.
	seen *presented
	// outsideGit is set where the session works outside a git work tree and
	// the plugin keeps memory to git: nothing is read and nothing is created.
	outsideGit bool

	seatRegistry  *seat.Registry
	registryFound bool
	shelfRegistry *seat.Shelves
	shelvesFound  bool

	// unreadable says why the store is of a format newer than this release
	// reads: nothing is read or written then.
	unreadable string

	// graphKnown is the graph's statuses once a read asked for them; graphWarned
	// is set once a read said they are unavailable.
	graphKnown  map[string]graphState
	graphWarned bool

	// graphConfig is read once per process: every link, end condition and
	// language read asks for it. here is the caller's checkout, asked once per
	// call, since a long-running MCP server outlives a commit or a branch
	// switch.
	graphConfig *resolvedGraph
	here        *tree
	hashCache   *hashCache
}

// mainRoot is the main checkout; Main remembers the lookup, so a process asks
// git once.
func (mem *memory) mainRoot(ctx context.Context) string {
	return project.Root(ctx, mem.env.Base, mem.env.Checkout)
}

// resolvedGraph is what memory reads of the graph the main checkout resolves
// to: where its entries are, its repo_id and its language.
type resolvedGraph struct {
	entries  string
	repoID   string
	language string
}

// graphProject is the graph the main checkout resolves to, read once per
// process.
func (mem *memory) graphProject(ctx context.Context) resolvedGraph {
	if mem.graphConfig == nil {
		resolved := graphconfig.ResolveAt(mem.mainRoot(ctx))
		mem.graphConfig = &resolvedGraph{
			entries: resolved.Entries, repoID: resolved.RepoID(), language: resolved.Language(),
		}
	}

	return *mem.graphConfig
}

func (mem *memory) store() string     { return mem.env.Roots.Store(mem.key, home.Memory) }
func (mem *memory) logDir() string    { return filepath.Join(mem.store(), "log") }
func (mem *memory) seatsDir() string  { return seat.Dir(mem.store()) }
func (mem *memory) sensorDir() string { return mem.env.Roots.Sessions(agent) }
func (mem *memory) cacheDir() string  { return mem.env.Roots.StoreCache(mem.key, home.Memory) }

// sibling is the memory of another store on the machine.
func (mem *memory) sibling(id string) *memory {
	return &memory{env: mem.env, streams: mem.streams, key: id}
}

// resolver draws store ids from crypto/rand, never from env.Random: an id
// needs no reproducing, and a draw there would shift every entry id after it.
func (mem *memory) resolver() home.Resolver {
	return home.Resolver{Roots: mem.env.Roots, Now: mem.env.Now}
}

func (mem *memory) storeExists() bool {
	info, err := os.Stat(mem.store())

	return err == nil && info.IsDir()
}

// locate finds the checkout's store: inside a git work tree by the checkout,
// outside one by the directory's own path where the plugin allows memory
// there. Nothing is created: a checkout matching no store works on an id
// nothing holds until a write binds it.
func (mem *memory) locate(ctx context.Context) {
	if mem.insideWorkTree(ctx) {
		checkout, err := home.Inspect(ctx, mem.env.Base, mem.env.Checkout, mem.env.Git)
		if err == nil {
			mem.facts = checkout
			mem.resolve(ctx, false)

			return
		}
	}

	if !mem.options().MemoryRequireGit {
		mem.facts = home.InspectDirectory(mem.env.Base)
		mem.resolve(ctx, false)

		return
	}

	mem.outsideGit = true
}

// resolve maps the checkout to its store, creating one where create is set
// and none matches.
func (mem *memory) resolve(ctx context.Context, create bool) {
	find := mem.resolver().Find
	if create {
		find = mem.resolver().Ensure
	}

	resolution, err := find(ctx, mem.facts)
	if err != nil {
		mem.unreadable = err.Error()

		return
	}

	mem.key, mem.unbound, mem.question = resolution.Store, resolution.New, resolution.Question
}

func (mem *memory) insideWorkTree(ctx context.Context) bool {
	return mem.checkout(ctx).workTree
}

// storeRefusal is why no call may touch the store, or empty where one may.
func (mem *memory) storeRefusal() string {
	switch {
	case mem.outsideGit:
		return mem.outsideGitRefusal()
	case mem.unreadable != "":
		return mem.unreadable
	case mem.key == "" && mem.question != nil:
		return mem.question.Text(self)
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
	options, err := pluginconfig.ReadWith(mem.env.Settings, mem.env.Getenv)
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
		return newState(), nil
	}

	log, err := mem.openLog()
	if err != nil {
		return nil, err
	}

	folded, err := mem.foldLog(ctx, log)
	if err != nil {
		return nil, err
	}

	mem.annotate(folded)
	mem.annotateLinks(ctx, folded)

	return folded, nil
}

// appendEvents writes events to the log, all or none, after scanning every
// text they carry for a credential: the one door every write passes, so a
// field added later is scanned without anyone remembering to.
func (mem *memory) appendEvents(ctx context.Context, events ...memorylog.Event) error {
	err := credentialInEvents(ctx, events)
	if err != nil {
		return err
	}

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

	if env.Checkout != nil {
		env.Checkout = &project.Remembered{Lookup: env.Checkout}
	}

	if len(args) == 0 {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	mem := &memory{env: env, streams: streams}
	defer func() { mem.hashCache.save() }()

	settles := settlesMapping(args[0], args[1:])

	if !pieces.Memory.On(mem.options()) && !settles {
		return mem.off(ctx, args[0])
	}

	mem.locate(ctx)

	// who answers anywhere: outside a git work tree it lists the other projects
	// only, and it migrates no store.
	if args[0] == "who" {
		held, _ := commandByName("who")

		req, err := held.parse(args[1:], nil)
		if err != nil {
			return mem.refuse(err)
		}

		mem.say(mem.who(ctx, req).text)

		return 0
	}

	name := args[0]
	_, isHook := hookEvents[name]

	if !isHook && !settles {
		mem.admission(ctx, name)
	}

	err := mem.prepare(ctx)
	if errors.Is(err, ErrNewerStore) {
		mem.unreadable = strings.TrimPrefix(err.Error(), ErrNewerStore.Error()+": ")
	} else if err != nil {
		mem.reportFailedMigration(err)
	}

	rest := args[1:]

	hookEvent := hookEvents[name]
	if isHook {
		if mem.outsideGit || mem.unreadable != "" {
			return 0
		}

		return hookEvent(mem, ctx, rest)
	}

	if name == "mcp" {
		return mem.serveMCP(ctx)
	}

	// A question standing refuses every call but those answering anywhere.
	asking := mem.key == "" && mem.question != nil
	refusal := mem.storeRefusal()

	if refusal != "" && !settles && !(asking && slices.Contains(readOnly, name)) && name != "help" && name != "--help" &&
		name != "-h" {
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

// off answers a call while memory is off on the machine: a hook says nothing,
// the MCP server lists no tools and says which option turns it on, a command
// refuses.
func (mem *memory) off(ctx context.Context, name string) int {
	_, isHook := hookEvents[name]

	switch {
	case isHook:
		return 0
	case name == "mcp":
		err := mcpserver.ServeNone(ctx, mem.streams.In, mem.streams.Out, "memory", mem.env.Version,
			pieces.Memory.Instructions())
		if err != nil {
			return 1
		}

		return 0
	}

	_, _ = fmt.Fprintln(mem.streams.Err, pieces.Memory.Off())

	return 1
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
