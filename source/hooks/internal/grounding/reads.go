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
	// searchFlags keep a suggested search to five header lines; only bare sdd
	// gets them, since a project's own read command need not take sdd's flags.
	searchFlags string
	// streamFlags are the stream refusal's example search flags.
	streamFlags string

	commandWords []string
	gatherWords  []string

	search     *regexp.Regexp
	view       *regexp.Regexp
	semantic   *regexp.Regexp
	literal    *regexp.Regexp
	show       *regexp.Regexp
	everyTopic *regexp.Regexp
}

// bareSearchFlags is how a suggested search reads for bare sdd: five hits,
// header lines only.
const bareSearchFlags = " --limit 5 --max-citations 0"

func readsOf(config graphconfig.Config) reads {
	found := reads{command: "sdd", tool: "sdd", searchFlags: bareSearchFlags, streamFlags: bareSearchFlags}

	if config.ReadCommand != "" || config.GatherCommand != "" {
		found.searchFlags, found.streamFlags = "", " --limit 8"
	}

	if config.ReadSuffix != "" {
		found.suffix = " " + config.ReadSuffix
	}

	if config.ReadCommand != "" {
		found.command = config.ReadCommand
		found.tool = `(?:sdd|` + spelledOut(config.ReadCommand) + `)`
		found.commandWords = strings.Fields(config.ReadCommand)
	}

	if config.GatherCommand != "" {
		found.gather = config.GatherCommand
		found.gatherWords = strings.Fields(config.GatherCommand)
	}

	found.search = regexp.MustCompile(found.tool + `\s+search\b`)
	found.view = regexp.MustCompile(found.tool + `\s+view\b`)
	found.semantic = regexp.MustCompile(found.tool + `\s+search\b[^\n]*--query\b`)
	found.literal = regexp.MustCompile(found.tool + `\s+search\b[^\n]*--term\b`)
	found.show = regexp.MustCompile(found.tool + `\s+show\b[^\n|;&]*`)
	found.everyTopic = regexp.MustCompile(found.tool + `\s+view\b[^\n]*\bactive:as-counts\b`)

	return found
}

// reachedBy reports whether the call runs the graph's tool, bare or through
// the project's read command, naming a subcommand.
func (reads reads) reachedBy(called call) bool {
	rest, isBare := callsAs(called, []string{"sdd"})
	if isBare && opensSubcommand(rest) {
		return true
	}

	if reads.commandWords == nil {
		return false
	}

	rest, isRead := callsAs(called, reads.commandWords)

	return isRead && opensSubcommand(rest)
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

// gatheringsIn lists every gather call among the calls, its flags read as
// the shell splits them, since an area name or a phrase may be quoted.
func (reads reads) gatheringsIn(calls []call) []gathering {
	if reads.gatherWords == nil {
		return nil
	}

	var found []gathering

	for _, called := range calls {
		rest, gathers := callsAs(called, reads.gatherWords)
		if gathers {
			found = append(found, gatheringOf(rest))
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
