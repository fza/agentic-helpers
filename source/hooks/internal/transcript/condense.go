package transcript

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const summaryLimit = 80

var summarizedInputs = []string{"file_path", "path", "notebook_path", "pattern", "command", "url"}

// dayLayout is a day header's line: the date the turn headers below it leave
// out, in UTC as their times are.
const dayLayout = "# 2006-01-02 UTC"

// Condense renders turns oldest first, each day's under its header.
func Condense(turns []Turn) string {
	var builder strings.Builder

	day := ""

	for _, turn := range turns {
		header := turn.At.UTC().Format(dayLayout)
		if header != day {
			builder.WriteString(header + "\n")
			day = header
		}

		builder.WriteString(turnBlock(turn))
	}

	return builder.String()
}

// turnBlock renders one turn as its number and time, then labelled parts,
// asked, replied and did, so an agent reading re-injected turns finds each
// part in the same place.
func turnBlock(turn Turn) string {
	lines := []string{fmt.Sprintf("## %d  %s", turn.Number, turn.At.UTC().Format("15:04"))}
	if turn.Asked != "" {
		lines = append(lines, "asked: "+turn.Asked)
	}

	if len(turn.Replied) > 0 {
		lines = append(lines, "replied: "+strings.Join(turn.Replied, "\n\n"))
	}

	if len(turn.Did) > 0 {
		var ordered []string

		for _, entry := range turn.Did {
			if !slices.Contains(ordered, entry) {
				ordered = append(ordered, entry)
			}
		}

		lines = append(lines, "did: "+strings.Join(ordered, " · "))
	}

	return strings.Join(lines, "\n") + "\n\n"
}

// isDayHeader is whether line, its line end aside, is a day header Condense
// writes.
func isDayHeader(line []rune) bool {
	_, err := time.Parse(dayLayout, strings.TrimSuffix(string(line), "\n"))

	return err == nil
}

func summarizeTool(name string, input map[string]json.RawMessage, cwd string) string {
	if name == "" {
		name = "?"
	}

	for _, key := range summarizedInputs {
		var value string

		err := json.Unmarshal(input[key], &value)
		if err != nil || value == "" {
			continue
		}

		if cwd != "" {
			value = strings.ReplaceAll(value, cwd+"/", "")
		}

		short := []rune(value)
		if len(short) > summaryLimit {
			short = append(short[:summaryLimit-3], []rune("...")...)
		}

		return name + " " + string(short)
	}

	return name
}
