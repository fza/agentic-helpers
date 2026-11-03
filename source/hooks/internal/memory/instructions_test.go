package memory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) instructionsLoaded(path string) string {
	data, _ := json.Marshal(map[string]string{
		"session_id": h.session, "hook_event_name": "InstructionsLoaded", "cwd": h.repo, "file_path": path,
		"memory_type": "Project", "load_reason": "session_start",
	})

	return string(data)
}

func (h *harness) promptOf(prompt string) string {
	data, _ := json.Marshal(map[string]string{"session_id": h.session, "prompt": prompt})

	return string(data)
}

func (h *harness) editCall(tool string, path string) string {
	data, _ := json.Marshal(map[string]any{
		"session_id": h.session, "tool_name": tool, "tool_input": map[string]string{"file_path": path},
	})

	return string(data)
}

// seatlessBeside is a second session on h's store holding no seat.
func seatlessBeside(t *testing.T) (*harness, *harness) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")

	return h, newHarnessSharing(h)
}

func rereadLine(path string) string {
	return "agentic-memory: " + path + " changed since loaded; re-read it"
}

func TestALoadedInstructionFileChangedElsewhereIsToldOnce(t *testing.T) {
	for _, hook := range []string{"prompt", "tool"} {
		t.Run(hook, func(t *testing.T) {
			_, other := seatlessBeside(t)
			path := filepath.Join(other.repo, "CLAUDE.md")
			test.WriteFile(t, path, "# rules\n")
			other.must(other.instructionsLoaded(path), "instructions")

			payload := other.promptOf("go on")
			read := func(out string) string { return out }

			if hook == "tool" {
				payload = other.toolCall()
				read = toolContext
			}

			if got := read(other.must(payload, hook)); strings.Contains(got, "changed since loaded") {
				t.Errorf("an unchanged instruction file should go unmentioned, got %q", got)
			}

			test.WriteFile(t, path, "# rules\n- never push\n")

			if got := read(other.must(payload, hook)); !strings.Contains(got, rereadLine("CLAUDE.md")) {
				t.Errorf("a seatless session should be told to re-read a changed instruction file, got %q", got)
			}

			if got := read(other.must(payload, hook)); strings.Contains(got, "changed since loaded") {
				t.Errorf("a change should be told once, got %q", got)
			}
		})
	}
}

func TestTheSessionsOwnEditIsTakenInSilently(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		t.Run(tool, func(t *testing.T) {
			_, other := seatlessBeside(t)
			path := filepath.Join(other.repo, "CLAUDE.local.md")
			test.WriteFile(t, path, "# local\n")
			other.must(other.instructionsLoaded(path), "instructions")

			test.WriteFile(t, path, "# local\n- push freely\n")

			if got := toolContext(other.must(other.editCall(tool, path), "tool")); strings.Contains(got, "changed since loaded") {
				t.Errorf("the session's own edit should not be announced to it, got %q", got)
			}

			if got := other.said(other.promptOf("next"), "prompt"); strings.Contains(got, "changed since loaded") {
				t.Errorf("the own edit should leave nothing to tell later, got %q", got)
			}
		})
	}
}

func TestAnEditOfAnotherFileDoesNotSilenceTheChange(t *testing.T) {
	_, other := seatlessBeside(t)
	path := filepath.Join(other.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")
	other.must(other.instructionsLoaded(path), "instructions")

	test.WriteFile(t, path, "# rules, changed\n")

	got := toolContext(other.must(other.editCall("Edit", filepath.Join(other.repo, "README.md")), "tool"))
	if !strings.Contains(got, rereadLine("CLAUDE.md")) {
		t.Errorf("an edit of another file should still let the change be told, got %q", got)
	}
}

func TestAReadOfTheChangedFileIsNoOwnChange(t *testing.T) {
	_, other := seatlessBeside(t)
	path := filepath.Join(other.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")
	other.must(other.instructionsLoaded(path), "instructions")

	test.WriteFile(t, path, "# rules, changed\n")

	if got := toolContext(other.must(other.editCall("Read", path), "tool")); !strings.Contains(got, rereadLine("CLAUDE.md")) {
		t.Errorf("only an Edit, Write or NotebookEdit should count as the session's own change, got %q", got)
	}
}

func TestANestedFileIsWatchedOnlyByTheSessionThatLoadedIt(t *testing.T) {
	h, other := seatlessBeside(t)
	root := filepath.Join(h.repo, "CLAUDE.md")
	nested := filepath.Join(h.repo, "internal", "CLAUDE.md")
	test.WriteFile(t, root, "# rules\n")
	test.WriteFile(t, nested, "# internal rules\n")

	h.must(h.instructionsLoaded(root), "instructions")
	h.must(h.instructionsLoaded(nested), "instructions")
	other.must(other.instructionsLoaded(root), "instructions")

	test.WriteFile(t, nested, "# internal rules, changed\n")

	if got := h.said(h.promptOf("go on"), "prompt"); !strings.Contains(got, rereadLine(filepath.Join("internal", "CLAUDE.md"))) {
		t.Errorf("the session that loaded the nested file should be told it changed, got %q", got)
	}

	if got := other.said(other.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("a session that never loaded the nested file should not hear of it, got %q", got)
	}
}

func TestARemovedInstructionFileIsToldOnce(t *testing.T) {
	_, other := seatlessBeside(t)
	path := filepath.Join(other.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")
	other.must(other.instructionsLoaded(path), "instructions")

	err := os.Remove(path)
	if err != nil {
		t.Fatalf("removing the instruction file: %v", err)
	}

	if got := other.said(other.promptOf("go on"), "prompt"); !strings.Contains(got, rereadLine("CLAUDE.md")) {
		t.Errorf("a removed instruction file should count as changed, got %q", got)
	}

	if got := other.said(other.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("a removal should be told once, got %q", got)
	}
}

func TestWithoutTheEventTheFixedSetIsWatched(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	agents := filepath.Join(h.repo, "AGENTS.md")
	fenced := filepath.Join(h.repo, "fenced.md")
	spanned := filepath.Join(h.repo, "spanned.md")
	local := filepath.Join(h.repo, "CLAUDE.local.md")
	user := filepath.Join(h.home, ".claude", "CLAUDE.md")
	nested := filepath.Join(h.repo, "internal", "CLAUDE.md")

	test.WriteFile(t, filepath.Join(h.repo, "CLAUDE.md"), "@AGENTS.md\n\nNot `run @spanned.md now`.\n\n```\n@fenced.md\n```\n")
	test.WriteFile(t, agents, "# agents\n")
	test.WriteFile(t, fenced, "# fenced\n")
	test.WriteFile(t, spanned, "# spanned\n")
	test.WriteFile(t, local, "# local\n")
	test.WriteFile(t, user, "# user\n")
	test.WriteFile(t, nested, "# nested\n")

	if got := h.said(h.promptOf("start"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("the first prompt should only take in the fixed set, got %q", got)
	}

	for _, path := range []string{agents, fenced, spanned, local, user, nested} {
		test.WriteFile(t, path, "# changed\n")
	}

	got := h.said(h.promptOf("go on"), "prompt")

	for _, told := range []string{"AGENTS.md", "CLAUDE.local.md", "~/.claude/CLAUDE.md"} {
		if !strings.Contains(got, rereadLine(told)) {
			t.Errorf("a changed file of the fixed set should be told, missing %s in %q", told, got)
		}
	}

	for _, quiet := range []string{"fenced.md", "spanned.md", filepath.Join("internal", "CLAUDE.md")} {
		if strings.Contains(got, rereadLine(quiet)) {
			t.Errorf("a file outside the fixed set should go untold, got %s in %q", quiet, got)
		}
	}
}

func TestASessionEndDropsTheSessionsRecord(t *testing.T) {
	_, other := seatlessBeside(t)
	path := filepath.Join(other.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")
	other.must(other.instructionsLoaded(path), "instructions")

	other.must(other.promptOf(""), "session-end")
	test.WriteFile(t, path, "# rules, changed\n")

	if got := other.said(other.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("an ended session's record should be gone, the next prompt taking in the files afresh, got %q", got)
	}
}

func TestTheFixedSetHoldsOnlyFilesThere(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.said(h.promptOf("start"), "prompt")

	test.WriteFile(t, filepath.Join(h.repo, "CLAUDE.local.md"), "# local\n")

	if got := h.said(h.promptOf("go on"), "prompt"); strings.Contains(got, "changed since loaded") {
		t.Errorf("a file created after the set was taken in was never loaded and should go untold, got %q", got)
	}
}

func TestInstructionsStaySilentWithoutAStore(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(h.repo, "CLAUDE.md")
	test.WriteFile(t, path, "# rules\n")

	got := h.run(h.instructionsLoaded(path), "instructions")
	if got.code != 0 || got.out != "" || got.err != "" {
		t.Errorf("the instructions hook should say nothing without a store, got %+v", got)
	}

	test.WriteFile(t, path, "# rules, changed\n")

	got = h.run(h.promptOf("go on"), "prompt")
	if got.code != 0 || got.out != "" || got.err != "" {
		t.Errorf("a prompt should say nothing without a store, got %+v", got)
	}

	if test.Exists(t, h.roots().Data) || test.Exists(t, h.roots().Cache) {
		t.Error("nothing should be written without a store")
	}
}
