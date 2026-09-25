package carryforward_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/carryforward"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const session = "test-session-0001"

type fixture struct {
	t          *testing.T
	env        carryforward.Env
	transcript string
	processes  *test.Processes
	seats      *test.Seats
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	root := t.TempDir()
	mkdir(t, filepath.Join(root, ".memory", "transcripts"))

	transcript := filepath.Join(root, "transcript.jsonl")
	test.WriteFile(t, transcript, "")

	processes := test.NewProcesses()
	seats := &test.Seats{}

	return &fixture{
		t:          t,
		transcript: transcript,
		processes:  processes,
		seats:      seats,
		env: carryforward.Env{
			Root:      root,
			Home:      t.TempDir(),
			OwnerName: "claude",
			Now:       time.Now,
			Processes: processes,
			Seats:     seats,
		},
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()

	err := os.MkdirAll(path, 0o755)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
}

func (f *fixture) run(stdin string, args ...string) result {
	f.t.Helper()

	captured := &test.Captured{}
	code := carryforward.Main(context.Background(), args, f.env, captured.Streams(stdin))

	return result{code: code, stdout: captured.Out.String(), stderr: captured.Err.String()}
}

func (f *fixture) payload(extra map[string]any) string {
	f.t.Helper()

	base := map[string]any{"session_id": session, "transcript_path": f.transcript}
	for key, value := range extra {
		base[key] = value
	}

	data, err := json.Marshal(base)
	if err != nil {
		f.t.Fatalf("encoding the payload: %v", err)
	}

	return string(data)
}

func (f *fixture) hookRun(args ...string) result {
	return f.run(f.payload(nil), args...)
}

func (f *fixture) sourced(source string, args ...string) result {
	return f.run(f.payload(map[string]any{"source": source}), args...)
}

func (f *fixture) path(parts ...string) string {
	return filepath.Join(append([]string{f.env.Root, ".memory"}, parts...)...)
}

func (f *fixture) lockPath(role string) string {
	return f.path("roles", role+".json")
}

func (f *fixture) lock(role string) map[string]any {
	f.t.Helper()

	return readJSON(f.t, f.lockPath(role))
}

func (f *fixture) state() map[string]any {
	f.t.Helper()

	return readJSON(f.t, f.path("transcripts", session+".state"))
}

func (f *fixture) log() string {
	return f.path("transcripts", session+".md")
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	held := map[string]any{}

	err = json.Unmarshal(data, &held)
	if err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}

	return held
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}

	test.WriteFile(t, path, string(data))
}

func (f *fixture) deadLock(role string, holder string) {
	writeJSON(f.t, f.lockPath(role), map[string]any{"session_id": holder, "pid": test.DeadPID, "started": "irrelevant"})
}

func (f *fixture) claim(role string, holder string) {
	f.t.Helper()

	got := f.run("", "claim", role, holder)
	if got.code != 0 {
		f.t.Fatalf("the claim should succeed, stderr: %s", got.stderr)
	}
}

func (f *fixture) sensor(percent float64, age time.Duration, size int) {
	reading := map[string]any{"used_percentage": percent, "at": float64(time.Now().Add(-age).UnixNano()) / 1e9}
	if size > 0 {
		reading["size"] = size
	}

	writeJSON(f.t, f.path("transcripts", session+".ctx"), reading)
}

func transcriptLine(t *testing.T, kind string, blocks []map[string]any) string {
	t.Helper()

	data, err := json.Marshal(map[string]any{"type": kind, "message": map[string]any{"content": blocks}})
	if err != nil {
		t.Fatalf("encoding a transcript line: %v", err)
	}

	return string(data) + "\n"
}

func (f *fixture) appendTurn(asked string, replied string, tool string) {
	f.t.Helper()

	blocks := []map[string]any{{"type": "text", "text": replied}}
	if tool != "" {
		blocks = append(blocks, map[string]any{"type": "tool_use", "name": tool, "input": map[string]any{"file_path": "/x/y.go"}})
	}

	text := transcriptLine(f.t, "user", []map[string]any{{"type": "text", "text": asked}}) + transcriptLine(f.t, "assistant", blocks)

	file, err := os.OpenFile(f.transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatalf("opening the transcript: %v", err)
	}

	defer func() { _ = file.Close() }()

	_, err = file.WriteString(text)
	if err != nil {
		f.t.Fatalf("appending to the transcript: %v", err)
	}
}

func (f *fixture) compactedLog() []string {
	pieces := make([]string, 0, 5)
	for index := 1; index <= 5; index++ {
		pieces = append(pieces, f.sourced("compact", "session-log", strconv.Itoa(index)).stdout)
	}

	return pieces
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestHolder(t *testing.T) {
	f := newFixture(t)

	if f.run("", "holder", "drive").code != 1 {
		t.Error("a free seat should report no holder")
	}

	f.claim("drive", session)

	held := f.run("", "holder", "drive")
	if held.code != 0 || !strings.Contains(held.stdout, "drive is held by session") {
		t.Errorf("a live holder should be named, got: %+v", held)
	}

	f.deadLock("drive", session)

	if f.run("", "holder", "drive").code != 1 {
		t.Error("a dead holder should free the seat")
	}

	writeJSON(t, f.lockPath("drive"), map[string]any{"session_id": session, "pid": test.OwnerPID, "started": "an earlier run"})

	if f.run("", "holder", "drive").code != 1 {
		t.Error("a reused pid should not pass for the holder")
	}

	writeJSON(t, f.lockPath("drive"), map[string]any{"session_id": session, "pid": test.ShellPID, "started": "shell"})

	if f.run("", "holder", "drive").code != 1 {
		t.Error("a live process that is not the owner should not hold the seat")
	}
}

func TestClaim(t *testing.T) {
	t.Run("writes the lock and refuses a second live claim", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		record := f.lock("drive")
		if record["session_id"] != session || record["pid"] != float64(test.OwnerPID) || record["started"] == "" {
			t.Errorf("the lock should name the session and its owner process, got: %v", record)
		}

		if len(f.seats.Calls) != 1 || f.seats.Calls[0].Verb != "take" {
			t.Errorf("the seat should be registered with sddap, got: %v", f.seats.Calls)
		}

		second := f.run("", "claim", "drive", "other-session")
		if second.code != 1 || !strings.Contains(second.stderr, "drive is held by") || !strings.Contains(second.stderr, "take drive") {
			t.Errorf("a second claim should be refused, naming take, got: %+v", second)
		}

		if f.lock("drive")["session_id"] != session {
			t.Error("the refused claim should leave the lock alone")
		}
	})

	t.Run("take seizes and records the previous holder", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		if f.run("", "take", "drive", "other-session").code != 0 {
			t.Fatal("take should succeed against a live holder")
		}

		record := f.lock("drive")
		if record["session_id"] != "other-session" || record["seized_from"] != session {
			t.Errorf("the seizure should be recorded, got: %v", record)
		}
	})

	t.Run("succeeds where the holder is dead", func(t *testing.T) {
		f := newFixture(t)
		f.deadLock("drive", "dead-session")
		f.claim("drive", session)

		if f.lock("drive")["session_id"] != session {
			t.Error("a dead holder's seat should go to the claimant")
		}
	})

	t.Run("needs no id once the session started", func(t *testing.T) {
		f := newFixture(t)
		f.sourced("startup", "session-start")

		got := f.run("", "claim", "settle")
		if got.code != 0 || f.lock("settle")["session_id"] != session {
			t.Errorf("the recorded session should be claimed, got: %+v", got)
		}
	})

	t.Run("refuses an id naming another session", func(t *testing.T) {
		f := newFixture(t)
		f.sourced("startup", "session-start")

		got := f.run("", "claim", "settle", "another-session-9999")
		if got.code != 1 || !strings.Contains(got.stderr, "not another") || exists(f.lockPath("settle")) {
			t.Errorf("a foreign id should be refused, got: %+v", got)
		}
	})

	t.Run("a short form of its own id is the same session", func(t *testing.T) {
		f := newFixture(t)
		f.sourced("startup", "session-start")

		got := f.run("", "claim", "settle", session[:8])
		if got.code != 0 || f.lock("settle")["session_id"] != session {
			t.Errorf("the prefix should resolve to the recorded session, got: %+v", got)
		}
	})

	t.Run("refuses rather than guessing with no recorded session", func(t *testing.T) {
		f := newFixture(t)

		got := f.run("", "claim", "settle")
		if got.code != 1 || !strings.Contains(got.stderr, "usage:") || exists(f.lockPath("settle")) {
			t.Errorf("a claim with no session should be refused, got: %+v", got)
		}
	})

	t.Run("leaves an existing state alone", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		state := f.state()
		state["turns"] = 7
		state["log_offset"] = 42
		writeJSON(t, f.path("transcripts", session+".state"), state)

		f.run("", "release", "drive")
		f.claim("drive", session)

		if f.state()["turns"] != float64(7) || f.state()["log_offset"] != float64(42) {
			t.Error("a reclaim should keep the session's state")
		}
	})

	t.Run("two seats held by different sessions", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.claim("settle", "settle-session")

		if f.lock("drive")["session_id"] != session || f.lock("settle")["session_id"] != "settle-session" {
			t.Error("each seat should keep its own holder")
		}
	})

	t.Run("one session cannot hold two seats", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		got := f.run("", "claim", "settle", session)
		if got.code != 1 || !strings.Contains(got.stderr, "already holds drive") || exists(f.lockPath("settle")) {
			t.Errorf("a second seat should be refused, got: %+v", got)
		}
	})

	t.Run("a role nobody named before is claimable and suggested", func(t *testing.T) {
		f := newFixture(t)
		f.claim("chart-sweep", session)

		seatless := f.run(`{"session_id": "no-seat", "source": "startup"}`, "session-start")
		if !strings.Contains(seatless.stdout, "chart-sweep") {
			t.Errorf("a held seat should be suggested, got: %s", seatless.stdout)
		}
	})

	for _, name := range []string{"../escape", "Drive", "with space", strings.Repeat("a", 30), ""} {
		t.Run("refuses role "+name, func(t *testing.T) {
			f := newFixture(t)

			got := f.run("", "claim", name, session)
			if got.code != 1 || !strings.Contains(got.stderr, "usage:") || exists(f.path("roles")) {
				t.Errorf("a role that is no path segment should be refused, got: %+v", got)
			}
		})
	}

	t.Run("seeds offsets so the first stop logs only new turns", func(t *testing.T) {
		f := newFixture(t)
		harness := filepath.Join(f.env.Home, ".claude", "projects", strings.ReplaceAll(f.env.Root, "/", "-"), session+".jsonl")
		old := transcriptLine(t, "user", []map[string]any{{"type": "text", "text": "old history"}})
		test.WriteFile(t, harness, old)

		f.claim("drive", session)

		state := f.state()
		if state["log_offset"] != float64(len(old)) || state["transcript_offset_at_refresh"] != float64(len(old)) {
			t.Errorf("both offsets should start at the harness transcript's end, got: %v", state)
		}

		test.WriteFile(t, f.transcript, old)
		f.appendTurn("new ask", "new reply", "")
		f.hookRun("stop")

		body, _ := os.ReadFile(f.log())
		if !strings.Contains(string(body), "new ask") || strings.Contains(string(body), "old history") {
			t.Errorf("the log should hold only turns after the claim, got: %s", body)
		}
	})

	t.Run("a seat in a worktree finds its own harness transcript", func(t *testing.T) {
		f := newFixture(t)
		slug := strings.ReplaceAll(f.env.Root, "/", "-") + "--claude-worktrees-settle"
		test.WriteFile(t, filepath.Join(f.env.Home, ".claude", "projects", slug, session+".jsonl"), strings.Repeat("x", 512))

		f.claim("settle", session)

		if f.state()["log_offset"] != float64(512) {
			t.Error("the sibling project directory should be found")
		}
	})
}

func TestReleaseAndBudget(t *testing.T) {
	t.Run("release frees the seat and tells sddap", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		got := f.run("", "release")
		if got.code != 0 || exists(f.lockPath("drive")) {
			t.Errorf("the process's own seat should be released, got: %+v", got)
		}

		if f.seats.Calls[len(f.seats.Calls)-1].Verb != "release" {
			t.Error("the release should reach sddap")
		}
	})

	t.Run("the owner speaking refills the seat budget", func(t *testing.T) {
		f := newFixture(t)
		f.sourced("startup", "session-start")
		f.run("", "claim", "validate")
		budget := f.path("roles", "validate.autopilot")
		test.WriteFile(t, budget, "22")

		if f.hookRun("prompt").code != 0 || exists(budget) {
			t.Error("a prompt should clear the seat's budget")
		}
	})

	t.Run("a session holding no seat touches no budget", func(t *testing.T) {
		f := newFixture(t)
		budget := f.path("roles", "validate.autopilot")
		test.WriteFile(t, budget, "22")

		if f.hookRun("prompt").code != 0 || !exists(budget) {
			t.Error("a seatless prompt should leave the budget alone")
		}
	})
}

func TestInertWithoutMemory(t *testing.T) {
	f := newFixture(t)
	f.env.Root = t.TempDir()

	for _, mode := range []string{"session-start", "prompt", "stop"} {
		got := f.sourced("startup", mode)
		if got.code != 0 || got.stdout != "" {
			t.Errorf("%s should stay silent, got: %+v", mode, got)
		}
	}

	for _, mode := range []string{"claim", "sweep"} {
		got := f.run("", mode, "drive", "any-session")
		if got.code != 1 || !strings.Contains(got.stderr, "has not opted in") {
			t.Errorf("%s should refuse, got: %+v", mode, got)
		}
	}

	entries, _ := os.ReadDir(f.env.Root)
	if len(entries) != 0 {
		t.Error("nothing should be created")
	}
}

func TestNudgeLadder(t *testing.T) {
	t.Run("a refresh disarms the trigger", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)

		if !strings.Contains(f.hookRun("prompt").stdout, "carry-forward") {
			t.Fatal("a reading past the threshold should nudge")
		}

		f.run("", "refreshed")

		if f.state()["percent_rung"] != float64(75) {
			t.Errorf("the rung should climb one margin, got: %v", f.state()["percent_rung"])
		}

		f.sensor(72, 0, 0)

		if f.hookRun("prompt").stdout != "" || f.hookRun("stop").code != 0 {
			t.Error("a reading below the new rung should stay quiet")
		}
	})

	t.Run("the trigger re-arms once context climbs further", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)
		f.hookRun("prompt")
		f.run("", "refreshed")
		f.sensor(80, 0, 0)

		if !strings.Contains(f.hookRun("prompt").stdout, "carry-forward") {
			t.Error("a reading past the new rung should nudge")
		}
	})

	t.Run("a late nudge climbs past the reading it answered", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)
		f.hookRun("prompt")
		f.sensor(87, 0, 0)
		f.run("", "refreshed")

		if f.state()["percent_rung"] != float64(95) {
			t.Errorf("the rung should clear the current reading, got: %v", f.state()["percent_rung"])
		}

		f.sensor(90, 0, 0)

		if f.hookRun("prompt").stdout != "" {
			t.Error("a reading below the climbed rung should stay quiet")
		}
	})

	t.Run("a compaction resets the ladder", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(87, 0, 0)
		f.hookRun("prompt")
		f.run("", "refreshed")
		f.sourced("compact", "session-start")

		if f.state()["percent_rung"] != nil {
			t.Error("a compaction should reset the rung")
		}

		f.sensor(70, 0, 0)

		if !strings.Contains(f.hookRun("prompt").stdout, "carry-forward") {
			t.Error("the threshold should nudge again after a compaction")
		}
	})

	t.Run("a refresh never wedges the turn", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)

		for range 4 {
			f.hookRun("prompt")
		}

		if f.hookRun("stop").code != 2 {
			t.Error("unanswered nudges should block the stop")
		}

		f.run("", "refreshed")

		for range 4 {
			f.hookRun("prompt")
		}

		if f.hookRun("stop").code != 0 {
			t.Error("a refresh should unblock the stop")
		}
	})

	t.Run("the backstop blocks only after two nudges", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)

		for nudge := range 2 {
			if f.hookRun("stop").code != 0 {
				t.Errorf("a stop after %d nudges should pass", nudge)
			}

			f.hookRun("prompt")
		}

		blocked := f.hookRun("stop")
		if blocked.code != 2 || !strings.Contains(blocked.stderr, "overdue") {
			t.Errorf("the third stop should block, got: %+v", blocked)
		}

		f.sensor(40, 0, 0)

		if f.hookRun("stop").code != 0 {
			t.Error("unanswered nudges should not block once the context is below the rung")
		}
	})

	t.Run("refreshed truncates the log and clears nudges", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)
		f.appendTurn("ask", "reply", "")
		f.hookRun("stop")
		f.hookRun("prompt")
		f.hookRun("prompt")

		if f.run("", "refreshed").code != 0 || exists(f.log()) {
			t.Fatal("a refresh should truncate the log")
		}

		state := f.state()
		if state["nudges"] != float64(0) || state["transcript_offset_at_refresh"] != state["log_offset"] {
			t.Errorf("a refresh should reset the ladder, got: %v", state)
		}
	})
}

func TestNudgeSensor(t *testing.T) {
	cases := []struct {
		name       string
		percent    float64
		age        time.Duration
		size       int
		window     int
		transcript int
		nudges     bool
	}{
		{name: "above the threshold", percent: 70, nudges: true},
		{name: "below the threshold", percent: 40},
		{name: "early compaction window", percent: 14, size: 1_000_000, window: 200_000, nudges: true},
		{name: "no early window", percent: 14, size: 1_000_000},
		{name: "stale sensor falls back to transcript growth", percent: 10, age: 10 * time.Minute, transcript: 500 * 1024, nudges: true},
		{name: "stale sensor below the growth threshold", percent: 99, age: 10 * time.Minute, transcript: 1024},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.env.CompactWindow = tc.window
			f.claim("drive", session)
			f.sensor(tc.percent, tc.age, tc.size)

			if tc.transcript > 0 {
				test.WriteFile(t, f.transcript, strings.Repeat("x", tc.transcript))
			}

			said := f.hookRun("prompt").stdout
			if strings.Contains(said, "carry-forward") != tc.nudges {
				t.Errorf("the nudge should fire only near compaction, got: %q", said)
			}
		})
	}
}

func TestNudgeWording(t *testing.T) {
	t.Run("the nudge carries the keep rule and the seat's file", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)

		said := f.hookRun("prompt").stdout
		for _, rule := range []string{"Uncaptured decisions", "leave untouched where nothing did", "agentic-carryforward refreshed", ".memory/carryforward/drive-memory.md"} {
			if !strings.Contains(said, rule) {
				t.Errorf("the nudge should carry %q, got: %s", rule, said)
			}
		}

		if f.state()["nudges"] != float64(1) {
			t.Error("the nudge should be counted")
		}
	})

	t.Run("the backstop repeats the rule", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.sensor(70, 0, 0)
		f.hookRun("prompt")
		f.hookRun("prompt")

		if !strings.Contains(f.hookRun("stop").stderr, "Uncaptured decisions") {
			t.Error("the backstop should carry the keep rule")
		}
	})

	t.Run("the nudge names the holding seat's file alone", func(t *testing.T) {
		f := newFixture(t)
		f.claim("idea", session)
		f.sensor(70, 0, 0)

		said := f.hookRun("prompt").stdout
		if !strings.Contains(said, ".memory/carryforward/idea-memory.md") || strings.Contains(said, "drive-memory.md") {
			t.Error("the nudge should name the holding seat's file")
		}
	})
}

func TestDeltaLog(t *testing.T) {
	t.Run("a secondary session writes nothing", func(t *testing.T) {
		f := newFixture(t)

		if f.hookRun("prompt").stdout != "" || f.hookRun("stop").code != 0 {
			t.Error("a seatless session should stay silent")
		}

		if exists(f.log()) || exists(f.path("transcripts", session+".state")) {
			t.Error("a seatless session should write nothing")
		}
	})

	t.Run("stop appends one turn block per call", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.appendTurn("first ask", "first reply", "Edit")

		if f.hookRun("stop").code != 0 {
			t.Fatal("stop should pass")
		}

		body, _ := os.ReadFile(f.log())
		for _, part := range []string{"## turn 1", "### asked", "first ask", "### replied", "first reply", "Edit /x/y.go"} {
			if !strings.Contains(string(body), part) {
				t.Errorf("the block should carry %q, got: %s", part, body)
			}
		}

		f.appendTurn("second ask", "second reply", "")
		f.hookRun("stop")

		body, _ = os.ReadFile(f.log())
		if strings.Count(string(body), "## turn ") != 2 || !strings.Contains(string(body), "## turn 2") || f.state()["turns"] != float64(2) {
			t.Errorf("a second stop should add a second block, got: %s", body)
		}
	})

	t.Run("a stop with no new records adds nothing", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.appendTurn("only ask", "only reply", "")
		f.hookRun("stop")
		first, _ := os.ReadFile(f.log())
		f.hookRun("stop")
		second, _ := os.ReadFile(f.log())

		if string(first) != string(second) {
			t.Error("an idle stop should add nothing")
		}
	})

	t.Run("a compaction serves the prompt file and the log", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		test.WriteFile(t, filepath.Join(f.env.Root, ".claude", "settings.local.md"), test.Fixture(t, "carryforward/settings.local.md"))
		f.appendTurn("what breaks the deploy", "the tenant role grants ingresses alone", "")
		f.hookRun("stop")

		if !strings.Contains(f.sourced("compact", "session-start").stdout, "After a compaction") {
			t.Error("the prompt file should be served after a compaction")
		}

		pieces := f.compactedLog()
		if !strings.Contains(pieces[0], "the tenant role grants ingresses alone") ||
			!strings.Contains(pieces[0], "Turns since the last carry-forward refresh") ||
			!strings.Contains(pieces[0], ".memory/transcripts/"+session+".md") ||
			strings.Contains(pieces[0], "earlier characters are in") {
			t.Errorf("the first piece should carry the whole log, got: %s", pieces[0])
		}

		if strings.Join(pieces[1:], "") != "" {
			t.Error("a short log should arrive in one piece")
		}
	})

	t.Run("every piece stays under what the client inlines", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		for index := range 40 {
			f.appendTurn("question "+strconv.Itoa(index), "answer "+strconv.Itoa(index)+" "+strings.Repeat("y", 1_500), "")
		}

		f.appendTurn("last", "the newest turn survives the cap", "")
		f.hookRun("stop")

		pieces := f.compactedLog()
		for _, piece := range pieces {
			if len([]rune(piece)) >= 10_000 {
				t.Errorf("a piece should stay under 10,000 characters, got %d", len([]rune(piece)))
			}
		}

		if !strings.Contains(pieces[4], "the newest turn survives the cap") || !strings.Contains(pieces[0], "earlier characters are in") {
			t.Error("the newest turns should survive and the withheld ones be pointed at")
		}
	})

	t.Run("a single long line is cut rather than overflowing", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.appendTurn("first", strings.Repeat("x", 200_000), "")
		f.appendTurn("last", "the newest turn survives the cap", "")
		f.hookRun("stop")

		pieces := f.compactedLog()
		for _, piece := range pieces {
			if len([]rune(piece)) >= 10_000 {
				t.Error("a long line should be cut to fit")
			}
		}

		if !strings.Contains(strings.Join(pieces, ""), "the newest turn survives the cap") {
			t.Error("the newest turn should survive")
		}
	})

	t.Run("the log arrives only after a compaction", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.appendTurn("ask", "reply", "")
		f.hookRun("stop")

		if f.sourced("startup", "session-log", "1").stdout != "" {
			t.Error("a fresh start should get no log")
		}
	})

	t.Run("an empty log serves nothing", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		test.WriteFile(t, f.log(), "")

		if strings.Join(f.compactedLog(), "") != "" {
			t.Error("an empty log should serve nothing")
		}
	})

	t.Run("a piece number is required", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)

		got := f.sourced("compact", "session-log")
		if got.code != 1 || !strings.Contains(got.stderr, "usage:") {
			t.Errorf("a missing piece number should be refused, got: %+v", got)
		}
	})
}

func TestSessionStart(t *testing.T) {
	t.Run("announces a session holding no seat", func(t *testing.T) {
		f := newFixture(t)

		said := f.sourced("startup", "session-start").stdout
		for _, part := range []string{"holds no role", "agentic-carryforward claim <role>", "Seats held: none"} {
			if !strings.Contains(said, part) {
				t.Errorf("the announcement should carry %q, got: %s", part, said)
			}
		}
	})

	t.Run("names the seats already held", func(t *testing.T) {
		f := newFixture(t)
		f.claim("finalize", "finalize-session")

		if !strings.Contains(f.sourced("startup", "session-start").stdout, "finalize (finalize)") {
			t.Error("a held seat should be named with its holder")
		}
	})

	t.Run("refreshes the recorded pid", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.deadLock("drive", session)
		f.sourced("resume", "session-start")

		record := f.lock("drive")
		if record["pid"] != float64(test.OwnerPID) || record["started"] == "irrelevant" {
			t.Errorf("the lock should follow the new process, got: %v", record)
		}
	})

	t.Run("forgets sessions of processes that are gone", func(t *testing.T) {
		f := newFixture(t)
		writeJSON(t, f.path("sessions.json"), map[string]string{strconv.Itoa(test.DeadPID): "gone"})
		f.sourced("startup", "session-start")

		known := readJSON(t, f.path("sessions.json"))
		if len(known) != 1 || known[strconv.Itoa(test.OwnerPID)] != session {
			t.Errorf("only live owner processes should be remembered, got: %v", known)
		}
	})
}

func TestSweep(t *testing.T) {
	aged := func(t *testing.T, path string, age time.Duration) {
		t.Helper()
		test.Age(t, path, age)
	}

	t.Run("reports a young orphan and keeps it", func(t *testing.T) {
		f := newFixture(t)
		orphan := f.path("transcripts", "aaaaaaaa-orphan.md")
		test.WriteFile(t, orphan, "## turn 1\n")

		got := f.run("", "sweep")
		if !strings.Contains(got.stdout, "aaaaaaaa") || !strings.Contains(got.stdout, "log holds unfolded turns") || !exists(orphan) {
			t.Errorf("a young orphan should be reported and kept, got: %s", got.stdout)
		}
	})

	t.Run("deletes an orphan past the age", func(t *testing.T) {
		f := newFixture(t)
		orphan := f.path("transcripts", "bbbbbbbb-orphan.md")
		test.WriteFile(t, orphan, "## turn 1\n")
		aged(t, orphan, 49*time.Hour)

		got := f.run("", "sweep")
		if !strings.Contains(got.stdout, "deleted") || exists(orphan) {
			t.Errorf("an old orphan should be deleted, got: %s", got.stdout)
		}
	})

	t.Run("never touches the live holder", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.appendTurn("ask", "reply", "")
		f.hookRun("stop")

		entries, _ := os.ReadDir(f.path("transcripts"))
		for _, entry := range entries {
			aged(t, f.path("transcripts", entry.Name()), 72*time.Hour)
		}

		f.run("", "sweep")

		if !exists(f.log()) {
			t.Error("the live holder's log should survive")
		}
	})

	t.Run("clears a dead seat while a live one stands", func(t *testing.T) {
		f := newFixture(t)
		f.claim("drive", session)
		f.deadLock("settle", "dead-settle")

		f.run("", "sweep")

		if exists(f.lockPath("settle")) || !exists(f.lockPath("drive")) {
			t.Error("only the dead seat should be freed")
		}
	})

	t.Run("an empty directory has nothing to sweep", func(t *testing.T) {
		f := newFixture(t)

		if !strings.Contains(f.run("", "sweep").stdout, "nothing to sweep") {
			t.Error("an empty sweep should say so")
		}
	})
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"bogus"}} {
		f := newFixture(t)

		got := f.run("", args...)
		if got.code != 1 || !strings.Contains(got.stderr, "usage: agentic-carryforward") {
			t.Errorf("%v should be refused with usage", args)
		}
	}
}
