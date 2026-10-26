package test

import (
	"context"
	"fmt"
	"time"
)

// CountdownClock is a clock a test moves: Wait returns at once, moving At on
// by the duration and noting it in Waited, unless the context has ended.
type CountdownClock struct {
	At     time.Time
	Waited []time.Duration
}

func (clock *CountdownClock) Now() time.Time {
	return clock.At
}

func (clock *CountdownClock) Wait(ctx context.Context, duration time.Duration) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("waiting: %w", err)
	}

	clock.Waited = append(clock.Waited, duration)
	clock.At = clock.At.Add(duration)

	return nil
}
