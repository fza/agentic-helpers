package memory_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/home"
	"github.com/fza/agentic-helpers/source/hooks/internal/space"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func (h *harness) limitSpace(options string) {
	h.t.Helper()

	test.WriteFile(h.t, h.settings, `{"pluginConfigs":{"agentic-helpers@agentic-helpers":{"options":{`+options+`}}}}`)
}

// sizedFile writes a file of size bytes, its first byte setting it apart.
func (h *harness) sizedFile(name string, first byte, size int) string {
	h.t.Helper()

	path := filepath.Join(h.t.TempDir(), name)
	test.WriteFile(h.t, path, string(first)+string(bytes.Repeat([]byte{'x'}, size-1)))

	return path
}

// toolsLedger is where the tool library's machine-wide ledger lives.
// toolsStore is a tool store of another project, whose ledger a case
// writes.
func (h *harness) toolsStore() string {
	return h.roots().Store("01a17249-ce50-7d47-bf5f-6f16ef31c669-other", home.Tools)
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	return lines[len(lines)-1]
}

func spaceLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "space left:") {
			return line
		}
	}

	return ""
}

func TestAttachmentsCountOnceAcrossTheMachine(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.limitSpace(`"attachment_max_mb":1,"attachments_total_mb":1`)

	other := otherProject(h)
	other.must("", "claim", "main")

	shared := h.sizedFile("shared.bin", 'a', 600<<10)
	h.file("Finding of the first project", "body", "--attach", shared)

	held := other.file("Finding of the second project", "body", "--attach", other.sizedFile("note.txt", 'n', 1<<10))

	got := other.run("", "attach", held, other.sizedFile("second.bin", 'b', 600<<10))
	if got.code != 1 || !strings.Contains(got.err,
		"attachments would hold 1.2 MB on this machine, past the 1 MB limit (attachments_total_mb); most held by "+held+" (main, 1.0 KB)") {
		t.Errorf("another project's files should count against the machine's limit, refused naming the size and the holders, got %d %q",
			got.code, got.err)
	}

	if got := other.run("", "attach", held, shared); got.code != 0 {
		t.Errorf("a file another project already stored should count once, got %d %q", got.code, got.err)
	}
}

func TestALedgerCannotWrapTheTotalPastTheLimit(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.limitSpace(`"attachment_max_mb":1,"attachments_total_mb":1`)

	test.WriteLedger(t, h.roots().Shared("attachments"),
		fmt.Sprintf(`[{"name":"a","counted":true,"bytes":%d},{"name":"b","counted":true,"bytes":%d}]`, int64(1)<<62,
			int64(1)<<62))

	got := h.run("body", "new", "--title", "Finding past the limit", "--topic", "gate", "--attach",
		h.sizedFile("log.txt", 'l', 1<<10))
	if got.code != 1 || !strings.Contains(got.err, "past the 1 MB limit (attachments_total_mb)") {
		t.Errorf("a total past the largest size should stay past the limit, never wrap below it, got %d %q",
			got.code, got.err)
	}
}

func TestANegativeLoggedSizeCannotDefeatTheLimit(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.limitSpace(`"attachment_max_mb":1,"attachments_total_mb":1`)

	held := h.file("Finding with a big log", "body", "--attach", h.sizedFile("first.bin", 'a', 600<<10))

	tail := filepath.Join(h.store(), "log", "tail.jnl")
	forged := false

	test.EditTail(t, tail, func(payload string) string {
		edited := strings.ReplaceAll(payload, fmt.Sprintf(`"size":%d`, 600<<10), fmt.Sprintf(`"size":%d`, int64(-1)<<40))
		forged = forged || edited != payload

		return edited
	})

	if !forged {
		t.Fatal("the log should record the attachment's size")
	}

	got := h.run("", "attach", held, h.sizedFile("second.bin", 'b', 600<<10))
	if got.code != 1 || !strings.Contains(got.err, "past the 1 MB limit (attachments_total_mb)") {
		t.Errorf("a size the log records below nothing should never shrink the machine's total, got %d %q",
			got.code, got.err)
	}

	h.must(hookPayload("startup", "", ""), "session-start")

	got = h.run("", "attach", held, h.sizedFile("second.bin", 'b', 600<<10))
	if got.code != 1 || !strings.Contains(got.err, "past the 1 MB limit (attachments_total_mb)") {
		t.Errorf("the SessionStart pass should keep a recorded size over one the log records below nothing, got %d %q",
			got.code, got.err)
	}
}

func TestTheAttachmentCapIsStatedBeforeAnAttach(t *testing.T) {
	h := newHarness(t)

	help := h.must("", "--help")
	if !strings.Contains(help, "at most attachment_max_mb") || !strings.Contains(help, "attachments_total_mb") {
		t.Errorf("--help should state the per-file cap and the machine-wide limit:\n%s", help)
	}

	lines := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	if listing := h.must(lines, "mcp"); !strings.Contains(listing, "never content; each within attachment_max_mb") {
		t.Errorf("the MCP new tool's attach key should state the per-file cap:\n%s", listing)
	}
}

func TestEveryAttachReplyEndsWithTheAttachmentsSpace(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.limitSpace(`"attachment_max_mb":1,"attachments_total_mb":1`)

	filed := h.must("body", "new", "--title", "Gate finding with its log", "--topic", "gate",
		"--attach", h.sizedFile("one.log", 'a', 1<<10))
	id := idPattern.FindString(filed)

	attached := h.must("", "attach", id, h.sizedFile("two.log", 'b', 1<<10))

	for name, got := range map[string]string{"new --attach": lastLine(filed), "attach": lastLine(attached)} {
		if !strings.HasPrefix(got, "space left: attachments ") || !strings.HasSuffix(got, " of 1.0 MB") {
			t.Errorf("the %s reply should end with the attachment store's space, got %q", name, got)
		}
	}

	if !strings.Contains(attached, "space left: attachments 1022.0 KB of 1.0 MB") {
		t.Errorf("the space left should be the limit less every recorded file, got %q", attached)
	}

	answer := h.mcp(`{"name":"new","arguments":{"title":"Filed with a file over MCP","body":"b","attach":["` +
		h.sizedFile("three.log", 'c', 1<<10) + `"]}}`)
	if !strings.Contains(answer, "space left: attachments 1021.0 KB of 1.0 MB") {
		t.Errorf("an MCP new attaching a file should carry the attachment store's space: %s", answer)
	}

	if plain := h.must("body", "new", "--title", "Gate finding without a file", "--topic", "gate"); strings.Contains(plain, "space left:") {
		t.Errorf("a new attaching nothing should say nothing of space, got %q", plain)
	}
}

func TestTheAttachmentSpaceComesFromRecordedSizes(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	h.limitSpace(`"attachment_max_mb":1,"attachments_total_mb":1`)

	id := h.file("Gate finding with its logs", "body", "--attach", h.sizedFile("one.log", 'a', 1<<10))

	planted := filepath.Join(h.roots().Shared("attachments"), strings.Repeat("d", 64))
	test.WriteFile(t, planted, strings.Repeat("y", 512<<10))

	if out := h.must("", "attach", id, h.sizedFile("two.log", 'b', 1<<10)); lastLine(out) != "space left: attachments 1022.0 KB of 1.0 MB" {
		t.Errorf("a file in the store no attach recorded should not count, got %q", lastLine(out))
	}

	ledger := filepath.Join(h.roots().Shared("attachments"), space.LedgerName)

	err := os.Remove(ledger)
	if err != nil {
		t.Fatal(err)
	}

	if got := h.run("", "attach", id, h.sizedFile("big.log", 'd', 1023<<10)); got.code != 1 {
		t.Errorf("the project's own files should count even where the ledger lacks them, got %d %q", got.code, got.out)
	}

	test.WriteLedger(t, h.roots().Shared("attachments"), test.Fixture(t, "memory/space/attachments-stale.json"))

	for _, legacy := range []string{".space.json", ".space.json.lock"} {
		test.WriteFile(t, filepath.Join(h.roots().Shared("attachments"), legacy), "{}")
	}

	h.must(hookPayload("startup", "", ""), "session-start")

	for _, legacy := range []string{".space.json", ".space.json.lock"} {
		if _, err := os.Lstat(filepath.Join(h.roots().Shared("attachments"), legacy)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the SessionStart pass should remove %s, which the ledger replaces", legacy)
		}
	}

	if out := h.must("", "attach", id, h.sizedFile("three.log", 'c', 1<<10)); lastLine(out) != "space left: attachments 1021.0 KB of 1.0 MB" {
		t.Errorf("the SessionStart pass should recompute the ledger from every log, dropping a file no longer stored, got %q",
			lastLine(out))
	}
}

func TestTheBriefNamesOnlyAStoreRunningLow(t *testing.T) {
	cases := []struct {
		name        string
		attachments int
		tools       string
		toolsOff    bool
		want        string
	}{
		{name: "both stores roomy"},
		{name: "prunable tool versions left out", tools: "memory/space/tools-prunable.json"},
		{name: "attachments low", attachments: 950 << 10, want: "space left: attachments 74.0 KB of 1.0 MB"},
		{name: "tools low", tools: "memory/space/tools-low.json", want: "space left: tools 20.0 MB of 500.0 MB"},
		{name: "tools low but switched off", tools: "memory/space/tools-low.json", toolsOff: true},
		{
			name: "both low", attachments: 950 << 10, tools: "memory/space/tools-low.json",
			want: "space left: attachments 74.0 KB of 1.0 MB, tools 20.0 MB of 500.0 MB",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.must("", "claim", "main")
			h.limitSpace(fmt.Sprintf(`"attachment_max_mb":1,"attachments_total_mb":1,"tools_enabled":%t`, !tc.toolsOff))

			if tc.attachments > 0 {
				h.file("Gate finding with a big log", "body", "--attach", h.sizedFile("big.log", 'a', tc.attachments))
			}

			if tc.tools != "" {
				test.WriteLedger(t, h.toolsStore(), test.Fixture(t, tc.tools))
			}

			if got := spaceLine(h.must("", "brief")); got != tc.want {
				t.Errorf("the brief should name a store only while under a tenth of its limit is left, got %q", got)
			}
		})
	}
}

func TestSessionStartPrunesTheToolLibrary(t *testing.T) {
	for _, toolsOn := range []bool{true, false} {
		t.Run(fmt.Sprintf("tools_enabled %t", toolsOn), func(t *testing.T) {
			h := newHarness(t)
			h.limitSpace(fmt.Sprintf(`"tools_enabled":%t`, toolsOn))
			test.WriteFile(t, h.config, "tools_total_mb: 1\n")

			store := h.roots().Store(test.OtherStore(t, h.roots(), "github.com/acme/tools"), home.Tools)
			test.FixtureTree(t, "tools/fdbox-library", store)
			test.WriteFile(t, filepath.Join(store, "word-count", "v1", "data.txt"), strings.Repeat("a", 700<<10))
			test.WriteFile(t, filepath.Join(store, "word-count", "v2", "data.txt"), strings.Repeat("b", 700<<10))

			h.must(hookPayload("startup", "", ""), "session-start")

			if _, err := os.Stat(filepath.Join(store, "word-count", "v1")); (err == nil) == toolsOn {
				t.Error("the SessionStart pass should prune the oldest prunable tool version past tools_total_mb, " +
					"and only while agentic-tools is on")
			}

			if _, err := os.Stat(filepath.Join(store, "word-count", "v2")); err != nil {
				t.Error("the SessionStart pass should never prune a current tool version")
			}
		})
	}
}

func TestTheViewListsAttachedEntriesBySize(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	smaller := h.file("Gate finding with two logs", "body", "--attach", h.sizedFile("one.log", 'a', 2<<10),
		"--attach", h.sizedFile("two.log", 'b', 1<<10))
	larger := h.file("Gate finding with a big log", "body", "--attach", h.sizedFile("big.log", 'c', 5<<10))
	bare := h.file("Gate finding with no file", "body")

	out := h.must("", "view", "--layout", "active:attached:rank(size):as-list")

	switch {
	case strings.Contains(out, bare):
		t.Errorf("attached should keep only entries holding attachments:\n%s", out)
	case !strings.Contains(out, "  attached: big.log (5.0 KB); total 5.0 KB"),
		!strings.Contains(out, "  attached: one.log (2.0 KB), two.log (1.0 KB); total 3.0 KB"):
		t.Errorf("the list should show each file's name and size and the entry's total:\n%s", out)
	case strings.Index(out, larger) > strings.Index(out, smaller):
		t.Errorf("rank(size) should list the most attachment bytes first:\n%s", out)
	}

	if ranked := h.must("", "view", "--layout", "active:rank(size):as-list"); !strings.Contains(ranked,
		"  attached: big.log (5.0 KB); total 5.0 KB") {
		t.Errorf("ranking by size alone should show the sizes it ranks by:\n%s", ranked)
	}

	if filtered := h.must("", "view", "--layout", "active:attached:as-list"); !strings.Contains(filtered,
		"  attached: big.log (5.0 KB); total 5.0 KB") {
		t.Errorf("filtering by attached alone should show what each entry holds:\n%s", filtered)
	}

	if plain := h.must("", "view", "--layout", "active:as-list"); strings.Contains(plain, "attached:") {
		t.Errorf("a list neither filtering by attached nor ranking by size should show no sizes:\n%s", plain)
	}
}
