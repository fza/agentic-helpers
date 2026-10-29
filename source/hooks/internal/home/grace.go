package home

import (
	"regexp"
	"strconv"
	"time"
)

// DefaultGrace is how long an unclaimed store waits for a checkout to promote
// it, as `orphan_store_grace` spells it: long enough for a project resting
// over a holiday to be opened again.
const DefaultGrace = "30d"

var gracePattern = regexp.MustCompile(`^([0-9]+)([mhd])$`)

// Grace reads `orphan_store_grace`: `0`, or a count of minutes, hours or
// days. Empty or unreadable takes DefaultGrace, and says so.
func Grace(text string) (time.Duration, bool) {
	if text == "" {
		held, _ := Grace(DefaultGrace)

		return held, true
	}

	if text == "0" {
		return 0, true
	}

	parts := gracePattern.FindStringSubmatch(text)
	if parts == nil {
		held, _ := Grace(DefaultGrace)

		return held, false
	}

	count, err := strconv.Atoi(parts[1])
	if err != nil {
		held, _ := Grace(DefaultGrace)

		return held, false
	}

	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[parts[2]]

	return time.Duration(count) * unit, true
}
