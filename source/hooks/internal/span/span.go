// Package span reads and spells lengths of time as every agentic-helpers
// option and `agentic-memory lifetime` take them.
package span

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var ErrInvalid = errors.New("is no span")

var pattern = regexp.MustCompile(`^([0-9]+)([smhdw])$`)

var units = map[string]time.Duration{
	"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour,
}

// Parse reads a length of time: `0`, a whole count of seconds, minutes, hours, days or
// weeks (`90d`), or a Go duration as sdd prints one (`1m0s`). A negative
// length is none.
func Parse(text string) (time.Duration, error) {
	if text == "0" {
		return 0, nil
	}

	parts := pattern.FindStringSubmatch(text)
	if parts != nil {
		count, err := strconv.ParseInt(parts[1], 10, 64)
		unit := units[parts[2]]

		if err != nil || count > int64(time.Duration(1<<63-1)/unit) {
			return 0, fmt.Errorf("%q %w", text, ErrInvalid)
		}

		return time.Duration(count) * unit, nil
	}

	held, err := time.ParseDuration(text)
	if err != nil || held < 0 {
		return 0, fmt.Errorf("%q %w", text, ErrInvalid)
	}

	return held, nil
}

// Format spells held in its largest whole unit, `90d` rather than
// `2160h`, so a value reads back as it was most likely set. Weeks are left
// out: `14d` reads plainer than `2w`.
func Format(held time.Duration) string {
	if held == 0 {
		return "0"
	}

	for _, unit := range []string{"d", "h", "m", "s"} {
		size := units[unit]
		if held%size == 0 {
			return strconv.FormatInt(int64(held/size), 10) + unit
		}
	}

	return held.String()
}
