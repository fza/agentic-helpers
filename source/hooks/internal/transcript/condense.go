package transcript

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const summaryLimit = 80

var summarizedInputs = []string{"file_path", "path", "notebook_path", "pattern", "command", "url"}

func Condense(turns []Turn) string {
	var builder strings.Builder

	for _, turn := range turns {
		builder.WriteString(turnBlock(turn))
	}

	return builder.String()
}

// turnBlock renders one turn in fixed sections, asked, replied and did, so an
// agent reading re-injected turns finds each part in the same place.
func turnBlock(turn Turn) string {
	lines := []string{fmt.Sprintf("## turn %d  %s", turn.Number, turn.At.UTC().Format("2006-01-02 15:04 UTC")), ""}
	if turn.Asked != "" {
		lines = append(lines, "### asked", "", turn.Asked, "")
	}

	if len(turn.Replied) > 0 {
		lines = append(lines, "### replied", "", strings.Join(turn.Replied, "\n\n"), "")
	}

	if len(turn.Did) > 0 {
		var ordered []string

		for _, entry := range turn.Did {
			if !slices.Contains(ordered, entry) {
				ordered = append(ordered, entry)
			}
		}

		lines = append(lines, "### did", "", strings.Join(ordered, " · "), "")
	}

	return strings.Join(lines, "\n") + "\n"
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
