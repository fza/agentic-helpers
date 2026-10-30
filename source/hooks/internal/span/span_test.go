package span_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/span"
)

func TestParseReadsEveryForm(t *testing.T) {
	cases := []struct {
		text string
		want time.Duration
	}{
		{text: "0", want: 0},
		{text: "120s", want: 2 * time.Minute},
		{text: "90m", want: 90 * time.Minute},
		{text: "12h", want: 12 * time.Hour},
		{text: "90d", want: 90 * 24 * time.Hour},
		{text: "2w", want: 14 * 24 * time.Hour},
		{text: "1m0s", want: time.Minute},
		{text: "1h30m", want: 90 * time.Minute},
		{text: "2m", want: 2 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			held, err := span.Parse(tc.text)
			if err != nil {
				t.Fatalf("a span should be read: %v", err)
			}

			if held != tc.want {
				t.Fatalf("the span should read as its length, got %v", held)
			}
		})
	}
}

func TestParseRefusesWhatIsNoSpan(t *testing.T) {
	for _, text := range []string{"", "120", "-5m", "5y", "d", "1.5d", "two days", "99999999999999999999d", "9999999999d"} {
		t.Run(text, func(t *testing.T) {
			_, err := span.Parse(text)
			if !errors.Is(err, span.ErrInvalid) {
				t.Fatalf("a text that is no span should be refused as one, got %v", err)
			}
		})
	}
}

func TestFormatSpellsTheLargestWholeUnit(t *testing.T) {
	cases := []struct {
		held time.Duration
		want string
	}{
		{held: 0, want: "0"},
		{held: 90 * 24 * time.Hour, want: "90d"},
		{held: 14 * 24 * time.Hour, want: "14d"},
		{held: 36 * time.Hour, want: "36h"},
		{held: 2 * time.Minute, want: "2m"},
		{held: 45 * time.Second, want: "45s"},
		{held: 1500 * time.Millisecond, want: "1.5s"},
	}

	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			text := span.Format(tc.held)
			if text != tc.want {
				t.Fatalf("the length should be spelled in its largest whole unit, got %q", text)
			}

			if tc.held%time.Second != 0 {
				return
			}

			back, err := span.Parse(text)
			if err != nil || back != tc.held {
				t.Fatalf("a spelled span should read back as the same length, got %v, %v", back, err)
			}
		})
	}
}
