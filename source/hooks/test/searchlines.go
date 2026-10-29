package test

import "regexp"

var scoreAfterID = regexp.MustCompile(`(?m)^(\s*m-[a-z0-9]+) \d+%`)

// Unscored is a ranked search's text with the score after each line's id
// taken out, for a check about what a line says rather than how it ranked.
func Unscored(text string) string {
	return scoreAfterID.ReplaceAllString(text, "$1")
}
