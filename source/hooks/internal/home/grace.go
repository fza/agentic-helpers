package home

import (
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/span"
)

// DefaultGrace is how long an unclaimed store waits for a checkout to promote
// it, as `orphan_store_grace` spells it: long enough for a project resting
// over a holiday to be opened again.
const DefaultGrace = "30d"

// Grace reads `orphan_store_grace`, a span as pluginconfig.Span reads one.
// Empty or unreadable takes DefaultGrace, and says so.
func Grace(text string) (time.Duration, bool) {
	if text == "" {
		held, _ := Grace(DefaultGrace)

		return held, true
	}

	held, err := span.Parse(text)
	if err != nil {
		held, _ = Grace(DefaultGrace)

		return held, false
	}

	return held, true
}
