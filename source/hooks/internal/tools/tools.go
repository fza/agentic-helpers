// Package tools is agentic-tools: a library of the small helper tools agents
// write, kept per project under `~/.claude/agentic-tools/<project>/` and read
// across every project on the machine, the own project preferred. Writes touch
// the caller's own store only.
//
// Every value in every store is untrusted: a sandboxed process can write a
// store wherever its sandbox allows `~/.claude`, and a foreign store holds
// another project's agents' text. Readers follow no link, wait on no pipe and
// cap every read; every listing opens with a framing lead; and the lines the
// hooks push into a session carry tool names, paths and commands only.
//
// Every verb is also a tool of the plugin's `tools` MCP server.
package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/projectkey"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

const self = "agentic-tools"

// framing opens every listing and take: what follows is other agents' text.
const framing = "Tools other agents registered (their own descriptions):"

var (
	ErrUsage          = errors.New("usage: " + self + " add|update|find|list|take|merge|reindex|mcp|prompt|notice|stop|end")
	ErrNoCaller       = errors.New("no session found for this call: " + self + " runs inside a Claude Code session")
	ErrPromptWithheld = errors.New("the prompt looks like it holds a credential; it is never embedded")
)

type Git interface {
	Output(ctx context.Context, dir string, args ...string) (string, error)
}

type EmbedderFactory func(config embed.Config) (embed.Embedder, error)

// Env is everything a run reads from outside the stores. Base is the directory
// the session works in.
type Env struct {
	Base      string
	Home      string
	CacheRoot string
	OwnerName string
	SessionID string
	Settings  []string
	Getenv    func(string) string
	Now       func() time.Time
	Processes seat.Processes
	Checkout  project.CommonDirLookup
	Git       Git
	Embedders EmbedderFactory
	Version   string
	// LockWait bounds how long a write waits for the store lock; zero takes
	// two seconds.
	LockWait time.Duration
}

type library struct {
	env     Env
	streams hookio.Streams
	key     string
	// outsideGit is set where the session works outside a git work tree and
	// the plugin keeps the library to git: nothing is read or written.
	outsideGit bool
	session    string
	options    pluginconfig.Options
	// reach is every store a read may use, the own one first, once listed.
	reach []*store
}

func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	lib := &library{env: env, streams: streams, session: env.SessionID}
	lib.options = lib.readOptions()

	verb, rest := args[0], args[1:]

	if !pieces.Tools.On(lib.options) {
		return off(ctx, pieces.Tools, "tools", env.Version, verb, hooks[verb] != nil, streams)
	}

	hook, isHook := hooks[verb]
	if isHook {
		lib.locate(ctx)

		return hook(lib, ctx)
	}

	if verb == "mcp" {
		lib.locate(ctx)

		return lib.serveMCP(ctx)
	}

	held, known := commandNamed(verb)
	if !known {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	lib.locate(ctx)

	req, err := parseArgs(held, rest)
	if err != nil {
		return lib.refuse(err.Error())
	}

	answer := lib.run(ctx, held, req)
	if answer.failed {
		return lib.refuse(answer.text)
	}

	lib.say(answer.text)

	return 0
}

// run is one verb as the CLI and the MCP server both call it: the session's
// pending nudges open its answer, once.
func (lib *library) run(ctx context.Context, held command, req request) result {
	refusal := lib.callRefusal()
	if refusal != "" {
		return refused("%s", refusal)
	}

	answer := held.run(lib, ctx, req)
	if answer.failed {
		return answer
	}

	pending := lib.takeNudges()
	if pending != "" {
		answer.text = pending + "\n" + answer.text
	}

	return answer
}

func (lib *library) callRefusal() string {
	switch {
	case lib.outsideGit:
		return fmt.Sprintf("%s is not inside a git work tree; the tool library works only in one", lib.env.Base)
	case lib.session == "":
		return ErrNoCaller.Error()
	}

	return ""
}

// locate keys the own store as agentic-memory keys its own, under the same
// git work tree rule.
func (lib *library) locate(ctx context.Context) {
	key, err := projectkey.Key(ctx, lib.env.Base, lib.env.Checkout)
	if err == nil && lib.insideWorkTree(ctx) {
		lib.key = key

		return
	}

	if !lib.options.MemoryRequireGit {
		lib.key = projectkey.DirectoryKey(lib.env.Base)

		return
	}

	lib.outsideGit = true
}

func (lib *library) insideWorkTree(ctx context.Context) bool {
	if lib.env.Git == nil {
		return false
	}

	answer, err := lib.env.Git.Output(ctx, lib.env.Base, "rev-parse", "--is-inside-work-tree")

	return err == nil && strings.TrimSpace(answer) == "true"
}

// workTree is the top of the work tree the session works in, a worktree's own
// rather than its main checkout's, or empty where git answers none.
func (lib *library) workTree(ctx context.Context) string {
	if lib.env.Git == nil {
		return ""
	}

	answer, err := lib.env.Git.Output(ctx, lib.env.Base, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(answer) == "" {
		return ""
	}

	return filepath.Clean(strings.TrimSpace(answer))
}

func (lib *library) root() string { return filepath.Join(lib.env.Home, ".claude", self) }

func (lib *library) ownDir() string { return projectkey.Dir(lib.root(), lib.key) }

func (lib *library) checkout(ctx context.Context) string {
	return project.Root(ctx, lib.env.Base, lib.env.Checkout)
}

func (lib *library) now() time.Time { return lib.env.Now().UTC() }

func (lib *library) getenv(name string) string {
	if lib.env.Getenv == nil {
		return ""
	}

	return lib.env.Getenv(name)
}

// scopeProject keeps every read to the own store, as a sandboxed caller's
// launcher sets it.
func (lib *library) scopeProject() bool {
	return lib.getenv("AGENTIC_TOOLS_SCOPE") == "project"
}

// role orders the offer and nothing else: the caller's hidden role, else the
// agentic-memory seat its session holds.
func (lib *library) role(ctx context.Context) string {
	given := strings.TrimSpace(lib.getenv("AGENTIC_TOOLS_ROLE"))
	if given != "" {
		return given
	}

	if lib.key == "" || lib.env.Processes == nil || !pieces.ToolsSeatName.Holds(lib.options) {
		return ""
	}

	memoryStore := projectkey.Dir(seat.StoreRoot(lib.env.Home), lib.key)

	id, _ := seat.Of(ctx, seat.Dir(memoryStore), lib.env.Processes, lib.env.OwnerName)
	if id == "" {
		return ""
	}

	registry, found, err := seat.LoadRegistry(memoryStore)
	if err != nil || !found {
		return id
	}

	return cmp.Or(registry.NameOf(id), id)
}

// off answers a call while the piece is off: a hook says nothing, the MCP
// server lists no tools and says which option turns it on, a command refuses.
func off(ctx context.Context, piece pieces.Piece, server string, version string, verb string, isHook bool,
	streams hookio.Streams,
) int {
	switch {
	case isHook:
		return 0
	case verb == "mcp":
		err := mcpserver.ServeNone(ctx, streams.In, streams.Out, server, version, piece.Instructions())
		if err != nil {
			return 1
		}

		return 0
	}

	_, _ = fmt.Fprintln(streams.Err, piece.Off())

	return 1
}

func (lib *library) readOptions() pluginconfig.Options {
	options, err := pluginconfig.ReadWith(lib.env.Settings, lib.env.Getenv)
	if err != nil {
		return pluginconfig.Defaults()
	}

	return options
}

func (lib *library) embedder() (embed.Embedder, error) {
	config := embed.Config{
		Provider:   lib.options.EmbeddingProvider,
		Model:      lib.options.EmbeddingModel,
		Endpoint:   lib.options.EmbeddingEndpoint,
		Dimensions: lib.options.EmbeddingDimensions,
	}

	if lib.options.EmbeddingAPIKeyEnv != "" {
		config.APIKey = lib.getenv(lib.options.EmbeddingAPIKeyEnv)
	}

	if lib.env.Embedders == nil {
		return nil, embed.ErrNotConfigured
	}

	embedder, err := lib.env.Embedders(config)
	if err != nil {
		return nil, fmt.Errorf("no embedder answers: %w", err)
	}

	return embedder, nil
}

func (lib *library) promptVectors() string {
	return promptvector.Dir(lib.env.CacheRoot)
}

func (lib *library) say(text string) {
	if text != "" {
		_, _ = fmt.Fprintln(lib.streams.Out, strings.TrimRight(text, "\n"))
	}
}

func (lib *library) refuse(text string) int {
	_, _ = fmt.Fprintln(lib.streams.Err, "refused: "+text)

	return 1
}

type result struct {
	text   string
	failed bool
}

func done(text string) result { return result{text: text} }

func refused(format string, args ...any) result {
	return result{text: fmt.Sprintf(format, args...), failed: true}
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}
