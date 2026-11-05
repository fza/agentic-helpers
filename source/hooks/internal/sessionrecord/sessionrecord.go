// Package sessionrecord keeps one record per Claude Code session, one key per
// session id in `<cache>/session-records/sessions.slots` (internal/slots):
// where the session runs, written at SessionStart, and its last hook event
// with the time it began waiting for the user, written at every event.
// SessionEnd clears the key, and SessionStart reclaims every key no event
// touched for slots.StaleAge. `agentic-helpers sessions --json` prints every
// record with the schema version of its fields, each marked gone where the
// Claude Code process it names no longer runs.
//
// `claude agents --json` names every live session and whether it waits, but
// neither the terminal it runs in nor when its wait began: only a hook sees
// both.
package sessionrecord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

const (
	self      = "agentic-helpers"
	slotsName = "sessions.slots"
	// Schema is the version of a record's fields, in the slot and in the
	// printed object alike. A change to what a field means takes a new one.
	Schema = 1
	// maxID bounds a session or agent id, as every per-session key of the
	// home does.
	maxID = 128
	// commonDirLimit bounds the git spawn naming the session's repository.
	commonDirLimit = 2 * time.Second
)

const usage = "usage: " + self + " record | sessions --json"

// The hook events a record tells apart. Any other event is recorded as the
// last one and changes nothing else.
const (
	eventSessionStart = "SessionStart"
	eventSessionEnd   = "SessionEnd"
	eventPrompt       = "UserPromptSubmit"
	eventPreTool      = "PreToolUse"
	eventPostTool     = "PostToolUse"
	eventStop         = "Stop"
	eventPermission   = "PermissionRequest"
)

// What a waiting session waits for: the user's next prompt, or the answer to
// a permission dialog.
const (
	waitPrompt     = "prompt"
	waitPermission = "permission"
)

// Processes reads the process table: the Claude Code process above the hook,
// when it started, and its controlling terminal.
type Processes interface {
	seat.Processes
	Terminal(ctx context.Context, pid int) string
}

// Env says where the records live, Dir, empty where the cache root is
// unknown; where a session runs is read from Getenv and Processes, the
// Claude Code process being the one memory's seats find (seat.OwnerAncestor).
type Env struct {
	Dir       string
	Getenv    func(string) string
	Processes Processes
	Checkout  project.CommonDirLookup
	OwnerName string
	Now       func() time.Time
}

// Dir is where the records live below the home's cache root (internal/home).
func Dir(cache string) string {
	return filepath.Join(cache, "session-records")
}

type payload struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	AgentID   string `json:"agent_id"`
	Cwd       string `json:"cwd"`
}

// Main runs `record`, the hook on every event a record follows, and
// `sessions --json`.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	switch {
	case len(args) == 1 && args[0] == "record":
		record(ctx, env, streams)

		return 0
	case len(args) == 2 && args[0] == "sessions" && args[1] == "--json":
		return list(ctx, env, streams)
	default:
		_, _ = fmt.Fprintln(streams.Err, usage)

		return 1
	}
}

func (env Env) file() *slots.File {
	file := slots.Open(filepath.Dir(env.Dir), filepath.Join(env.Dir, slotsName))
	file.Now = env.Now

	return file
}

func (env Env) now() time.Time {
	return env.Now().UTC().Truncate(time.Millisecond)
}

// record writes the event a hook payload names into its session's record. A
// payload that cannot be read, or names a session id outside the shape every
// per-session key takes, records nothing; a write that fails says so on
// stderr and still exits 0, since no event waits on a record.
func record(ctx context.Context, env Env, streams hookio.Streams) {
	var event payload

	err := json.NewDecoder(streams.In).Decode(&event)
	if err != nil || !validID(event.SessionID) || env.Dir == "" {
		return
	}

	file := env.file()

	if event.Event == eventSessionEnd {
		report(streams.Err, "clearing", file.Delete(ctx, event.SessionID))

		return
	}

	if event.Event == eventSessionStart {
		report(streams.Err, "reclaiming", file.ReclaimStale(ctx))
	}

	// A record no SessionStart wrote, as for a session started before the
	// hook was installed, learns where it runs at its first event. That is
	// looked up before the writer lock is taken, since it may spawn git.
	var located *place
	if event.Event == eventSessionStart || !stored(ctx, file, event.SessionID) {
		where := locate(ctx, env, event.Cwd)
		located = &where
	}

	at := env.now()

	err = file.Update(ctx, event.SessionID, func(value []byte, _ bool) ([]byte, error) {
		current := entry{Schema: Schema}
		_ = decode(value, &current)

		if located != nil {
			current.place = *located
		}

		current.apply(event.Event, event.AgentID, at)

		return slots.Encode(&current)
	})
	report(streams.Err, "writing", err)
}

// stored is whether key holds a record of this schema, read without the
// writer lock.
func stored(ctx context.Context, file *slots.File, key string) bool {
	value, found, err := file.Get(ctx, key)
	if err != nil || !found {
		return false
	}

	var held entry

	return decode(value, &held)
}

// decode reads a stored record into held, answering whether it is one of
// this schema; held stays as it is otherwise.
func decode(value []byte, held *entry) bool {
	if len(value) == 0 {
		return false
	}

	var decoded entry

	err := json.Unmarshal(value, &decoded)
	if err != nil || decoded.Schema != Schema {
		return false
	}

	*held = decoded

	return true
}

func report(stderr io.Writer, action string, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s the session record: %v\n", self, action, err)
	}
}

// locate reads where the session runs: the Claude Code process, its start
// and terminal, the terminal's variables, and the session's folder with its
// repository.
func locate(ctx context.Context, env Env, cwd string) place {
	where := place{
		TermProgram:      env.Getenv("TERM_PROGRAM"),
		ITermSessionID:   env.Getenv("ITERM_SESSION_ID"),
		TerminalEmulator: env.Getenv("TERMINAL_EMULATOR"),
		TmuxPane:         env.Getenv("TMUX_PANE"),
		BundleID:         env.Getenv("__CFBundleIdentifier"),
		Cwd:              cwd,
	}

	where.PID = seat.OwnerAncestor(ctx, env.Processes, env.OwnerName)
	if where.PID != 0 {
		started, found := env.Processes.StartTime(ctx, where.PID)
		if found {
			where.Started = started.UTC()
		}

		where.TTY = env.Processes.Terminal(ctx, where.PID)
	}

	if cwd != "" && env.Checkout != nil {
		bounded, cancel := context.WithTimeout(ctx, commonDirLimit)
		defer cancel()

		common, err := env.Checkout.CommonDir(bounded, cwd)
		if err == nil {
			where.GitCommonDir = common
		}
	}

	return where
}

// apply records event, sent at at by agent (empty for the session's own
// agent), as the last one, and begins or ends the wait it begins or ends.
//
// A Stop begins a wait for the next prompt, keeping the start of a wait
// already running. A permission dialog begins one where none runs, owned by
// the agent asking. A prompt ends any wait; a tool event ends a wait only the
// agent it comes from owns, so a subagent working in the background never
// ends the wait for the user's prompt. An agent id outside the shape of a
// session id begins and ends nothing.
func (held *entry) apply(event string, agent string, at time.Time) {
	held.Event = event
	held.EventAt = at

	if agent != "" && !validID(agent) {
		return
	}

	switch event {
	case eventPrompt:
		held.endWait()
	case eventPreTool, eventPostTool:
		if held.waiting() && held.WaitingAgent == agent {
			held.endWait()
		}
	case eventStop:
		if !held.waiting() {
			held.WaitingSince = at
		}

		held.WaitingFor = waitPrompt
		held.WaitingAgent = ""
	case eventPermission:
		if !held.waiting() {
			held.WaitingSince = at
			held.WaitingFor = waitPermission
			held.WaitingAgent = agent
		}
	}
}

func (held *entry) waiting() bool {
	return !held.WaitingSince.IsZero()
}

func (held *entry) endWait() {
	held.WaitingSince = time.Time{}
	held.WaitingFor = ""
	held.WaitingAgent = ""
}

// validID holds an id to `^[A-Za-z0-9_-]{1,128}$`, the shape every session id
// a per-session key is built from takes, without compiling a pattern at
// every start of the program.
func validID(id string) bool {
	if id == "" || len(id) > maxID {
		return false
	}

	for _, char := range []byte(id) {
		letter := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
		if !letter && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}

	return true
}

// listed is a record as `sessions --json` prints it: every field present,
// a time unknown as null.
type listed struct {
	Schema           int        `json:"schema"`
	SessionID        string     `json:"session_id"`
	PID              int        `json:"pid"`
	Started          *time.Time `json:"started"`
	TTY              string     `json:"tty"`
	TermProgram      string     `json:"term_program"`
	ITermSessionID   string     `json:"iterm_session_id"`
	TerminalEmulator string     `json:"terminal_emulator"`
	TmuxPane         string     `json:"tmux_pane"`
	BundleID         string     `json:"bundle_id"`
	Cwd              string     `json:"cwd"`
	GitCommonDir     string     `json:"git_common_dir"`
	Event            string     `json:"event"`
	EventAt          *time.Time `json:"event_at"`
	WaitingSince     *time.Time `json:"waiting_since"`
	WaitingFor       string     `json:"waiting_for"`
	Gone             bool       `json:"gone"`
}

// list prints every record of this schema as one JSON array, in session id
// order. A record is gone where its process is not running, or runs from
// another start than the one recorded, as a reused pid does.
func list(ctx context.Context, env Env, streams hookio.Streams) int {
	printed := []listed{}

	if env.Dir != "" {
		held, err := env.file().All(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(streams.Err, "%s: reading the session records: %v\n", self, err)

			return 1
		}

		for _, slot := range held {
			var record entry
			if !validID(slot.Key) || !decode(slot.Value, &record) {
				continue
			}

			printed = append(printed, record.listed(ctx, env, slot.Key))
		}
	}

	slices.SortFunc(printed, func(a listed, b listed) int { return strings.Compare(a.SessionID, b.SessionID) })

	data, err := json.MarshalIndent(printed, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintf(streams.Err, "%s: encoding the session records: %v\n", self, err)

		return 1
	}

	_, _ = fmt.Fprintln(streams.Out, string(data))

	return 0
}

func (held entry) listed(ctx context.Context, env Env, session string) listed {
	return listed{
		Schema: Schema, SessionID: session,
		PID: held.PID, Started: optional(held.Started), TTY: held.TTY,
		TermProgram: held.TermProgram, ITermSessionID: held.ITermSessionID,
		TerminalEmulator: held.TerminalEmulator, TmuxPane: held.TmuxPane, BundleID: held.BundleID,
		Cwd: held.Cwd, GitCommonDir: held.GitCommonDir,
		Event: held.Event, EventAt: optional(held.EventAt),
		WaitingSince: optional(held.WaitingSince), WaitingFor: held.WaitingFor,
		Gone: !held.running(ctx, env.Processes),
	}
}

// running is whether the recorded process still runs from the start
// recorded: a pid alone may since name another process.
func (held entry) running(ctx context.Context, processes Processes) bool {
	if held.PID <= 0 || held.Started.IsZero() {
		return false
	}

	started, found := processes.StartTime(ctx, held.PID)

	return found && started.Equal(held.Started)
}

func optional(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}

	return &at
}
