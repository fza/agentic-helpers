package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestARenamedFdboxLibraryReadsAsIs(t *testing.T) {
	h := newHarness(t)
	fdbox := h.elsewhere(otherRepo)
	test.FixtureTree(t, "tools/fdbox-library", fdbox.store())

	if !strings.Contains(h.must("find", "--term", "markdown"), "word-count@format-d-fdbox  v2") {
		t.Error("another project should find a library holding no index yet, by its tool folders")
	}

	out := fdbox.must("list")
	if !strings.Contains(out, "word-count  Count the words of a markdown file, code blocks left out  [taken 2, last ") ||
		!strings.Contains(out, "moved-sentences  List sentences that moved") {
		t.Errorf("list should read the renamed library as it is:\n%s", out)
	}

	if !test.Exists(t, filepath.Join(fdbox.store(), "index.json")) {
		t.Error("the first read should build index.json")
	}

	if !strings.Contains(fdbox.must("find", "--query", "list sentences that moved between two revisions"), "moved-sentences") {
		t.Error("the first read should embed the descriptions the library carried no vector for")
	}

	if !strings.Contains(h.must("take", "moved-sentences"), "moved-sentences@format-d-fdbox/v1: List sentences") {
		t.Error("another project should take the library's tools")
	}

	if !strings.Contains(h.must("list"), "other projects: format-d-fdbox 2 tools; `agentic-tools find` searches them") {
		t.Error("list should count the other project's tools")
	}
}

func TestAMissingOrCorruptIndexIsRebuiltWithoutReEmbedding(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")

	embedded := h.embedder.Calls()
	index := filepath.Join(h.store(), "index.json")

	for name, damage := range map[string]func(){
		"missing":        func() { _ = os.Remove(index) },
		"corrupt":        func() { _ = os.WriteFile(index, []byte("{not json"), 0o600) },
		"another schema": func() { _ = os.WriteFile(index, []byte(`{"schema":99,"tools":{}}`), 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			damage()

			if !strings.Contains(h.must("find", "--query", "count the words of standard input"), "word-count") {
				t.Error("a damaged index should be rebuilt on read, vectors and all")
			}

			if len(h.indexed()) != 2 {
				t.Error("the rebuilt index should be written back")
			}
		})
	}

	if !strings.Contains(h.must("reindex"), "rebuilt index.json: 2 tools") {
		t.Error("reindex should rebuild the index")
	}

	queries := 3
	if h.embedder.Calls() != embedded+queries {
		t.Errorf("no rebuild should embed an unchanged description: %d embeds beyond the queries", h.embedder.Calls()-embedded-queries)
	}
}

func TestAStoreLockHeldElsewhereRefusesAWrite(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	lock, err := os.OpenFile(filepath.Join(h.store(), ".lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock: %v", err)
	}

	defer func() { _ = lock.Close() }()

	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}

	_ = lock.Truncate(0)
	_, _ = lock.WriteAt([]byte("4242\n"), 0)

	started := time.Now()

	h.refused("the store is locked by pid 4242", "update", "word-count", "--summary", "Count words faster", "--why", "x")

	if time.Since(started) > 2*time.Second {
		t.Error("a held lock should refuse within the wait")
	}
}

func TestAReaderFollowsNoLinkAndWaitsOnNoPipe(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")
	h.tool("word-split", "Put each word on its own line")
	h.tool("big-tool", "A tool grown past the read limit")

	outside := filepath.Join(h.home, "secret.txt")
	test.WriteFile(t, outside, "secret text\n")

	_ = os.Remove(filepath.Join(h.store(), "word-count", "v1", "word-count.py"))

	err := os.Symlink(outside, filepath.Join(h.store(), "word-count", "v1", "word-count.py"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	_ = os.Remove(filepath.Join(h.store(), "word-split", "current"))

	err = syscall.Mkfifo(filepath.Join(h.store(), "word-split", "current"), 0o600)
	if err != nil {
		t.Fatalf("planting the pipe: %v", err)
	}

	test.WriteFile(t, filepath.Join(h.store(), "big-tool", "v1", "big.bin"), strings.Repeat("x", 1<<20+64<<10+1))
	_ = os.Remove(filepath.Join(h.store(), "index.json"))

	done := make(chan string, 1)

	go func() { done <- h.must("reindex") }()

	select {
	case out := <-done:
		if !strings.Contains(out, "rebuilt index.json: 0 tools, 3 unreadable skipped") {
			t.Errorf("a link, a pipe and an oversized version should each be skipped, got %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reader should never wait on a pipe")
	}

	if strings.Contains(h.run("", "find", "--term", "secret").out, "word-count") {
		t.Error("a reader should never follow a link out of the store")
	}
}

func TestTheMCPToolsTakeExactlyTheirKeys(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"find","arguments":{"term":["words"],"Limit":2}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"take","arguments":{"names":["word-count"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"take","arguments":{"name":"word-count"}}}`,
	}

	out := h.must2(strings.Join(lines, "\n")+"\n", "mcp")

	answers := map[float64]string{}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var answer struct {
			ID float64 `json:"id"`
		}

		_ = json.Unmarshal([]byte(line), &answer)
		answers[answer.ID] = line
	}

	listed := answers[2]
	for _, verb := range []string{"find", "list", "take", "add", "update", "merge", "reindex"} {
		if !strings.Contains(listed, `"name":"`+verb+`"`) {
			t.Errorf("the server should serve %s", verb)
		}
	}

	if strings.Count(listed, `"additionalProperties":false`) != strings.Count(listed, `"inputSchema"`) {
		t.Error("every schema should take no key beyond its properties")
	}

	if !strings.Contains(answers[3], `find takes no key \"Limit\"; it takes again, limit, page, query, term`) {
		t.Errorf("a wrong-case key should be refused, got %s", answers[3])
	}

	if !strings.Contains(answers[4], `"summary":"Count the words`) || strings.Contains(answers[4], `"isError":true`) {
		t.Errorf("take should answer over MCP, got %s", answers[4])
	}

	if !strings.Contains(answers[5], `take takes no key \"name\"; it takes names`) {
		t.Errorf("take should refuse a key it does not list, got %s", answers[5])
	}
}

func (h *harness) must2(stdin string, args ...string) string {
	h.t.Helper()

	got := h.run(stdin, args...)
	if got.code != 0 {
		h.t.Fatalf("%v should succeed, exit %d: %s%s", args, got.code, got.out, got.err)
	}

	return got.out
}

func TestAWalkStopsAtItsDepthAndFileCaps(t *testing.T) {
	h := newHarness(t)
	h.tool("deep-tool", "A tool nested past the walk depth")
	h.tool("wide-tool", "A tool holding more files than a walk reads")
	h.tool("fine-tool", "A tool that reads as it should")

	test.WriteFile(t, filepath.Join(h.store(), "deep-tool", "v1", "a", "b", "c", "d", "e", "f", "g", "h.py"), "deep\n")

	for index := range 2001 {
		test.WriteFile(t, filepath.Join(h.store(), "wide-tool", "v1", "many", strings.Repeat("x", 1+index%9)+string(rune('a'+index%26))+
			"-"+time.Duration(index).String()), "")
	}

	_ = os.Remove(filepath.Join(h.store(), "index.json"))

	if !strings.Contains(h.must("reindex"), "rebuilt index.json: 1 tools, 2 unreadable skipped") {
		t.Error("a version past the depth or the file cap should be skipped, the rest read")
	}
}

func TestTakersLiftAToolAboveAnEqualOne(t *testing.T) {
	h := newHarness(t)
	h.tool("alpha-count", "Count markdown section words")
	h.tool("beta-count", "Count markdown section words", "--distinct", "another")

	taker := *h
	taker.session = test.OtherSession
	taker.must("take", "beta-count")

	out := h.must("find", "--query", "count markdown section words")
	if strings.Index(out, "beta-count") > strings.Index(out, "alpha-count") {
		t.Errorf("a tool other sessions took should rank above an equal one, got:\n%s", out)
	}
}

func TestAReaderRefusesALinkInsideTheStoreToo(t *testing.T) {
	h := newHarness(t)
	h.tool("file-link", "A tool whose file is a link to its sibling")
	h.tool("folder-link", "A tool whose version is a link to another folder")
	h.tool("fine-tool", "A tool that reads as it should")

	version := filepath.Join(h.store(), "file-link", "v1")
	test.WriteFile(t, filepath.Join(version, "sibling.txt"), "sibling\n")
	_ = os.Remove(filepath.Join(version, "file-link.py"))

	err := os.Symlink("sibling.txt", filepath.Join(version, "file-link.py"))
	if err != nil {
		t.Fatalf("planting the file link: %v", err)
	}

	folder := filepath.Join(h.store(), "folder-link")

	err = os.Rename(filepath.Join(folder, "v1"), filepath.Join(folder, "real"))
	if err == nil {
		err = os.Symlink("real", filepath.Join(folder, "v1"))
	}

	if err != nil {
		t.Fatalf("planting the folder link: %v", err)
	}

	_ = os.Remove(filepath.Join(h.store(), "index.json"))

	if !strings.Contains(h.must("reindex"), "rebuilt index.json: 1 tools, 2 unreadable skipped") {
		t.Error("a link should be refused even where it stays inside the store")
	}
}

func TestATamperedIndexRowIsDropped(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	index := filepath.Join(h.store(), "index.json")
	data, _ := os.ReadFile(index)
	tampered := strings.Replace(string(data), `"name":"word-count"`, `"name":"../../escape"`, 1)
	test.WriteFile(t, index, tampered)

	if strings.Contains(h.must("find", "--term", "words"), "escape") {
		t.Error("a row whose name is not its key should never be read")
	}
}

func TestATakeRefusesAVersionSwappedForALink(t *testing.T) {
	h := newHarness(t)
	h.tool("word-count", "Count the words of standard input")

	folder := filepath.Join(h.store(), "word-count")
	test.WriteFile(t, filepath.Join(folder, "planted", "secret.txt"), "secret\n")
	_ = os.RemoveAll(filepath.Join(folder, "v1"))

	err := os.Symlink("planted", filepath.Join(folder, "v1"))
	if err != nil {
		t.Fatalf("planting the link: %v", err)
	}

	got := h.run("", "take", "word-count")
	if got.code != 1 || test.Exists(t, filepath.Join(h.store(), "copies", "word-count", "v1", "secret.txt")) {
		t.Errorf("a take should refuse a version folder that is a link, got %d %q", got.code, got.out)
	}
}
