package transcript

import "strings"

// Pieces exists because Claude Code inlines a hook's output only up to a
// character limit and swaps anything longer for a file path, so a caller sizes
// a piece under that limit. Lengths count characters, as the client does. It
// keeps the newest maxPieces and answers how many older ones it cut.
func Pieces(text string, pieceChars int, maxPieces int) ([]string, int) {
	if pieceChars < 1 || maxPieces < 1 || strings.TrimSpace(text) == "" {
		return nil, 0
	}

	var pieces [][]rune

	var current []rune

	for _, line := range splitLinesKeepingEnds([]rune(text)) {
		for len(line) > pieceChars {
			if len(current) > 0 {
				pieces = append(pieces, current)
				current = nil
			}

			pieces = append(pieces, line[:pieceChars])
			line = line[pieceChars:]
		}

		if len(current)+len(line) > pieceChars {
			pieces = append(pieces, current)
			current = nil
		}

		current = append(current, line...)
	}

	pieces = append(pieces, current)

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

func Tail(text string, maxChars int) string {
	if maxChars < 1 {
		return ""
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return string(runes)
	}

	kept := runes[len(runes)-maxChars:]
	if runes[len(runes)-maxChars-1] == '\n' {
		return string(kept)
	}

	for index, char := range kept {
		if char == '\n' && index+1 < len(kept) {
			return string(kept[index+1:])
		}
	}

	return string(kept)
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
