package shellguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

const (
	modeDontAsk = "dontAsk"
	modeAuto    = "auto"
	modeBypass  = "bypassPermissions"

	// After this many countdowns ran out unanswered since the session's last
	// prompt, nobody is watching: an ask denies at once.
	unansweredLimit = 3
)

var ErrUsage = errors.New("usage: agentic-shell-guard [permission]")

var sessionName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Clock times an ask's countdown. Wait returns early, with an error, when the
// context ends: a hook stopped by a signal answers nothing.
type Clock interface {
	Now() time.Time
	Wait(ctx context.Context, duration time.Duration) error
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}

func (SystemClock) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for an answer: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// Run answers the hook event its arguments name: none for PreToolUse,
// `permission` for PermissionRequest.
func Run(ctx context.Context, args []string, env Env, streams Streams) int {
	switch {
	case len(args) == 0:
		return Main(ctx, env, streams)
	case len(args) == 1 && args[0] == "permission":
		return Permission(ctx, env, streams)
	}

	_, _ = fmt.Fprintln(streams.Err, self+": "+ErrUsage.Error())

	return 2
}

type permissionAnswer struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// Permission answers one PermissionRequest on Bash. In auto and
// bypassPermissions mode, for a call shell-guard asks about, it waits out the
// countdown and denies; whichever of the person and the hook answers first
// decides. Every other call, and anything it cannot read, it leaves to the
// person at the prompt.
func Permission(ctx context.Context, env Env, streams Streams) (code int) {
	defer func() {
		failure := recover()
		if failure != nil {
			code = 0
		}
	}()

	read, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	var call payload

	err = json.NewDecoder(streams.In).Decode(&call)
	if err != nil || call.ToolInput.Command == nil || !pieces.ShellGuard.On(read) {
		return 0
	}

	seconds := read.ShellGuardAskTimeout
	if call.PermissionMode != modeAuto && call.PermissionMode != modeBypass || seconds == 0 {
		return 0
	}

	verdict := judgeCall(ctx, env, read, call)
	if verdict.Decision != Ask {
		return 0
	}

	held := countdownsOf(ctx, env, call)
	if held.unanswered() >= unansweredLimit {
		return deny(streams, fmt.Sprintf("%s: %d countdowns ran out unanswered since the last prompt; %s",
			self, unansweredLimit, verdict.Reason))
	}

	err = env.Clock.Wait(ctx, time.Duration(seconds)*time.Second)
	if err != nil {
		return 0
	}

	held.ranOut(env.Clock.Now())

	return deny(streams, fmt.Sprintf("%s: nobody answered within %ds; %s", self, seconds, verdict.Reason))
}

func deny(streams Streams, message string) int {
	var reply permissionAnswer

	reply.HookSpecificOutput.HookEventName = "PermissionRequest"
	reply.HookSpecificOutput.Decision.Behavior = "deny"
	reply.HookSpecificOutput.Decision.Message = message

	_ = json.NewEncoder(streams.Out).Encode(reply)

	return 0
}

// countdowns are the session's countdowns that ran out since its last prompt.
// A session without a usable state file counts none and notes none.
type countdowns struct {
	path       string
	lastPrompt time.Time
	RanOut     []time.Time `json:"ran_out"`
}

func countdownsOf(ctx context.Context, env Env, call payload) *countdowns {
	held := &countdowns{}

	if env.StateDir == "" || !sessionName.MatchString(call.SessionID) {
		return held
	}

	held.path = filepath.Join(env.StateDir, "shell-guard", call.SessionID+".json")

	turns, err := transcript.Since(ctx, call.TranscriptPath, time.Time{})
	if err == nil && len(turns) > 0 {
		held.lastPrompt = turns[len(turns)-1].At
	}

	content, err := os.ReadFile(held.path)
	if err != nil {
		return held
	}

	_ = json.Unmarshal(content, held)

	return held
}

func (held *countdowns) unanswered() int {
	count := 0

	for _, at := range held.RanOut {
		if at.After(held.lastPrompt) {
			count++
		}
	}

	return count
}

// ranOut notes a countdown that ran out. A note it cannot write costs only the
// deny-at-once.
func (held *countdowns) ranOut(at time.Time) {
	if held.path == "" {
		return
	}

	held.RanOut = append(held.RanOut, at)

	content, err := json.Marshal(held)
	if err != nil {
		return
	}

	err = os.MkdirAll(filepath.Dir(held.path), 0o700)
	if err != nil {
		return
	}

	staged := fmt.Sprintf("%s.%d", held.path, os.Getpid())

	err = os.WriteFile(staged, content, 0o600)
	if err != nil {
		return
	}

	_ = os.Rename(staged, held.path)
}
