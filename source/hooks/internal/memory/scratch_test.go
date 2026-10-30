package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/scratch"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// scratchProject is a harness whose checkout carries `.tmp/`, so seats get
// scratch folders.
func scratchProject(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	test.WriteFile(t, filepath.Join(h.repo, ".tmp", ".keep"), "")

	return h
}

func (h *harness) seatScratch(name string) string {
	return scratch.SeatDir(h.repo, name)
}

func startPayload(session string, transcript string) string {
	data, _ := json.Marshal(map[string]string{"session_id": session, "source": "startup", "transcript_path": transcript})

	return string(data)
}

func TestAClaimMakesTheSeatsMarkedScratchFolder(t *testing.T) {
	h := scratchProject(t)

	out := h.must("", "claim", "main")
	if !strings.Contains(out, "\nscratch: .tmp/claude/main/\n") {
		t.Errorf("the claim should name the seat's scratch folder, got:\n%s", out)
	}

	marker, err := os.ReadFile(filepath.Join(h.seatScratch("main"), scratch.Marker))
	if err != nil || strings.TrimSpace(string(marker)) != seatID(t, h.store(), "main") {
		t.Errorf("the folder should be marked with the seat's id, got %q, %v", marker, err)
	}
}

func TestAClaimGivesAGitCheckoutItsTmpFolder(t *testing.T) {
	h := newHarness(t)

	out := h.must("", "claim", "main")
	if !strings.Contains(out, "\nscratch: .tmp/claude/main/\n") || !test.Exists(t, filepath.Join(h.seatScratch("main"), scratch.Marker)) {
		t.Errorf("a claim in a git checkout without .tmp/ should create it with the seat's folder, got:\n%s", out)
	}

	if !strings.Contains(out, "agentic-memory: added .tmp/ to ") {
		t.Errorf("the claim should keep the new .tmp/ out of git, got:\n%s", out)
	}
}

func TestNoClaimCreatesTmpWithScratchOffOrOutsideGit(t *testing.T) {
	cases := map[string]func(h *harness){
		"scratch off": func(h *harness) {
			test.WriteFile(h.t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"scratch_enabled":false}}}}`)
		},
		"outside git": func(h *harness) {
			test.WriteFile(h.t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"memory_require_git":false}}}}`)
			_ = os.RemoveAll(filepath.Join(h.repo, ".git"))
			h.git.OutsideWorkTree = true
		},
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			setup(h)

			if strings.Contains(h.must("", "claim", "main"), "scratch:") || test.Exists(t, filepath.Join(h.repo, ".tmp")) {
				t.Error("no .tmp/ should be created")
			}
		})
	}
}

func TestScratchSweepDecidesWhenTheSeatsFolderGoes(t *testing.T) {
	cases := []struct {
		mode        string
		releaseGone bool
		endGone     bool
	}{
		{mode: "", releaseGone: true},
		{mode: "release", releaseGone: true},
		{mode: "session", endGone: true},
		{mode: "idle"},
	}

	for _, tc := range cases {
		t.Run("mode "+tc.mode, func(t *testing.T) {
			h := scratchProject(t)
			if tc.mode != "" {
				test.WriteFile(t, h.config, "scratch_sweep: "+tc.mode+"\n")
			}

			h.must("", "claim", "main")
			test.WriteFile(t, filepath.Join(h.seatScratch("main"), "probe.py"), "print(1)\n")
			h.must("", "release")

			if test.Exists(t, h.seatScratch("main")) == tc.releaseGone {
				t.Errorf("a release should remove the folder only where scratch_sweep says so")
			}

			h.must("", "claim", "main")
			test.WriteFile(t, filepath.Join(h.seatScratch("main"), "probe.py"), "print(1)\n")
			h.must(h.sessionEnd("prompt_input_exit"), "session-end")

			if test.Exists(t, h.seatScratch("main")) == tc.endGone {
				t.Errorf("a session end should remove the folder only where scratch_sweep says so")
			}
		})
	}
}

func TestAClearKeepsTheSeatsFolderUnderTheSessionSweep(t *testing.T) {
	h := scratchProject(t)
	test.WriteFile(t, h.config, "scratch_sweep: session\n")
	h.must("", "claim", "main")

	h.must(h.sessionEnd("clear"), "session-end")

	if !test.Exists(t, h.seatScratch("main")) {
		t.Error("a /clear keeps the seat, and its folder with it")
	}
}

func TestAKeepAtTheSeatsNameIsNeverSwept(t *testing.T) {
	h := scratchProject(t)
	keep := filepath.Join(h.seatScratch("main"), "keep.sh")
	test.WriteFile(t, keep, "echo keep\n")

	if !strings.Contains(h.must("", "claim", "main"), "scratch: .tmp/claude/main/ (a keep, never swept)") {
		t.Error("the claim should name a folder it did not make as a keep")
	}

	h.must("", "release")

	if !test.Exists(t, keep) || test.Exists(t, filepath.Join(h.seatScratch("main"), scratch.Marker)) {
		t.Error("a keep should survive the release unmarked")
	}
}

func TestARenamedSeatsFolderMovesWithIt(t *testing.T) {
	h := scratchProject(t)
	h.must("", "claim", "main")
	test.WriteFile(t, filepath.Join(h.seatScratch("main"), "probe.py"), "print(1)\n")

	h.must("", "rename", "lead")

	if test.Exists(t, h.seatScratch("main")) || !test.Exists(t, filepath.Join(h.seatScratch("lead"), "probe.py")) {
		t.Error("a rename should carry the seat's folder to the new name")
	}
}

func TestTheSessionStartReapsAWeekIdleSeatFolder(t *testing.T) {
	h := scratchProject(t)
	h.must("", "claim", "main")

	probe := filepath.Join(h.seatScratch("main"), "probe.py")
	test.WriteFile(t, probe, "print(1)\n")

	h.must(unrelatedStart(), "session-start")

	if !test.Exists(t, probe) {
		t.Error("a folder used within the week should stay")
	}

	for _, path := range []string{probe, filepath.Join(h.seatScratch("main"), scratch.Marker), h.seatScratch("main")} {
		test.Age(t, path, 8*24*time.Hour)
	}

	h.must(unrelatedStart(), "session-start")

	if test.Exists(t, h.seatScratch("main")) {
		t.Error("a seat folder idle a week should be reaped at session start")
	}
}

func TestAnOldSessionFolderMovesIntoItsSeatsFolder(t *testing.T) {
	h := scratchProject(t)
	h.must("", "claim", "main")

	held := filepath.Join(h.repo, ".tmp", "claude", test.Session, "probe.py")
	test.WriteFile(t, held, "print(1)\n")

	h.must(unrelatedStart(), "session-start")

	if test.Exists(t, filepath.Dir(held)) || !test.Exists(t, filepath.Join(h.seatScratch("main"), "probe.py")) {
		t.Error("the session folder of a seat's claim should move into the seat's folder")
	}
}

func TestATranscriptNamesTheSeatOfAnOlderSessionFolder(t *testing.T) {
	cases := map[string]string{
		"a shell claim":        `{"input":{"command":"agentic-memory claim main"}}`,
		"an MCP claim":         `{"name":"mcp__plugin_agentic-helpers_memory__claim","input":{"name":"main"}}`,
		"a session start":      `{"content":"agentic-memory: seat main, 3 live, 0 retired"}`,
		"no seat in it at all": `{"content":"nothing here"}`,
	}

	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			h := scratchProject(t)
			h.must("", "claim", "main")

			const older = "f00dbabe-0000-4000-8000-000000000002"
			const session = "a1b2c3d4-0000-4000-8000-000000000001"

			transcripts := filepath.Join(h.home, "projects", "repo")
			test.WriteFile(t, filepath.Join(transcripts, session+".jsonl"), line+"\n")
			held := filepath.Join(h.repo, ".tmp", "claude", session, "notes.md")
			test.WriteFile(t, held, "notes\n")

			h.must(startPayload(older, filepath.Join(transcripts, older+".jsonl")), "session-start")

			moved := test.Exists(t, filepath.Join(h.seatScratch("main"), "notes.md"))
			if moved != (name != "no seat in it at all") || moved == test.Exists(t, held) {
				t.Errorf("a session folder should move exactly where its transcript names the seat, moved %v", moved)
			}
		})
	}
}

func TestTheCompactBriefNamesTheSeatsFolder(t *testing.T) {
	h := scratchProject(t)
	h.must("", "claim", "main")

	if !strings.Contains(h.must(hookPayload("compact", "", ""), "session-start"), "scratch: .tmp/claude/main/") {
		t.Error("the brief after a compaction should name the seat's scratch folder")
	}
}

func TestScratchOffLeavesTheSeatWithoutAFolder(t *testing.T) {
	h := scratchProject(t)
	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"scratch_enabled":false}}}}`)

	if strings.Contains(h.must("", "claim", "main"), "scratch:") || test.Exists(t, h.seatScratch("main")) {
		t.Error("with scratch off a claim should make no folder")
	}
}

func TestAHandoverCarriesTheSeatsFolderToTheNewSeat(t *testing.T) {
	board := scratchProject(t)
	job := newHarnessSharing(board)
	job.must("", "claim", "impl-7ah", "--shelf", "row-7ah", "--lifetime", "0")
	test.WriteFile(t, filepath.Join(job.seatScratch("impl-7ah"), "probe.py"), "print(1)\n")

	board.must("", "claim", "impl-8bq", "--session", job.session, "--shelf", "row-8bq")

	moved := filepath.Join(board.seatScratch("impl-8bq"), "probe.py")
	if test.Exists(t, board.seatScratch("impl-7ah")) || !test.Exists(t, moved) {
		t.Error("a handover should carry the seat's folder to the seat the session moved to")
	}

	marker, _ := os.ReadFile(filepath.Join(board.seatScratch("impl-8bq"), scratch.Marker))
	if strings.TrimSpace(string(marker)) != seatID(t, board.store(), "impl-8bq") {
		t.Errorf("the carried folder should be marked for the new seat, got %q", marker)
	}
}

func TestScratchTurnedOffLeavesEveryFolderAlone(t *testing.T) {
	h := scratchProject(t)
	h.must("", "claim", "main")

	probe := filepath.Join(h.seatScratch("main"), "probe.py")
	test.WriteFile(t, probe, "print(1)\n")
	held := filepath.Join(h.repo, ".tmp", "claude", test.Session, "notes.md")
	test.WriteFile(t, held, "notes\n")

	for _, path := range []string{probe, filepath.Join(h.seatScratch("main"), scratch.Marker), h.seatScratch("main")} {
		test.Age(t, path, 8*24*time.Hour)
	}

	test.WriteFile(t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{"scratch_enabled":false}}}}`)

	h.must(unrelatedStart(), "session-start")
	h.must("", "release")

	if !test.Exists(t, probe) || !test.Exists(t, held) {
		t.Error("with scratch off nothing should be reaped, moved or swept")
	}
}
