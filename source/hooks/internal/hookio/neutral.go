package hookio

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// hookLine is how a line our hooks write opens: an agentic-* name, alone or
// with one word (`agentic-helpers level:`), the scratch and tool library
// lines, and shell-guard's withheld-credential mark.
// Case is folded, since a model reads `Agentic-Memory:` as the same voice.
var hookLine = regexp.MustCompile(`(?i)^\[?(?:agentic-[a-z-]+(?: [a-z]+)?|scratch|tool library):`)

// quoteMark opens a line of filed text that would otherwise open as a hook
// line of ours: a quote, which a reader takes as cited, never as said.
const quoteMark = "> "

// tagMark stands in for the `<` of a tag-shaped token: a look-alike no tag
// parser reads as `<` and no decoding turns back into one, where `&lt;` is
// exactly what an HTML-trained reader decodes, and costs three characters
// more on every placeholder a body names.
const tagMark = '‹'

// Neutralize is text another agent, a file or a transcript filed, made safe
// to hand Claude Code as hook context: a line opening as a hook line of ours
// (after any blank or invisible lead) gets quoteMark, and the `<` of every
// tag-shaped token (`<x`, `</x`, `<!x`, invisible characters between) becomes
// a look-alike, so no text renders as our hooks' voice or as a harness tag.
// Everything else stays byte for byte.
func Neutralize(text string) string {
	return neutralize(text, true)
}

// NeutralizeInline is Neutralize for text set inside a line, a title after
// an id: its first line opens no line, so only the lines after a break in it
// are checked for a hook line's opening.
func NeutralizeInline(text string) string {
	return neutralize(text, false)
}

func neutralize(text string, atLineStart bool) string {
	if !strings.ContainsAny(text, "<:") {
		return text
	}

	var out strings.Builder

	out.Grow(len(text) + 8)

	lineStart := atLineStart

	for index := 0; index < len(text); {
		if lineStart {
			lead := blankLead(text[index:])
			if hookLine.MatchString(text[index+lead:]) {
				out.WriteString(text[index : index+lead])
				out.WriteString(quoteMark)
				index += lead
			}

			lineStart = false

			continue
		}

		char, size := utf8.DecodeRuneInString(text[index:])

		switch {
		case char == '\n' || char == '\r':
			lineStart = true
		case char == '<' && tagFollows(text[index+size:]):
			out.WriteRune(tagMark)
			index += size

			continue
		}

		out.WriteString(text[index : index+size])
		index += size
	}

	return out.String()
}

// blankLead is the byte length of the blanks and invisible format characters
// opening text, which hide nothing from a reader taking the line as a hook's.
func blankLead(text string) int {
	for index, char := range text {
		if !unicode.IsSpace(char) && !unicode.Is(unicode.Cf, char) {
			return index
		}
	}

	return len(text)
}

// tagFollows is whether what follows a `<` makes it a tag's opening: a
// letter, after an optional `/` or `!` and any invisible format characters.
func tagFollows(text string) bool {
	text = strings.TrimLeft(text, "/!")

	for _, char := range text {
		if unicode.Is(unicode.Cf, char) {
			continue
		}

		return unicode.IsLetter(char)
	}

	return false
}
