package grounding

import (
	"regexp"
	"slices"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
)

// reads is how this project's graph reads are spelled: through `sdd`, and
// through the command `.sdd/grounding.yaml` names as `read_command`, where it
// names one. command and suffix are what a refusal prints around a suggested
// read; tool is the pattern every read check matches. gather is the command
// running every read a turn owes in one call, where `gather_command` names one.
type reads struct {
	command string
	suffix  string
	tool    string
	gather  string

	called         *regexp.Regexp
	subshellCalled *regexp.Regexp
	gathers        *regexp.Regexp
	subshellGather *regexp.Regexp

	search     *regexp.Regexp
	view       *regexp.Regexp
	semantic   *regexp.Regexp
	literal    *regexp.Regexp
	show       *regexp.Regexp
	everyTopic *regexp.Regexp
}

func readsOf(config graphconfig.Config) reads {
	found := reads{command: "sdd", tool: "sdd"}

	if config.ReadSuffix != "" {
		found.suffix = " " + config.ReadSuffix
	}

	if config.ReadCommand != "" {
		spelled := spelledOut(config.ReadCommand)
		call := `(?:\S*/)?` + spelled + `\s+[a-z]`

		found.command = config.ReadCommand
		found.tool = `(?:sdd|` + spelled + `)`
		found.called = regexp.MustCompile(atCommand + call)
		found.subshellCalled = regexp.MustCompile(`\(\s*` + call)
	}

	if config.GatherCommand != "" {
		spelled := `((?:\S*/)?` + spelledOut(config.GatherCommand) + `)(?:[\s;&|)]|$)`

		found.gather = config.GatherCommand
		found.gathers = regexp.MustCompile(atCommand + spelled)
		found.subshellGather = regexp.MustCompile(`\(\s*` + spelled)
	}

	found.search = regexp.MustCompile(found.tool + `\s+search\b`)
	found.view = regexp.MustCompile(found.tool + `\s+view\b`)
	found.semantic = regexp.MustCompile(found.tool + `\s+search\b[^\n]*--query\b`)
	found.literal = regexp.MustCompile(found.tool + `\s+search\b[^\n]*--term\b`)
	found.show = regexp.MustCompile(found.tool + `\s+show\b[^\n|;&]*`)
	found.everyTopic = regexp.MustCompile(found.tool + `\s+view\b[^\n]*\bactive:as-counts\b`)

	return found
}

// reachedBy reports whether the text calls the graph's tool, bare or through
// the project's read command, where a command word sits.
func (reads reads) reachedBy(text string) bool {
	if reachesBareTool(text) {
		return true
	}

	if reads.called == nil {
		return false
	}

	return reads.called.MatchString(text) || opensSubshell(reads.subshellCalled, text)
}

// spelledOut is the pattern of a command however the shell spaces its words.
func spelledOut(command string) string {
	words := strings.Fields(command)
	for index, word := range words {
		words[index] = regexp.QuoteMeta(word)
	}

	return strings.Join(words, `\s+`)
}

// gathering is what one gather call asked for: which search modes, which areas
// by name, and the subject it grounds.
type gathering struct {
	query   bool
	term    bool
	areas   []string
	subject string
}

// gatheringsIn lists every gather call the command makes. A call counts where
// the shell reads a command word, never inside a quoted argument or a heredoc
// body, and its flags are read as the shell splits them, since an area name or
// a phrase may be quoted.
func (reads reads) gatheringsIn(command string) []gathering {
	if reads.gathers == nil {
		return nil
	}

	text := stripHeredocs(command)
	spans := quotedSpans(text)

	var found []gathering

	for _, pattern := range []*regexp.Regexp{reads.gathers, reads.subshellGather} {
		for _, match := range pattern.FindAllStringSubmatchIndex(text, -1) {
			if quotedAt(spans, match[2]) || pattern == reads.subshellGather && !atSubshell(text, match[0]) {
				continue
			}

			found = append(found, gatheringOf(shellWords(text[match[3]:])))
		}
	}

	return found
}

func gatheringOf(words []string) gathering {
	var found gathering

	for index := 0; index < len(words); index++ {
		flag, value, inline := strings.Cut(words[index], "=")
		if !slices.Contains([]string{"--query", "--term", "--area", "--entry"}, flag) {
			continue
		}

		if !inline && index+1 < len(words) {
			index++
			value = words[index]
		}

		switch flag {
		case "--query":
			found.query = true
		case "--term":
			found.term = true
		case "--area":
			found.areas = append(found.areas, value)
		default:
			found.subject = value
		}
	}

	return found
}

// shellWords splits the text into the words the shell hands one command,
// quotes removed, up to the first operator ending that command.
func shellWords(text string) []string {
	var (
		words []string
		word  strings.Builder
	)

	inWord := false
	flush := func() {
		if inWord {
			words = append(words, word.String())
		}

		word.Reset()
		inWord = false
	}

	for index := 0; index < len(text); index++ {
		char := text[index]

		switch {
		case char == '\\' && index+1 < len(text):
			index++
			word.WriteByte(text[index])
			inWord = true
		case char == '\'' || char == '"':
			closing := strings.IndexByte(text[index+1:], char)
			if closing < 0 {
				word.WriteByte(char)
			} else {
				word.WriteString(text[index+1 : index+1+closing])
				index += closing + 1
			}

			inWord = true
		case char == ' ' || char == '\t':
			flush()
		case strings.IndexByte("\n;&|)", char) >= 0:
			flush()

			return words
		default:
			word.WriteByte(char)
			inWord = true
		}
	}

	flush()

	return words
}
