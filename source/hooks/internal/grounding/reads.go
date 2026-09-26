package grounding

import (
	"regexp"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/graphconfig"
)

// reads is how this project's graph reads are spelled: through `sdd`, and
// through the command `.sdd/grounding.yaml` names as `read_command`, where it
// names one. command and suffix are what a refusal prints around a suggested
// read; tool is the pattern every read check matches.
type reads struct {
	command string
	suffix  string
	tool    string

	called         *regexp.Regexp
	subshellCalled *regexp.Regexp

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

	words := strings.Fields(config.ReadCommand)
	if len(words) > 0 {
		for index, word := range words {
			words[index] = regexp.QuoteMeta(word)
		}

		spelled := strings.Join(words, `\s+`)
		call := `(?:\S*/)?` + spelled + `\s+[a-z]`

		found.command = config.ReadCommand
		found.tool = `(?:sdd|` + spelled + `)`
		found.called = regexp.MustCompile(atCommand + call)
		found.subshellCalled = regexp.MustCompile(`\(\s*` + call)
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
