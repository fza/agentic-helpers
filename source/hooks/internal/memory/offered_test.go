package memory_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// offeredStores are the stores around a code repository whose split layout
// asks keep or merge: its own, its graph repository's, and an unrelated
// project's, which no question offers.
type offeredStores struct {
	own, graph, other string
}

// splitQuestion builds a code repository working on a store of its own whose
// `.sdd/` then names a graph repository on another store, so the open
// question offers both, and an unrelated project beside them.
func splitQuestion(t *testing.T) (*harness, offeredStores) {
	t.Helper()

	h := newHarness(t)
	h.must("", "claim", "main")

	graph := newHarnessSharing(h)
	graph.repo = filepath.Join(filepath.Dir(h.repo), "mika-sdd")
	test.WriteFile(t, filepath.Join(graph.repo, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/graph-repo.yaml"))
	test.WriteFile(t, filepath.Join(graph.repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	graph.git = &test.Git{Top: graph.repo, Commit: "15af1a8c", Branch: "main"}
	graph.must("", "claim", "main")

	other := otherProject(h)
	other.must("", "claim", "main")

	test.WriteFile(t, filepath.Join(h.repo, ".sdd", "config.yaml"), test.Fixture(t, "sddconfig/project-dir.yaml"))

	stores := offeredStores{own: h.storeID(), graph: graph.storeID(), other: other.storeID()}

	if got := h.run(unrelatedStart(), "session-start"); !strings.Contains(got.out, "keep their memory in two stores") {
		t.Fatalf("the split layout should ask keep or merge, got %q", got.out)
	}

	return h, stores
}

func TestAnAgentBindsAndMergesOnlyOfferedStores(t *testing.T) {
	cases := []struct {
		name     string
		terminal memory.Terminal
		// outsideClaude runs the call from a shell no claude process
		// started; unset, it runs under one, as an agent's call does.
		outsideClaude bool
		args          func(stores offeredStores) []string
		says          func(h *harness, stores offeredStores) string
		// refused names the store the refusal names and the verb it says to
		// run in a terminal; empty where the call runs.
		refused func(stores offeredStores) (string, string)
	}{
		{
			name: "bind to the graph store the question offers",
			args: func(stores offeredStores) []string { return []string{"bind", stores.graph} },
			says: func(h *harness, stores offeredStores) string {
				return "bound " + h.repo + " to the store " + stores.graph
			},
		},
		{
			name: "bind to the own store the question offers",
			args: func(stores offeredStores) []string { return []string{"bind", stores.own} },
			says: func(h *harness, stores offeredStores) string {
				return "bound " + h.repo + " to the store " + stores.own
			},
		},
		{
			name: "bind to a new store",
			args: func(offeredStores) []string { return []string{"bind", "--new"} },
			says: func(h *harness, _ offeredStores) string { return "bound " + h.repo + " to the store " },
		},
		{
			name:    "bind to a store no question offers",
			args:    func(stores offeredStores) []string { return []string{"bind", stores.other} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "bind" },
		},
		{
			name:     "bind from the user's shell with no terminal to a store no question offers",
			terminal: test.Terminal(false), outsideClaude: true,
			args:    func(stores offeredStores) []string { return []string{"bind", stores.other} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "bind" },
		},
		{
			name:     "merge from the user's shell with no terminal an unoffered store into the own",
			terminal: test.Terminal(false), outsideClaude: true,
			args:    func(stores offeredStores) []string { return []string{"merge", stores.other, stores.own} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:     "bind from a terminal under claude to a store no question offers",
			terminal: test.Terminal(true),
			args:     func(stores offeredStores) []string { return []string{"bind", stores.other} },
			refused:  func(stores offeredStores) (string, string) { return stores.other, "bind" },
		},
		{
			name:     "merge from a terminal under claude an unoffered store into the own",
			terminal: test.Terminal(true),
			args:     func(stores offeredStores) []string { return []string{"merge", stores.other, stores.own} },
			refused:  func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:     "bind from a terminal at the user's shell to a store no question offers",
			terminal: test.Terminal(true), outsideClaude: true,
			args: func(stores offeredStores) []string { return []string{"bind", stores.other} },
			says: func(h *harness, stores offeredStores) string {
				return "bound " + h.repo + " to the store " + stores.other
			},
		},
		{
			name: "merge the offered graph store into the own",
			args: func(stores offeredStores) []string { return []string{"merge", stores.graph, stores.own} },
			says: func(_ *harness, stores offeredStores) string {
				return "joined the store " + stores.graph + " into " + stores.own
			},
		},
		{
			name: "merge the own store into the offered graph store",
			args: func(stores offeredStores) []string { return []string{"merge", stores.own, stores.graph} },
			says: func(_ *harness, stores offeredStores) string {
				return "joined the store " + stores.own + " into " + stores.graph
			},
		},
		{
			name:    "merge an unoffered store into the own",
			args:    func(stores offeredStores) []string { return []string{"merge", stores.other, stores.own} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:    "merge the own store into an unoffered one",
			args:    func(stores offeredStores) []string { return []string{"merge", stores.own, stores.other} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:    "merge an offered store with an unoffered one, the own store on neither side",
			args:    func(stores offeredStores) []string { return []string{"merge", stores.graph, stores.other} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:    "merge an unoffered store into an offered one, the own store on neither side",
			args:    func(stores offeredStores) []string { return []string{"merge", stores.other, stores.graph} },
			refused: func(stores offeredStores) (string, string) { return stores.other, "merge" },
		},
		{
			name:     "merge from a terminal at the user's shell an unoffered store into the own",
			terminal: test.Terminal(true), outsideClaude: true,
			args: func(stores offeredStores) []string { return []string{"merge", stores.other, stores.own} },
			says: func(_ *harness, stores offeredStores) string {
				return "joined the store " + stores.other + " into " + stores.own
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, stores := splitQuestion(t)
			h.terminal = tc.terminal

			if tc.outsideClaude {
				h.processes = test.NewProcessesOutsideClaude()
			}

			before := test.Tree(t, h.roots().Data) + test.Tree(t, filepath.Join(h.repo, ".git"))
			got := h.run("", tc.args(stores)...)

			if tc.refused == nil {
				if got.code != 0 || !strings.Contains(got.out, tc.says(h, stores)) {
					t.Errorf("the call should run, got %d %q %q", got.code, got.out, got.err)
				}

				return
			}

			store, verb := tc.refused(stores)
			line := "agentic-memory: " + store + " is not offered for this checkout; run the " + verb + " in a terminal\n"

			if got.code != 1 || got.err != line {
				t.Errorf("the call should be refused naming the store no question offers, got %d %q", got.code, got.err)
			}

			if test.Tree(t, h.roots().Data)+test.Tree(t, filepath.Join(h.repo, ".git")) != before {
				t.Error("a refused call should write nothing")
			}
		})
	}
}

func TestWithNoQuestionAnAgentBindsOnlyANewStore(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")
	first := h.storeID()

	other := otherProject(h)
	other.must("", "claim", "main")
	own := other.storeID()

	cases := []struct {
		name string
		args []string
		line string
	}{
		{
			name: "bind to another project's store",
			args: []string{"bind", first},
			line: "agentic-memory: " + first + " is not offered for this checkout; run the bind in a terminal\n",
		},
		{
			name: "merge the own store into another project's",
			args: []string{"merge", own, first},
			line: "agentic-memory: " + first + " is not offered for this checkout; run the merge in a terminal\n",
		},
		{
			name: "merge another project's store into the own",
			args: []string{"merge", first, own},
			line: "agentic-memory: " + first + " is not offered for this checkout; run the merge in a terminal\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := other.run("", tc.args...); got.code != 1 || got.err != tc.line {
				t.Errorf("a call no question offers should be refused naming the other project's store, got %d %q",
					got.code, got.err)
			}
		})
	}

	if got := other.must("", "bind", "--new"); !strings.Contains(got, "bound "+other.repo+" to the store ") {
		t.Errorf("bind --new should run with no question standing, got %q", got)
	}
}

func TestTheMCPServerRefusesAnUnofferedBindOrMerge(t *testing.T) {
	h, stores := splitQuestion(t)
	h.terminal = test.Terminal(true)
	h.processes = test.NewProcessesOutsideClaude()

	cases := []struct {
		name string
		call string
		says string
	}{
		{
			name: "bind to a store no question offers",
			call: `{"name":"bind","arguments":{"target":"` + stores.other + `"}}`,
			says: "agentic-memory: " + stores.other + " is not offered for this checkout; run the bind in a terminal",
		},
		{
			name: "merge a store no question offers",
			call: `{"name":"merge","arguments":{"from_topic":"` + stores.other + `","into":"` + stores.own + `"}}`,
			says: "agentic-memory: " + stores.other + " is not offered for this checkout; run the merge in a terminal",
		},
		{
			name: "bind to an offered store",
			call: `{"name":"bind","arguments":{"target":"` + stores.graph + `"}}`,
			says: "agentic-memory: bind is no MCP tool; run it through Bash: agentic-memory bind ",
		},
		{
			name: "merge a store into a topic",
			call: `{"name":"merge","arguments":{"from_topic":"` + stores.other + `","into":"gates"}}`,
			says: "agentic-memory: merge is no MCP tool; run it through Bash: agentic-memory merge ",
		},
		{
			name: "merge a topic into a store",
			call: `{"name":"merge","arguments":{"from_topic":"gate","into":"` + stores.other + `"}}`,
			says: "agentic-memory: merge is no MCP tool; run it through Bash: agentic-memory merge ",
		},
		{
			name: "merge topics",
			call: `{"name":"merge","arguments":{"from_topic":"gate","into":"gates"}}`,
			says: "agentic-memory: merge is no MCP tool; run it through Bash: agentic-memory merge ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := test.Tree(t, h.roots().Data)

			answer := h.mcp(tc.call)
			if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, tc.says) {
				t.Errorf("the MCP server should refuse as the command line refuses an agent, got %s", answer)
			}

			if test.Tree(t, h.roots().Data) != before {
				t.Error("a refused MCP call should write nothing")
			}
		})
	}
}
