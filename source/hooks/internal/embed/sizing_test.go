package embed_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

func TestEveryChunkFitsTheCapWithItsHeading(t *testing.T) {
	longTitle := strings.Repeat("Title ", 100)

	cases := []struct {
		name     string
		title    string
		body     string
		maxRunes int
	}{
		{name: "dense CJK under a small cap", title: "Seats", body: ideographs(5000), maxRunes: 512},
		{name: "words under the fallback cap", title: "Seats", body: words(2000), maxRunes: 2048},
		{name: "a long title", title: longTitle, body: words(500), maxRunes: 1024},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := embed.Chunks(tc.title, tc.body, tc.maxRunes)
			if len(chunks) < 2 {
				t.Fatalf("a body past the cap should yield several chunks, got %d", len(chunks))
			}

			for _, chunk := range chunks {
				if utf8.RuneCountInString(chunk) > tc.maxRunes {
					t.Errorf("a chunk should hold at most the cap, heading included, got %d runes", utf8.RuneCountInString(chunk))
				}
			}

			heading, _, _ := strings.Cut(chunks[0], "\n\n")
			if utf8.RuneCountInString(heading) > len("Entry: ")+tc.maxRunes/4 {
				t.Errorf("the title should take at most a quarter of the cap, got %d runes", utf8.RuneCountInString(heading))
			}
		})
	}
}

func TestPiecesCutOnlyATextPastTheCap(t *testing.T) {
	short := "Counts the lines of a file."
	if pieces := embed.Pieces(short, 64); !reflect.DeepEqual(pieces, []string{short}) {
		t.Errorf("a text within the cap should stay whole, got %q", pieces)
	}

	pieces := embed.Pieces(ideographs(3000), 1024)
	if len(pieces) < 3 {
		t.Fatalf("a text past the cap should yield pieces, got %d", len(pieces))
	}

	for _, piece := range pieces {
		if utf8.RuneCountInString(piece) > 1024 {
			t.Errorf("a piece should hold at most the cap, got %d runes", utf8.RuneCountInString(piece))
		}
	}
}

func TestCutKeepsTheHead(t *testing.T) {
	if embed.Cut("abcdef", 3) != "abc" || embed.Cut("字字字字", 2) != "字字" || embed.Cut("ab", 5) != "ab" {
		t.Error("a cut should keep the head runes up to the cap")
	}
}

func TestARowHashFoldsTheCap(t *testing.T) {
	if embed.RowHash("content", 1792) == embed.RowHash("content", 2048) {
		t.Error("rows cut to another cap should never share a hash")
	}

	if embed.RowHash("content", 1792) != embed.RowHash("content", 1792) || embed.RowHash("a", 64) == embed.RowHash("b", 64) {
		t.Error("a row hash should follow its content and cap alone")
	}
}

func TestACapCutsEveryTextBeforeItsTemplate(t *testing.T) {
	raw := &recording{}
	embedder := embed.Template(raw, embed.Config{QueryTemplate: "query: {text}", DocumentTemplate: "text: {text}"}).Capped(4)

	_, _ = embedder.Queries(context.Background(), []string{"a long prompt"})
	_, _ = embedder.Documents(context.Background(), []string{"字字字字字字"})

	if !slices.Equal(raw.texts, []string{"query: a lo", "text: 字字字字"}) {
		t.Errorf("a capped text should be cut to its head before the template wraps it, got %q", raw.texts)
	}
}

func TestTheFingerprintIgnoresThePathAndTheCap(t *testing.T) {
	raw := &recording{fingerprint: "openai/model/0"}
	base := embed.Template(raw, embed.Config{Endpoint: "http://127.0.0.1:1235"})
	routed := embed.Template(raw, embed.Config{Endpoint: "http://127.0.0.1:1235", Path: "/v3/compat/embeddings"}).Capped(1792)

	if base.Fingerprint() != routed.Fingerprint() {
		t.Errorf("a path or a cap should leave the fingerprint as it was, got %q", routed.Fingerprint())
	}
}
