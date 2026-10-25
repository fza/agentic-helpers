package transcript_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func session(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	test.WriteFile(t, path, test.Fixture(t, "transcript/session.jsonl"))

	return path
}

func turns(t *testing.T, after time.Time) []transcript.Turn {
	t.Helper()

	read, err := transcript.Since(context.Background(), session(t), after)
	if err != nil {
		t.Fatalf("reading the transcript: %v", err)
	}

	return read
}

func numbers(read []transcript.Turn) []int {
	held := make([]int, 0, len(read))
	for _, turn := range read {
		held = append(held, turn.Number)
	}

	return held
}

func at(clock string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, "2026-01-02T"+clock+"Z")

	return parsed
}

func TestSinceSelectsTurnsByPromptTime(t *testing.T) {
	cases := []struct {
		name  string
		after time.Time
		want  []int
	}{
		{"a zero time selects every turn", time.Time{}, []int{1, 2, 3, 4}},
		{"a time before the first prompt selects every turn", at("09:00:00"), []int{1, 2, 3, 4}},
		{"a prompt at exactly the time is left out", at("10:00:00"), []int{2, 3, 4}},
		{"a time inside a turn leaves that turn out", at("10:07:00"), []int{3, 4}},
		{"a time after the last prompt selects nothing", at("11:00:00"), []int{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := numbers(turns(t, tc.after))
			if !slices.Equal(got, tc.want) {
				t.Errorf("the selection should hold the turns prompted after the time, got %v", got)
			}
		})
	}
}

func TestSinceRecognisesPrompts(t *testing.T) {
	read := turns(t, time.Time{})

	asked := make([]string, 0, len(read))
	for _, turn := range read {
		asked = append(asked, turn.Asked)
	}

	if !slices.Equal(asked, []string{"Why does the build fail?", "Search for TODOs", "Ship it", "Still there?"}) {
		t.Errorf("only typed prompts should open a turn, got %q", asked)
	}

	if !read[0].At.Equal(at("10:00:00")) {
		t.Errorf("a turn should carry its prompt's time, got %v", read[0].At)
	}

	condensed := transcript.Condense(read)

	for _, outsider := range []string{
		"carrier text must not open a turn",
		"tool output must stay out",
		"injected skill body must stay out",
		"subagent prompt must stay out",
		"subagent reply must stay out",
		"subagent.go",
		"compaction summary must stay out",
		"reply before any prompt must stay out",
		"thinking must stay out",
		"a text field outside a text block must stay out",
	} {
		t.Run(outsider, func(t *testing.T) {
			if strings.Contains(condensed, outsider) {
				t.Error("a record that is no part of a prompted turn should stay out of the log")
			}
		})
	}
}

func TestSinceSummarisesTools(t *testing.T) {
	read := turns(t, time.Time{})

	cases := []struct {
		name string
		turn int
		want []string
	}{
		{"a path is named relative to the working directory", 0, []string{
			"Read internal/build/build.go", "Bash go test ./...", "Read internal/build/build.go",
		}},
		{"a long input is cut and a tool without a named input stands alone", 1, []string{
			"Grep internal",
			"Bash grep -rn TODO internal cmd test --include=*.go | sort | uniq -c | sort -rn | ...",
			"TodoWrite",
			"Bash ls",
			"?",
		}},
		{"a turn without tools did nothing", 2, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(read[tc.turn].Did, tc.want) {
				t.Errorf("the tool calls should be summarised in order, got %q", read[tc.turn].Did)
			}
		})
	}

	if len([]rune(read[1].Did[1])) != len("Bash ")+80 {
		t.Error("a cut input should keep the summary limit")
	}
}

func TestSinceKeepsFinalReply(t *testing.T) {
	read := turns(t, time.Time{})

	cases := []struct {
		name string
		turn int
		want []string
	}{
		{"every reply of a turn is kept, the final one last", 0, []string{
			"The import path is stale.", "Final reply one: fix the import path.",
		}},
		{"a reply after malformed lines stays with its turn", 1, []string{"Final reply two: three TODOs remain."}},
		{"a reply stays with its own prompt", 2, []string{"Shipped."}},
		{"the last line counts without a line end, unanswered", 3, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !slices.Equal(read[tc.turn].Replied, tc.want) {
				t.Errorf("a turn should hold its own replies, got %q", read[tc.turn].Replied)
			}
		})
	}
}

func TestSinceUndatedPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undated.jsonl")
	test.WriteFile(t, path, test.Fixture(t, "transcript/undated.jsonl"))

	cases := []struct {
		name  string
		after time.Time
		want  int
	}{
		{"a zero time selects a prompt without a time", time.Time{}, 1},
		{"any time leaves a prompt without a time out", at("09:00:00"), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			read, err := transcript.Since(context.Background(), path, tc.after)
			if err != nil || len(read) != tc.want {
				t.Fatalf("an undated prompt should count only when every turn is asked for, got %v, %v", read, err)
			}
		})
	}

	t.Run("a path is kept whole where the record names no working directory", func(t *testing.T) {
		read, _ := transcript.Since(context.Background(), path, time.Time{})
		if !slices.Equal(read[0].Did, []string{"Read /abs/file.go"}) {
			t.Errorf("the path should stay as recorded, got %q", read[0].Did)
		}
	})
}

func TestSinceReadsAMissingTranscriptAsEmpty(t *testing.T) {
	read, err := transcript.Since(context.Background(), filepath.Join(t.TempDir(), "gone.jsonl"), time.Time{})
	if err != nil || len(read) > 0 {
		t.Errorf("a cleaned-up transcript should hold no turns and no error, got %v, %v", read, err)
	}
}

func TestSinceStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := transcript.Since(ctx, session(t), time.Time{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled read should report the cancellation, got %v", err)
	}
}

func TestCondense(t *testing.T) {
	t.Run("turns render in fixed sections, oldest first", func(t *testing.T) {
		got := transcript.Condense(turns(t, time.Time{}))
		if got != test.Fixture(t, "transcript/condensed.md") {
			t.Errorf("the condensed log should match the fixture, got:\n%s", got)
		}
	})

	t.Run("a turn without a prompt renders no asked section", func(t *testing.T) {
		got := transcript.Condense([]transcript.Turn{{Number: 1, At: at("10:00:00"), Replied: []string{"resumed"}}})
		if strings.Contains(got, "### asked") || !strings.Contains(got, "### replied\n\nresumed") {
			t.Errorf("only the sections a turn holds should render, got:\n%s", got)
		}
	})

	t.Run("no turns render nothing", func(t *testing.T) {
		if transcript.Condense(nil) != "" {
			t.Error("an empty selection should condense to nothing")
		}
	})
}

func TestPieces(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		pieceChars int
		maxPieces  int
		want       []string
		cut        int
	}{
		{"a short text stays whole", "a\nb\n", 10, 5, []string{"a\nb\n"}, 0},
		{"lines group up to the limit", "aaa\nbbb\nccc\n", 8, 5, []string{"aaa\nbbb\n", "ccc\n"}, 0},
		{"the oldest pieces are dropped", "aaa\nbbb\nccc\n", 4, 2, []string{"bbb\n", "ccc\n"}, 1},
		{"a line longer than a piece is split", "abcdefghij\n", 4, 5, []string{"abcd", "efgh", "ij\n"}, 0},
		{"a long line closes the piece before it", "ab\nabcdef\n", 4, 5, []string{"ab\n", "abcd", "ef\n"}, 0},
		{"characters count, not bytes", "äöü\nßé\n", 4, 5, []string{"äöü\n", "ßé\n"}, 0},
		{"a blank text yields nothing", " \n", 10, 5, nil, 0},
		{"no piece size yields nothing", "a\n", 0, 5, nil, 0},
		{"a line exactly filling pieces leaves no empty piece", "abcdefgh", 4, 5, []string{"abcd", "efgh"}, 0},
		{"a negative piece size yields nothing", "a\n", -1, 5, nil, 0},
		{"a negative piece count yields nothing", "a\n", 10, -1, nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := transcript.Pieces(tc.text, tc.pieceChars, tc.maxPieces)
			if !slices.Equal(got, tc.want) {
				t.Errorf("the pieces should split at line ends within bounds, got %q", got)
			}

			if cut != tc.cut {
				t.Errorf("the count of pieces cut should match the oldest dropped, got %d", cut)
			}
		})
	}

	t.Run("a long log keeps its newest lines within bounds", func(t *testing.T) {
		var builder strings.Builder
		for index := range 2_000 {
			builder.WriteString(strings.Repeat("y", index%120) + " line\n")
		}

		builder.WriteString("the newest line survives\n")

		got, cut := transcript.Pieces(builder.String(), 9_000, 5)
		if cut == 0 {
			t.Error("a log longer than every piece allowed should say how many it cut")
		}

		if len(got) != 5 {
			t.Errorf("a long log should fill every piece allowed, got %d", len(got))
		}

		for _, piece := range got {
			if len([]rune(piece)) > 9_000 || !strings.HasSuffix(piece, "\n") {
				t.Error("every piece should stay within the limit and end at a line end")
			}
		}

		if !strings.HasSuffix(got[len(got)-1], "the newest line survives\n") {
			t.Error("the newest line should close the last piece")
		}
	})
}

func TestTail(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		maxChars int
		want     string
	}{
		{"a short text stays whole", "a\nb\n", 10, "a\nb\n"},
		{"a cut on a line end keeps the whole lines", "first\nsecond\nthird\n", 13, "second\nthird\n"},
		{"a cut inside a line drops that line", "first\nsecond\nthird\n", 15, "second\nthird\n"},
		{"a last line longer than the limit is cut", "abcdefgh", 3, "fgh"},
		{"a last line ending at the cut keeps its end", "abcdefgh\n", 3, "gh\n"},
		{"characters count, not bytes", "ää\nöö\n", 3, "öö\n"},
		{"a text exactly the limit stays whole", "ab\n", 3, "ab\n"},
		{"no size yields nothing", "a\n", 0, ""},
		{"a negative size yields nothing", "a\n", -1, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transcript.Tail(tc.text, tc.maxChars)
			if got != tc.want {
				t.Errorf("the tail should keep the newest whole lines within the limit, got %q", got)
			}
		})
	}
}
