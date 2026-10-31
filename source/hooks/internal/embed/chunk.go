package embed

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	chunkSize    = 3200
	chunkOverlap = 320
	chunkPrefix  = "Entry: "
	// titleShare is the part of a cap an entry's title may take in each chunk,
	// as sdd caps its summary line, so a long title leaves room for the body.
	titleShare = 4
)

// The empty separator is the hard cut: every text contains it, and strings.Split
// on it yields one rune per piece.
var separators = []string{"\n\n", "\n", " ", ""}

// Chunks cuts an entry into the texts it embeds, each at most maxRunes runes
// with the prefix naming the entry: the body in pieces of at most chunkSize,
// smaller where the cap leaves less.
func Chunks(title string, body string, maxRunes int) []string {
	heading := chunkPrefix + Cut(title, maxRunes/titleShare)
	room := min(chunkSize, maxRunes-utf8.RuneCountInString(heading)-2)

	pieces := split(body, separators, room, min(chunkOverlap, room/10))
	if len(pieces) == 0 {
		return []string{heading}
	}
	chunks := make([]string, len(pieces))
	for index, piece := range pieces {
		chunks[index] = heading + "\n\n" + piece
	}
	return chunks
}

// Pieces cuts a text into parts of at most maxRunes runes, the whole text
// where it fits.
func Pieces(text string, maxRunes int) []string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return []string{text}
	}

	return split(text, separators, maxRunes, min(chunkOverlap, maxRunes/10))
}

// Cut is the head of text up to maxRunes runes.
func Cut(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}

	return string([]rune(text)[:max(maxRunes, 0)])
}

// RowHash names a stored row by the content it was embedded from and the cap
// its texts were cut to, so rows cut to another cap never pass as current.
func RowHash(content string, maxRunes int) string {
	sum := sha256.Sum256([]byte(content + "\x00" + strconv.Itoa(maxRunes)))

	return hex.EncodeToString(sum[:])[:16]
}

func split(text string, candidates []string, size int, overlap int) []string {
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
		if utf8.RuneCountInString(piece) <= size {
			fitting = append(fitting, piece)
			continue
		}
		chunks = append(chunks, merge(fitting, separator, size, overlap)...)
		fitting = nil
		chunks = append(chunks, split(piece, finer, size, overlap)...)
	}
	return append(chunks, merge(fitting, separator, size, overlap)...)
}

// merge packs pieces into chunks of at most size runes. Once a chunk is full,
// the next one starts from the trailing pieces of the last that fit in overlap.
func merge(pieces []string, separator string, size int, overlap int) []string {
	separatorLength := utf8.RuneCountInString(separator)
	var chunks []string
	var current []string
	total := 0
	for _, piece := range pieces {
		pieceLength := utf8.RuneCountInString(piece)
		if len(current) > 0 && total+separatorLength+pieceLength > size {
			chunks = appendJoined(chunks, current, separator)
			for len(current) > 0 && (total > overlap || total+separatorLength+pieceLength > size) {
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
