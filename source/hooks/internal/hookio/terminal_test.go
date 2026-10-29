//go:build darwin || linux

package hookio_test

import (
	"os"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestTerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("opening a pipe: %v", err)
	}

	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening %s: %v", os.DevNull, err)
	}

	t.Cleanup(func() { _ = null.Close() })

	cases := []struct {
		name        string
		file        func(t *testing.T) *os.File
		interactive bool
	}{
		{name: "pipe", file: func(*testing.T) *os.File { return reader }},
		{name: "null device", file: func(*testing.T) *os.File { return null }},
		{name: "no file", file: func(*testing.T) *os.File { return nil }},
		{name: "pseudo-terminal", file: test.PseudoTerminal, interactive: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if (hookio.Terminal{File: tc.file(t)}).Interactive() != tc.interactive {
				t.Error("only a terminal should count as a person typing")
			}
		})
	}
}
