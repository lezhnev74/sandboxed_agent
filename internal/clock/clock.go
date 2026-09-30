// Package clock is the only place that reads the wall clock or waits on it.
// Everything else takes a Clock, so tests drive time with a Fake.
package clock

import (
	"context"
	"time"
)

// Clock reads and waits on time.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives the time once d has elapsed.
	After(d time.Duration) <-chan time.Time
	// Sleep blocks for d or until ctx is done, returning ctx's error then.
	Sleep(ctx context.Context, d time.Duration) error
}

// Real is the wall clock.
type Real struct{}

// Now returns the wall-clock time.
func (Real) Now() time.Time { return time.Now() }

// After waits on the wall clock.
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Sleep waits on the wall clock or ctx, whichever ends first.
func (r Real) Sleep(ctx context.Context, d time.Duration) error {
	return sleep(ctx, r.After(d))
}

func sleep(ctx context.Context, fired <-chan time.Time) error {
	select {
	case <-fired:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
