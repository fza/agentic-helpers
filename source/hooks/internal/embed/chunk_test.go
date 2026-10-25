package embed_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

const (
	maximumChunk   = 3200
	maximumOverlap = 320
	heading        = "Entry: Seats survive a compaction\n\n"
)

func chunkBodies(t *testing.T, body string) []string {
	t.Helper()
	chunks := embed.Chunks("Seats survive a compaction", body)
	bodies := make([]string, len(chunks))
	for index, chunk := range chunks {
		text, found := strings.CutPrefix(chunk, heading)
		if !found {
			t.Fatalf("every chunk should open with the entry's title, got %q", chunk[:min(len(chunk), 80)])
		}
		bodies[index] = text
	}
	return bodies
}

func words(count int) string {
	parts := make([]string, count)
	for index := range parts {
		parts[index] = fmt.Sprintf("w%05d", index)
	}
	return strings.Join(parts, " ")
}

// ideographs is count distinct three-byte runes, so no tail of one chunk repeats by chance.
func ideographs(count int) string {
	runes := make([]rune, count)
	for index := range runes {
		runes[index] = rune(0x4E00 + index)
	}
	return string(runes)
}

func TestChunksOfAnEmptyBody(t *testing.T) {
	for _, body := range []string{"", " \n\n \n"} {
		chunks := embed.Chunks("Seats survive a compaction", body)
		if !reflect.DeepEqual(chunks, []string{"Entry: Seats survive a compaction"}) {
			t.Errorf("a body without text should still yield one chunk naming the entry, got %q", chunks)
		}
	}
}

func TestChunksOfAShortBody(t *testing.T) {
	bodies := chunkBodies(t, "A seat holds a session.\n\nIt is released on exit.")
	if !reflect.DeepEqual(bodies, []string{"A seat holds a session.\n\nIt is released on exit."}) {
		t.Errorf("a body within the bound should stay one chunk, unchanged, got %q", bodies)
	}
}

func TestChunksAreBoundedAndOverlap(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		overlaps bool
	}{
		{name: "words", body: words(2000), overlaps: true},
		{name: "lines", body: strings.ReplaceAll(words(2000), "0 ", "0\n"), overlaps: true},
		{name: "multibyte runes with no separator", body: ideographs(12000), overlaps: true},
		{
			name: "a paragraph past the bound between short ones",
			body: "intro\n\n" + words(1000) + "\n\ntail",
		},
		{
			name: "a paragraph too long to follow any overlap",
			body: strings.Repeat("x", 3000) + "\n\n" + strings.Repeat("s", 100) + "\n\n" + strings.Repeat("b", 3150),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			bodies := chunkBodies(t, testCase.body)
			if len(bodies) < 2 {
				t.Fatalf("a body past the bound should be split, got %d chunk", len(bodies))
			}
			for index, body := range bodies {
				if utf8.RuneCountInString(body) > maximumChunk {
					t.Errorf("chunk %d should hold at most the bound in runes, holds %d", index, utf8.RuneCountInString(body))
				}
			}
			for index := 1; testCase.overlaps && index < len(bodies); index++ {
				overlap := sharedEdge(bodies[index-1], bodies[index])
				if overlap == 0 || overlap > maximumOverlap {
					t.Errorf("chunk %d should repeat a bounded tail of the chunk before, repeats %d runes", index, overlap)
				}
			}
		})
	}
}

func TestChunksCountRunesNotBytes(t *testing.T) {
	body := ideographs(10000)
	bodies := chunkBodies(t, body)
	if utf8.RuneCountInString(bodies[0]) != maximumChunk {
		t.Errorf("a hard cut should fill a chunk to the bound in runes, filled %d", utf8.RuneCountInString(bodies[0]))
	}
	if sharedEdge(bodies[0], bodies[1]) != maximumOverlap {
		t.Errorf("a hard cut should carry the full overlap in runes, carried %d", sharedEdge(bodies[0], bodies[1]))
	}

	paragraphs := ideographs(2000) + "\n\n" + ideographs(500)
	whole := chunkBodies(t, paragraphs)
	if !reflect.DeepEqual(whole, []string{paragraphs}) {
		t.Errorf("paragraphs within the bound in runes should stay one chunk however many bytes they take, got %d chunks", len(whole))
	}
}

func TestChunksPreferTheCoarsestSeparator(t *testing.T) {
	paragraph := func(seed string) string {
		var lines []string
		for line := range 30 {
			lines = append(lines, strings.TrimSpace(strings.Repeat(seed+fmt.Sprint(line)+" ", 10)))
		}
		return strings.Join(lines, "\n")
	}
	first, second := paragraph("alpha"), paragraph("beta")

	t.Run("blank lines before newlines", func(t *testing.T) {
		bodies := chunkBodies(t, first+"\n\n"+second)
		if !reflect.DeepEqual(bodies, []string{first, second}) {
			t.Errorf("paragraphs should be cut at the blank line between them, got %d chunks", len(bodies))
		}
	})

	t.Run("newlines before spaces", func(t *testing.T) {
		body := first + "\n" + second
		lines := map[string]bool{}
		for line := range strings.SplitSeq(body, "\n") {
			lines[line] = true
		}
		bodies := chunkBodies(t, body)
		if len(bodies) < 2 {
			t.Fatalf("a body past the bound should be split, got %d chunk", len(bodies))
		}
		for index, chunk := range bodies {
			for line := range strings.SplitSeq(chunk, "\n") {
				if !lines[line] {
					t.Errorf("chunk %d should hold whole lines only, holds %q", index, line)
				}
			}
		}
	})

	t.Run("spaces before a hard cut", func(t *testing.T) {
		body := words(2000)
		known := map[string]bool{}
		for word := range strings.FieldsSeq(body) {
			known[word] = true
		}
		for index, chunk := range chunkBodies(t, body) {
			for word := range strings.FieldsSeq(chunk) {
				if !known[word] {
					t.Errorf("chunk %d should hold whole words only, holds %q", index, word)
				}
			}
		}
	})
}

func TestChunksAreDeterministic(t *testing.T) {
	body := words(3000) + "\n\n" + strings.Repeat("ü", 5000)
	if !reflect.DeepEqual(embed.Chunks("Title", body), embed.Chunks("Title", body)) {
		t.Errorf("the same entry should always yield the same chunks")
	}
}

// sharedEdge is the rune length of the longest tail of previous that opens next.
func sharedEdge(previous string, next string) int {
	runes := []rune(previous)
	for start := range runes {
		tail := string(runes[start:])
		if strings.HasPrefix(next, tail) {
			return len(runes) - start
		}
	}
	return 0
}
