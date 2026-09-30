package clock

import (
	"context"
	"sync"
	"time"
)

// Fake is a Clock that moves only when Advance is called.
type Fake struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake returns a Fake reading start.
func NewFake(start time.Time) *Fake {
	f := &Fake{now: start}
	f.cond = sync.NewCond(&f.mu)

	return f
}

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.now
}

// After returns a channel that fires once the fake time reaches now+d.
func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	ch := make(chan time.Time, 1)
	at := f.now.Add(d)

	if d <= 0 {
		ch <- at

		return ch
	}

	f.waiters = append(f.waiters, waiter{at: at, ch: ch})
	f.cond.Broadcast()

	return ch
}

// Sleep blocks until the fake time advances by d or ctx is done.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	return sleep(ctx, f.After(d))
}

// Advance moves the fake time forward and fires every waiter now due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.now = f.now.Add(d)

	pending := f.waiters[:0]
	for _, w := range f.waiters {
		if w.at.After(f.now) {
			pending = append(pending, w)

			continue
		}

		w.ch <- w.at
	}

	f.waiters = pending
}

// BlockUntilWaiters blocks until at least n waiters are pending, so a test
// can advance time only after the code under test started waiting.
func (f *Fake) BlockUntilWaiters(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for len(f.waiters) < n {
		f.cond.Wait()
	}
}
