package clock_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/clock"
)

var epoch = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func TestFakeNowStartsAtGivenTimeAndAdvances(t *testing.T) {
	c := clock.NewFake(epoch)

	assert.Equal(t, epoch, c.Now())

	c.Advance(90 * time.Second)

	assert.Equal(t, epoch.Add(90*time.Second), c.Now())
}

func TestFakeAfterFiresOnlyOnceDeadlinePassed(t *testing.T) {
	c := clock.NewFake(epoch)
	ch := c.After(time.Minute)

	c.Advance(59 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired before the deadline")
	default:
	}

	c.Advance(time.Second)
	select {
	case got := <-ch:
		assert.Equal(t, epoch.Add(time.Minute), got)
	default:
		t.Fatal("did not fire at the deadline")
	}
}

func TestFakeAfterWithNonPositiveDurationFiresImmediately(t *testing.T) {
	c := clock.NewFake(epoch)

	select {
	case <-c.After(0):
	default:
		t.Fatal("zero duration must fire immediately")
	}
}

func TestFakeSleepReturnsWhenAdvanced(t *testing.T) {
	c := clock.NewFake(epoch)
	done := make(chan error, 1)

	go func() { done <- c.Sleep(context.Background(), time.Minute) }()

	c.BlockUntilWaiters(1)
	c.Advance(time.Minute)

	require.NoError(t, <-done)
}

func TestFakeSleepReturnsContextError(t *testing.T) {
	c := clock.NewFake(epoch)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Sleep(ctx, time.Hour)

	require.ErrorIs(t, err, context.Canceled)
}

func TestRealClockMovesForward(t *testing.T) {
	c := clock.Real{}
	before := c.Now()

	require.NoError(t, c.Sleep(context.Background(), time.Millisecond))
	<-c.After(time.Millisecond)

	assert.True(t, c.Now().After(before))
}

func TestRealSleepReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := clock.Real{}.Sleep(ctx, time.Hour)

	require.ErrorIs(t, err, context.Canceled)
}
