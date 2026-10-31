package transcript

import (
	"slices"
	"strings"
)

// Pieces exists because Claude Code inlines a hook's output only up to a
// character limit and swaps anything longer for a file path, so a caller sizes
// a piece under that limit. Lengths count characters, as the client does. It
// keeps the newest maxPieces and answers how many older ones it cut. A piece
// opening inside a day Condense headed opens with that day's header, inside
// the limit, so each piece read alone still dates its turns.
func Pieces(text string, pieceChars int, maxPieces int) ([]string, int) {
	if pieceChars < 1 || maxPieces < 1 || strings.TrimSpace(text) == "" {
		return nil, 0
	}

	var pieces [][]rune

	var (
		current []rune
		day     []rune
		// opened is how much of current is the day header it opened with.
		opened int
	)

	closePiece := func() {
		pieces = append(pieces, current)
		current, opened = nil, 0
	}

	for _, line := range splitLinesKeepingEnds([]rune(text)) {
		if isDayHeader(line) {
			day = line
		}

		for len(line) > 0 {
			if len(current) == 0 && len(day) > 0 && 2*len(day) <= pieceChars {
				current = slices.Clone(day)
				opened = len(day)

				// A header opening a piece is that piece's own.
				if isDayHeader(line) {
					break
				}
			}

			room := pieceChars - len(current)
			if len(line) <= room {
				current = append(current, line...)

				break
			}

			if len(current) > opened {
				closePiece()

				continue
			}

			current = append(current, line[:room]...)
			line = line[room:]

			closePiece()
		}
	}

	if len(current) > opened {
		pieces = append(pieces, current)
	}

	cut := 0

	if len(pieces) > maxPieces {
		cut = len(pieces) - maxPieces
		pieces = pieces[cut:]
	}

	rendered := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		rendered = append(rendered, string(piece))
	}

	return rendered, cut
}

// Tail is the newest whole lines of text within maxChars. Where the cut falls
// inside a day Condense headed, that day's header opens the tail, inside the
// limit.
func Tail(text string, maxChars int) string {
	runes := []rune(text)

	kept := tail(runes, maxChars)

	day := dayBefore(runes, len(runes)-len(kept))
	if 2*len(day) > maxChars {
		return string(kept)
	}

	kept = tail(runes, maxChars-len(day))
	if opensWithDay(kept) {
		return string(kept)
	}

	return string(dayBefore(runes, len(runes)-len(kept))) + string(kept)
}

func tail(runes []rune, maxChars int) []rune {
	if maxChars < 1 {
		return nil
	}

	if len(runes) <= maxChars {
		return runes
	}

	kept := runes[len(runes)-maxChars:]
	if runes[len(runes)-maxChars-1] == '\n' {
		return kept
	}

	for index, char := range kept {
		if char == '\n' && index+1 < len(kept) {
			return kept[index+1:]
		}
	}

	return kept
}

func opensWithDay(text []rune) bool {
	lines := splitLinesKeepingEnds(text)

	return len(lines) > 0 && isDayHeader(lines[0])
}

// dayBefore is the last day header among the whole lines of text before end,
// nil where none stands there.
func dayBefore(text []rune, end int) []rune {
	var day []rune

	for _, line := range splitLinesKeepingEnds(text[:end]) {
		if line[len(line)-1] == '\n' && isDayHeader(line) {
			day = line
		}
	}

	return day
}

func splitLinesKeepingEnds(text []rune) [][]rune {
	var lines [][]rune

	start := 0

	for index, char := range text {
		if char == '\n' {
			lines = append(lines, text[start:index+1])
			start = index + 1
		}
	}

	if start < len(text) {
		lines = append(lines, text[start:])
	}

	return lines
}
