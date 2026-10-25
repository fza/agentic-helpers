package embed

import (
	"strings"
	"unicode/utf8"
)

const (
	chunkSize    = 3200
	chunkOverlap = 320
	chunkPrefix  = "Entry: "
)

// The empty separator is the hard cut: every text contains it, and strings.Split
// on it yields one rune per piece.
var separators = []string{"\n\n", "\n", " ", ""}

// Chunks bounds the body text of each chunk, not the prefix naming the entry.
func Chunks(title string, body string) []string {
	heading := chunkPrefix + title
	pieces := split(body, separators)
	if len(pieces) == 0 {
		return []string{heading}
	}
	chunks := make([]string, len(pieces))
	for index, piece := range pieces {
		chunks[index] = heading + "\n\n" + piece
	}
	return chunks
}

func split(text string, candidates []string) []string {
	var separator string
	var finer []string
	for index, candidate := range candidates {
		if strings.Contains(text, candidate) {
			separator = candidate
			finer = candidates[index+1:]
			break
		}
	}

	var chunks []string
	var fitting []string
	for _, piece := range strings.Split(text, separator) {
		if utf8.RuneCountInString(piece) <= chunkSize {
			fitting = append(fitting, piece)
			continue
		}
		chunks = append(chunks, merge(fitting, separator)...)
		fitting = nil
		chunks = append(chunks, split(piece, finer)...)
	}
	return append(chunks, merge(fitting, separator)...)
}

// merge packs pieces into chunks of at most chunkSize runes. Once a chunk is full,
// the next one starts from the trailing pieces of the last that fit in chunkOverlap.
func merge(pieces []string, separator string) []string {
	separatorLength := utf8.RuneCountInString(separator)
	var chunks []string
	var current []string
	total := 0
	for _, piece := range pieces {
		pieceLength := utf8.RuneCountInString(piece)
		if len(current) > 0 && total+separatorLength+pieceLength > chunkSize {
			chunks = appendJoined(chunks, current, separator)
			for len(current) > 0 && (total > chunkOverlap || total+separatorLength+pieceLength > chunkSize) {
				total -= utf8.RuneCountInString(current[0])
				if len(current) > 1 {
					total -= separatorLength
				}
				current = current[1:]
			}
		}
		if len(current) > 0 {
			total += separatorLength
		}
		current = append(current, piece)
		total += pieceLength
	}
	return appendJoined(chunks, current, separator)
}

func appendJoined(chunks []string, pieces []string, separator string) []string {
	joined := strings.TrimSpace(strings.Join(pieces, separator))
	if joined == "" {
		return chunks
	}
	return append(chunks, joined)
}
