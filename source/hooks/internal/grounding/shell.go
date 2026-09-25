package grounding

import (
	"regexp"
	"strings"
)

// An `sdd` invocation, rather than the `.sdd/` directory or the word as an
// argument to something else. It has to sit where a command word sits: opening
// the line, following a separator, after the `--` that ends a runner's flags,
// or after a runner that hands the rest of the line to another program. A path
// spelling reaches the same binary, so `/opt/homebrew/bin/sdd` counts.
const runner = `env(?:\s+[A-Za-z_]\w*=\S*)*|command|exec|nohup|time|sudo` +
	`|xargs(?:\s+-\S+)*|stdbuf(?:\s+-\S+)*|caffeinate(?:\s+-\S+)*`

const atCommand = `(?:^|[\n;|&]\s*|--\s+|\b(?:` + runner + `)\s+)`

const sddWord = `(?:\S*/)?sdd\s+[a-z]`

var (
	bareSDD = regexp.MustCompile(atCommand + sddWord)

	// A `(` opens a subshell only where nothing word-like precedes it, so
	// `(sdd info)` is a call and a pattern such as `Bash(sdd new` is not. RE2
	// has no lookbehind, so the preceding character is checked by hand.
	subshellSDD = regexp.MustCompile(`\(\s*` + sddWord)

	wrapperCall = regexp.MustCompile(`graph\.py\s+[a-z]`)

	// A shell handed a string to run. Only a shell qualifies: `grep -c` takes a
	// `-c` of its own and counts lines.
	shellC = regexp.MustCompile(`(?:^|[\s;|&(])(?:ba|z|da|k)?sh\s+(?:-[A-Za-z]+\s+)*-c\s+('[^']*'|"[^"]*"|\S+)`)

	heredocOpen = regexp.MustCompile(`<<-?\s*(['"]?)(\w+)(['"]?)\r?\n`)
)

func reachesBareTool(text string) bool {
	if bareSDD.MatchString(text) {
		return true
	}

	for _, match := range subshellSDD.FindAllStringIndex(text, -1) {
		if match[0] == 0 || !strings.ContainsRune(`/.-`, rune(text[match[0]-1])) && !isWordByte(text[match[0]-1]) {
			return true
		}
	}

	return false
}

func reachesTool(text string) bool {
	return reachesBareTool(text) || wrapperCall.MatchString(text)
}

func isWordByte(char byte) bool {
	return char == '_' || char >= '0' && char <= '9' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= 0x80
}

// stripHeredocs replaces every heredoc, from `<<` through its terminator line,
// with a placeholder. A body is data the shell hands to a program, so a graph
// read quoted inside one is prose rather than a call.
func stripHeredocs(command string) string {
	var kept strings.Builder

	rest := command
	for {
		open := heredocOpen.FindStringSubmatchIndex(rest)
		if open == nil {
			break
		}

		opening, word, closing := rest[open[2]:open[3]], rest[open[4]:open[5]], rest[open[6]:open[7]]
		end := -1

		if opening == closing {
			end = terminator(rest[open[1]:], word)
		}

		if end < 0 {
			kept.WriteString(rest[:open[0]+2])
			rest = rest[open[0]+2:]

			continue
		}

		kept.WriteString(rest[:open[0]])
		kept.WriteString("<<REDACTED\n")
		rest = rest[open[1]+end:]
	}

	kept.WriteString(rest)

	return kept.String()
}

// terminator finds the first line of body consisting of word alone, and
// returns the offset just past it, or -1.
func terminator(body string, word string) int {
	offset := 0
	for {
		line, _, found := strings.Cut(body[offset:], "\n")
		if line == word {
			return offset + len(word)
		}

		if !found {
			return -1
		}

		offset += len(line) + 1
	}
}

// quotedSpans lists every span the shell reads as text, scanned the way the
// shell scans one. A backslash escapes the character after it, so `\'` is an
// apostrophe rather than an opening quote, and a quote nothing closes opens no
// span.
func quotedSpans(command string) [][2]int {
	var spans [][2]int

	for index := 0; index < len(command); {
		char := command[index]
		if char == '\\' {
			index += 2

			continue
		}

		if char != '\'' && char != '"' {
			index++

			continue
		}

		closing := strings.IndexByte(command[index+1:], char)
		if closing < 0 {
			index++

			continue
		}

		end := index + 1 + closing + 1
		spans = append(spans, [2]int{index, end})
		index = end
	}

	return spans
}

func quotedAt(spans [][2]int, position int) bool {
	for _, span := range spans {
		if span[0] < position && position < span[1] {
			return true
		}
	}

	return false
}

// unquoted empties every quoted span. A quoted span is an argument, and a grep
// pattern holds pipes and parentheses that would otherwise read as syntax.
func unquoted(command string) string {
	var kept strings.Builder

	last := 0
	for _, span := range quotedSpans(command) {
		kept.WriteString(command[last:span[0]])
		kept.WriteString("''")
		last = span[1]
	}

	kept.WriteString(command[last:])

	return kept.String()
}

// shellOnly is the command as the shell reads it: no heredoc bodies, no
// quoted arguments.
func shellOnly(command string) string {
	return unquoted(stripHeredocs(command))
}

// carried lists every command string a shell inside this one would run. A
// `bash -c` quoted inside another argument is a mention, and stays one.
func carried(command string, depth int) []string {
	spans := quotedSpans(command)

	var found []string

	for _, match := range shellC.FindAllStringSubmatchIndex(command, -1) {
		if quotedAt(spans, match[0]) {
			continue
		}

		inner := command[match[2]:match[3]]
		if inner[0] == '\'' || inner[0] == '"' {
			inner = inner[1 : len(inner)-1]
		}

		found = append(found, inner)
		if depth < 3 {
			found = append(found, carried(inner, depth+1)...)
		}
	}

	return found
}
