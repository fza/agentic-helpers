package tools_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/internal/tools"
)

func TestTheMCPServerRunsItsIdleWorkWhileTheClientIsQuiet(t *testing.T) {
	h := newHarness(t)
	ran := make(chan struct{}, 1)

	env := h.env()
	env.Idle = mcpserver.Idle{After: 10 * time.Millisecond, Run: func(context.Context) {
		select {
		case ran <- struct{}{}:
		default:
		}
	}}

	requests, input := io.Pipe()
	done := make(chan int, 1)

	go func() {
		done <- tools.Main(context.Background(), []string{"mcp"}, env,
			hookio.Streams{In: requests, Out: io.Discard, Err: io.Discard})
	}()

	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Error("the server should run its idle work while no request arrives")
	}

	_ = input.Close()

	if code := <-done; code != 0 {
		t.Errorf("the server should end cleanly with its input, got %d", code)
	}
}
