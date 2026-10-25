package grounding_test

import (
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/test"
)

func mcp(tool string, input map[string]any) map[string]any {
	return map[string]any{"session_id": "s1", "tool_name": "mcp__sdd__" + tool, "tool_input": input}
}

func TestMCPReadsGroundAQuestion(t *testing.T) {
	cases := []struct {
		name    string
		reads   []map[string]any
		missing string
	}{
		{
			name: "every read through MCP passes",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "a host key that changed"}),
				mcp("search", map[string]any{"terms": []string{"ssh_host_key_changed"}}),
				mcp("view", map[string]any{"layout": "active:as-counts"}),
			},
		},
		{
			name: "one search carrying both modes counts for both",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "host key", "terms": []string{"host_key"}}),
				mcp("view", map[string]any{"layout": "active:as-counts"}),
			},
		},
		{
			name: "without the view the listing is owed",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "a host key that changed"}),
				mcp("search", map[string]any{"terms": []string{"ssh_host_key_changed"}}),
			},
			missing: "No listing of every topic ran this turn.",
		},
		{
			name: "a view of another layout is no listing of every topic",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "host key", "terms": []string{"host_key"}}),
				mcp("view", map[string]any{"layout": "top(15)"}),
			},
			missing: "No listing of every topic ran this turn.",
		},
		{
			name: "blank terms are no literal read",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "host key", "terms": []string{" "}}),
				mcp("view", map[string]any{"layout": "active:as-counts"}),
			},
			missing: "No `--term` search ran this turn.",
		},
		{
			name: "MCP and Bash reads add up",
			reads: []map[string]any{
				mcp("search", map[string]any{"query": "a host key that changed"}),
				bash(literalRead),
				mcp("view", map[string]any{"layout": "active:as-counts"}),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := bare(t)
			record(t, env, tc.reads...)

			got := run(t, env, "gate", ask("s1"))
			if tc.missing == "" {
				passes(t, got)

				return
			}

			refuses(t, got, tc.missing)
		})
	}
}

func TestMCPViewCountsAPrefixedListing(t *testing.T) {
	env := configured(t, "area-prefix.yaml")
	record(t, env,
		mcp("search", map[string]any{"query": "host key", "terms": []string{"host_key"}}),
		mcp("view", map[string]any{"layout": "topic(area-hooks):as-list"}),
	)

	passes(t, run(t, env, "gate", ask("s1")))
}

func TestMCPShowOwesTheDepth(t *testing.T) {
	entry := []string{"20260907-164635-d-tac-vkm"}

	cases := []struct {
		name  string
		input map[string]any
		says  string
	}{
		{name: "no depth reads at the server's default", input: map[string]any{"ids": entry},
			says: "`down 1` reads short of `\"down\": 2, \"up\": 1`."},
		{name: "a shallow down", input: map[string]any{"ids": entry, "down": 1, "up": 1}, says: "`down 1`"},
		{name: "a shallow up", input: map[string]any{"ids": entry, "down": 2, "up": 0}, says: "`up 0`"},
		{name: "the depth owed", input: map[string]any{"ids": entry, "down": 2, "up": 1}},
		{name: "a rules entry reads at any depth", input: map[string]any{"ids": []string{"20260703-094500-d-prc-cap"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := run(t, bare(t), "gate", mcp("show", tc.input))
			if tc.says == "" {
				passes(t, got)

				return
			}

			refuses(t, got, tc.says)
		})
	}
}

func TestAnExemptSeatShowsThroughMCPAtAnyDepth(t *testing.T) {
	env := configured(t, "show-depth-exempt.yaml")
	env.Processes, env.OwnerName = test.NewProcesses(), "claude"
	test.ClaimSeat(t, test.MemoryStore(env.ProjectDir, env.Home, env.Checkout), "reviewer", test.Session)

	passes(t, run(t, env, "gate", mcp("show", map[string]any{"ids": []string{"20260907-164635-d-tac-vkm"}})))
}

func TestTheMCPCaptureOwesTheReads(t *testing.T) {
	capture := mcp("start_procedure", map[string]any{"canonical": "capture"})

	t.Run("without reads it is refused", func(t *testing.T) {
		refuses(t, run(t, bare(t), "gate", capture), "No `--query` search ran this turn.")
	})

	t.Run("with reads it passes", func(t *testing.T) {
		env := bare(t)
		record(t, env,
			mcp("search", map[string]any{"query": "host key", "terms": []string{"host_key"}}),
			mcp("view", map[string]any{"layout": "active:as-counts"}),
		)

		passes(t, run(t, env, "gate", capture))
	})

	t.Run("another procedure starts freely", func(t *testing.T) {
		passes(t, run(t, bare(t), "gate", mcp("start_procedure", map[string]any{"canonical": "engage"})))
	})

	t.Run("other MCP tools pass", func(t *testing.T) {
		passes(t, run(t, bare(t), "gate", mcp("next", map[string]any{"instance": "i-1"})))
	})
}
