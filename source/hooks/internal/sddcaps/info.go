package sddcaps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Framing is what `sdd info --format json` says about the graph it runs on.
// Capabilities is nil where the graph is on a schema newer than the binary,
// which then cannot tell what writes to it support.
type Framing struct {
	GraphSchema  int           `json:"graph_schema"`
	Capabilities *Capabilities `json:"capabilities"`
}

// Capabilities is what writes to the graph support. Titles is whether an
// entry takes a title.
type Capabilities struct {
	Titles bool `json:"titles"`
}

// Info runs `sdd info --format json` from dir, through an sdd whose probe found
// InfoJSON.
func Info(ctx context.Context, runner Runner, dir string) (Framing, error) {
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	argv := []string{"sdd", "info", "--format", "json"}

	result, err := runner.Run(bounded, dir, argv)
	if err != nil {
		return Framing{}, fmt.Errorf("`%s` printed nothing (%w)", strings.Join(argv, " "), err)
	}

	if result.Code != 0 {
		return Framing{}, fmt.Errorf("`%s` printed %q and exited %d", strings.Join(argv, " "),
			strings.TrimSpace(result.Stdout+result.Stderr), result.Code)
	}

	var framing Framing

	err = json.Unmarshal([]byte(result.Stdout), &framing)
	if err != nil {
		return Framing{}, fmt.Errorf("reading `%s`: %w", strings.Join(argv, " "), err)
	}

	return framing, nil
}
