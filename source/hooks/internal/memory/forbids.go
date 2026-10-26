package memory

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

var ErrForbidsOnRule = errors.New("--forbids is for a rule the user gave: what never belongs in memory")

// forbidsFor checks a filing's --forbids patterns: a rule's alone, each RE2.
func forbidsFor(kind string, given []string) ([]string, error) {
	if len(given) > 0 && kind != kindRule {
		return nil, ErrForbidsOnRule
	}

	var patterns []string

	for _, pattern := range given {
		_, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("--forbids %q is not an RE2 pattern: %w", pattern, err)
		}

		if !slices.Contains(patterns, pattern) {
			patterns = append(patterns, pattern)
		}
	}

	return patterns, nil
}

// forbidden is the warning for every live rule the filing seat's brief would
// show whose patterns the entry's title or body matches. The entry has landed
// either way: a loose pattern must not block a real entry.
func (mem *memory) forbidden(folded *state, held *entry, seatName string) []string {
	var lines []string

	for _, rule := range mem.ownLive(folded, seatName) {
		if rule.kind != kindRule {
			continue
		}

		for _, text := range rule.forbids {
			pattern, err := regexp.Compile(text)
			if err != nil {
				continue
			}

			matched := pattern.FindString(held.title + "\n" + held.body)
			if matched == "" {
				continue
			}

			lines = append(lines, fmt.Sprintf("forbidden by %s %s", rule.id, rule.title),
				fmt.Sprintf("  matched: %q", matched),
				fmt.Sprintf("  → %s retire %s --because 'forbidden by %s'", self, held.id, rule.id))

			break
		}
	}

	return lines
}
