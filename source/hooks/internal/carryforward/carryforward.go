// Package carryforward keeps each seat's carry-forward file current across a
// compaction.
//
// A seat is a named job at the work, held by one session at a time. Each seat
// carries its own lock under `.memory/roles/`, its own carry-forward under
// `.memory/carryforward/`, and its own nudge ladder, so several sessions
// coordinate through files rather than through whoever claimed first.
//
// session-start, session-log, prompt and stop answer hook events; holder,
// claim, take, release, refreshed and sweep are run from the conversation.
package carryforward

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
)

const self = "agentic-carryforward"

var ErrUsage = errors.New("usage: " + self +
	" session-start|session-log|holder|prompt|stop|claim|take|release|refreshed|sweep")

// Env is everything a carry-forward run reads from outside the project tree.
// Root is the main checkout; OwnerName is the process a seat's holder runs as.
type Env struct {
	Root          string
	Home          string
	OwnerName     string
	CompactWindow int
	Now           func() time.Time
	Processes     ProcessTable
}

type hook struct {
	env     Env
	streams hookio.Streams
}

func (hook *hook) memoryDir() string      { return filepath.Join(hook.env.Root, ".memory") }
func (hook *hook) rolesDir() string       { return seat.Dir(hook.env.Root) }
func (hook *hook) transcriptsDir() string { return filepath.Join(hook.memoryDir(), "transcripts") }
func (hook *hook) carryDir() string       { return filepath.Join(hook.memoryDir(), "carryforward") }

func (hook *hook) enabled() bool {
	info, err := os.Stat(hook.memoryDir())

	return err == nil && info.IsDir()
}

func (hook *hook) say(text string) {
	_, _ = fmt.Fprintln(hook.streams.Out, text)
}

func (hook *hook) refuse(text string) int {
	_, _ = fmt.Fprintln(hook.streams.Err, text)

	return 1
}

// Main runs one subcommand. A project opts in by carrying `.memory/`; the
// hook events stay silent without it and create nothing.
func Main(ctx context.Context, args []string, env Env, streams hookio.Streams) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}

	hook := &hook{env: env, streams: streams}
	rest := args[1:]

	switch args[0] {
	case "session-start":
		return hook.sessionStart(ctx)
	case "session-log":
		return hook.sessionLog(rest)
	case "holder":
		return hook.holder(ctx, rest)
	case "prompt":
		return hook.prompt(ctx)
	case "stop":
		return hook.stop()
	case "claim":
		return hook.claim(ctx, rest, false)
	case "take":
		return hook.claim(ctx, rest, true)
	case "release":
		return hook.release(ctx, rest)
	case "refreshed":
		return hook.refreshed(ctx, rest)
	case "sweep":
		return hook.sweepCommand(ctx)
	default:
		_, _ = fmt.Fprintln(streams.Err, ErrUsage)

		return 1
	}
}

// payload reads what the client handed the hook. A garbled payload names no
// session, and a hook reading none does nothing.
func (hook *hook) payload() hookio.Payload {
	payload, err := hookio.ReadPayload(hook.streams.In)
	if err != nil {
		return hookio.Payload{}
	}

	return payload
}

func arg(args []string, index int) string {
	if index < len(args) {
		return args[index]
	}

	return ""
}

func short(session string) string {
	if len(session) > 8 {
		return session[:8]
	}

	return session
}

func (hook *hook) sessionStart(ctx context.Context) int {
	if !hook.enabled() {
		return 0
	}

	payload := hook.payload()
	session := payload.SessionID

	if session != "" {
		// Without the record a later claim has to be handed the id, and the
		// seat still works: a failure here costs convenience, not state.
		_ = hook.rememberSession(ctx, session)
	}

	var output []string

	role := hook.roleOfSession(session)

	switch {
	case role != "":
		held, _ := hook.readLock(role)
		held.PID = hook.ownerPID(ctx)
		held.Started = hook.env.Processes.StartTime(ctx, held.PID)
		_ = hook.writeLock(role, held)

		rows := hook.sweep(ctx)
		if len(rows) > 0 {
			output = append(output, "Sweep of .memory/transcripts/:")
			output = append(output, rows...)
		}

		if payload.Source == "compact" {
			// The window is fresh, so the ladder starts at the threshold again.
			state := hook.loadState(session)
			state.PercentRung = nil
			_ = hook.saveState(session, state)

			prompt, err := os.ReadFile(filepath.Join(hook.env.Root, ".claude", "settings.local.md"))
			if err == nil {
				output = append(output, string(prompt))
			}
		}
	case payload.Source == "startup" || payload.Source == "resume" || payload.Source == "clear":
		holders := hook.holders()

		var named []string
		for _, name := range seat.Names(holders) {
			named = append(named, fmt.Sprintf("%s (%s)", name, short(holders[name].SessionID)))
		}

		held := strings.Join(named, ", ")
		if held == "" {
			held = "none"
		}

		output = append(output, "This session holds no role and writes no memory file. Read "+
			"the files under `.memory/carryforward/` for context, one per seat. Claim one with "+
			fmt.Sprintf("`%s claim <role> <session-id>` (seats: %s). Seats held: %s.", self, hook.roleUsage(), held))
	}

	if len(output) > 0 {
		hook.say(strings.Join(output, "\n"))
	}

	return 0
}

// sessionLog prints one piece of the delta log after a compaction; one hook
// runs per piece.
func (hook *hook) sessionLog(args []string) int {
	if !hook.enabled() {
		return 0
	}

	payload := hook.payload()
	if payload.Source != "compact" || payload.SessionID == "" || hook.roleOfSession(payload.SessionID) == "" {
		return 0
	}

	index, err := strconv.Atoi(arg(args, 0))
	if err != nil {
		return hook.refuse("usage: " + self + " session-log <piece number, from 1>")
	}

	pieces := hook.deltaLogPieces(payload.SessionID)
	if index >= 1 && index <= len(pieces) {
		hook.say(pieces[index-1])
	}

	return 0
}

func (hook *hook) prompt(ctx context.Context) int {
	if !hook.enabled() {
		return 0
	}

	payload := hook.payload()
	session := payload.SessionID

	role := hook.roleOfSession(session)
	if role == "" {
		role = hook.roleOfThisProcess(ctx)
	}

	if role == "" {
		return 0
	}

	state := hook.loadState(session)
	if hook.thresholdCrossed(session, payload.TranscriptPath, state) {
		state.Nudges++
		_ = hook.saveState(session, state)
		hook.say(nudgeText(role, session))
	}

	return 0
}

func (hook *hook) stop() int {
	if !hook.enabled() {
		return 0
	}

	payload := hook.payload()
	session := payload.SessionID

	role := hook.roleOfSession(session)
	if role == "" {
		return 0
	}

	state := hook.loadState(session)
	_ = hook.appendTurn(session, payload.TranscriptPath, &state)
	overdue := hook.thresholdCrossed(session, payload.TranscriptPath, state) && state.Nudges >= nudgesBeforeBackstop
	_ = hook.saveState(session, state)

	if overdue {
		_, _ = fmt.Fprintln(hook.streams.Err, backstopText(role, session))

		return 2
	}

	return 0
}

func (hook *hook) notOptedIn() int {
	return hook.refuse(fmt.Sprintf("no %s directory; this project has not opted in", hook.memoryDir()))
}

func (hook *hook) claim(ctx context.Context, args []string, force bool) int {
	if !hook.enabled() {
		return hook.notOptedIn()
	}

	role, given := arg(args, 0), arg(args, 1)
	known := hook.sessionOfThisProcess(ctx)

	// The id this process recorded at start outranks one typed on the command
	// line: an id read out of the sweep or another seat's files is not this
	// session's. A prefix of its own id is the same session, short.
	if given != "" && known != "" && !strings.HasPrefix(known, given) {
		return hook.refuse(fmt.Sprintf("this process is session %s, not %s; claim it with no id: %s claim %s",
			short(known), short(given), self, role))
	}

	session := known
	if session == "" {
		session = given
	}

	if !validRole(role) || session == "" {
		return hook.refuse(fmt.Sprintf("usage: %s claim|take <role> [session-id]  "+
			"(seats: %s; a new name is fine, lower case and hyphens). "+
			"The id is only needed where this session started before the machinery recorded it.",
			self, hook.roleUsage()))
	}

	held, locked := hook.readLock(role)
	if locked && !force && hook.ownerAlive(ctx, held.PID, held.Started) {
		return hook.refuse(fmt.Sprintf("%s is held by %s (pid %d, alive). Release it there, or say `take %s`.",
			role, short(held.SessionID), held.PID, role))
	}

	seated := hook.roleOfSession(session)
	if seated != "" && seated != role {
		return hook.refuse(fmt.Sprintf("this session already holds %s; release that seat before taking %s", seated, role))
	}

	pid := hook.ownerPID(ctx)
	record := seat.Claim{
		SessionID: session,
		PID:       pid,
		Started:   hook.env.Processes.StartTime(ctx, pid),
		Claimed:   hook.env.Now().Format("2006-01-02T15:04:05"),
	}

	if force && locked {
		record.SeizedFrom = held.SessionID
	}

	err := hook.seatFiles(role, session, record)
	if err != nil {
		return hook.refuse(err.Error())
	}

	state := hook.loadState(session)
	if !isFile(hook.statePath(session)) {
		var start int64

		transcript := hook.transcriptFor(session)
		if transcript != "" {
			info, err := os.Stat(transcript)
			if err == nil {
				start = info.Size()
			}
		}

		state.LogOffset = start
		state.TranscriptOffsetAtRefresh = start

		err = hook.saveState(session, state)
		if err != nil {
			return hook.refuse(err.Error())
		}
	}

	hook.say(fmt.Sprintf("%s: %s (pid %d), carry-forward carryforward/%s-memory.md, log starts at %d bytes",
		role, short(session), pid, role, state.LogOffset))

	return 0
}

func (hook *hook) seatFiles(role string, session string, record seat.Claim) error {
	for _, dir := range []string{hook.rolesDir(), hook.carryDir(), hook.transcriptsDir()} {
		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			return fmt.Errorf("creating %s for %s (%s): %w", dir, role, short(session), err)
		}
	}

	return hook.writeLock(role, record)
}

func (hook *hook) namedOrOwnRole(ctx context.Context, args []string) string {
	if len(args) > 0 {
		return args[0]
	}

	return hook.roleOfThisProcess(ctx)
}

func (hook *hook) release(ctx context.Context, args []string) int {
	role := hook.namedOrOwnRole(ctx, args)
	if !validRole(role) {
		return hook.refuse(fmt.Sprintf("usage: %s release <%s>", self, hook.roleUsage()))
	}

	_ = os.Remove(hook.lockPath(role))
	hook.say(role + " released")

	return 0
}

func (hook *hook) refreshed(ctx context.Context, args []string) int {
	role := hook.namedOrOwnRole(ctx, args)
	if !validRole(role) {
		return hook.refuse(fmt.Sprintf("usage: %s refreshed [%s] — no seat matches this process, so name the role",
			self, hook.roleUsage()))
	}

	held, _ := hook.readLock(role)
	if held.SessionID == "" {
		return hook.refuse("no session holds " + role)
	}

	session := held.SessionID
	state := hook.loadState(session)
	_ = os.Remove(hook.logPath(session))
	state.TranscriptOffsetAtRefresh = state.LogOffset

	// Climb to the lowest rung above where the context actually stands: a rung
	// already passed can never fire again inside this window.
	rung := rungOf(state) + rearmPercentMargin

	percent, known := hook.contextPercent(session)
	for known && float64(rung) <= percent {
		rung += rearmPercentMargin
	}

	state.PercentRung = &rung
	state.Nudges = 0

	err := hook.saveState(session, state)
	if err != nil {
		return hook.refuse(err.Error())
	}

	hook.say(role + " carry-forward recorded; delta log truncated")

	return 0
}

func (hook *hook) sweepCommand(ctx context.Context) int {
	if !hook.enabled() {
		return hook.notOptedIn()
	}

	rows := hook.sweep(ctx)
	if len(rows) == 0 {
		hook.say("nothing to sweep")

		return 0
	}

	hook.say(strings.Join(rows, "\n"))

	return 0
}

// holder exits 0 naming the holder where a live session holds the seat, else
// 1, so a launcher opens no second session on a held seat.
func (hook *hook) holder(ctx context.Context, args []string) int {
	role := arg(args, 0)
	if role == "" {
		return 1
	}

	held, _ := hook.readLock(role)
	if held.PID == 0 || !hook.ownerAlive(ctx, held.PID, held.Started) {
		return 1
	}

	hook.say(fmt.Sprintf("%s is held by session %s (pid %d)", role, short(held.SessionID), held.PID))

	return 0
}
