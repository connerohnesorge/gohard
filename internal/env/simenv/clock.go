package simenv

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Clock is a virtual clock. Time moves only through Advance, AdvanceTo,
// Sleep and Fire; nothing ever waits. Timers fire in deadline order,
// ties broken by creation order, and each fire is delivered on the
// timer's buffered channel, so no send ever blocks.
type Clock struct {
	mu      sync.Mutex
	now     time.Time
	seq     uint64
	pending []*timer // sorted by (when, seq)
}

// NewClock returns a clock reading start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now implements env.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep implements env.Clock: it advances the clock by d at once,
// firing the timers that fall due, unless ctx is already done.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Advance(d)
	return nil
}

// NewTimer implements env.Clock. A timer with d <= 0 fires at once.
func (c *Clock) NewTimer(d time.Duration) env.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &timer{
		clock: c,
		when:  c.now.Add(max(d, 0)),
		seq:   c.seq,
		ch:    make(chan time.Time, 1),
	}
	if d <= 0 {
		t.ch <- c.now
		return t
	}
	i, _ := slices.BinarySearchFunc(c.pending, t, compareTimers)
	c.pending = slices.Insert(c.pending, i, t)
	return t
}

// Advance moves the clock forward by d (negative d is treated as 0),
// firing every timer due by the new time in order. While a timer fires,
// Now reads its deadline.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(max(d, 0))
	c.mu.Unlock()
	c.AdvanceTo(target)
}

// AdvanceTo moves the clock to t, firing due timers. A t in the past
// leaves the clock where it is.
func (c *Clock) AdvanceTo(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.pending) > 0 && !c.pending[0].when.After(t) {
		c.fireFirst()
	}
	if t.After(c.now) {
		c.now = t
	}
}

// Fire advances the clock to the earliest pending deadline and fires
// that one timer. It reports false when no timer is pending. Test
// drivers loop on it to run everything that was scheduled.
func (c *Clock) Fire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return false
	}
	c.fireFirst()
	return true
}

// Next returns the earliest pending deadline.
func (c *Clock) Next() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return time.Time{}, false
	}
	return c.pending[0].when, true
}

// Pending returns the number of timers that have not fired or stopped.
func (c *Clock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *Clock) fireFirst() {
	t := c.pending[0]
	c.pending = c.pending[1:]
	if t.when.After(c.now) {
		c.now = t.when
	}
	t.ch <- c.now // capacity 1, sent at most once
}

func compareTimers(a, b *timer) int {
	return cmp.Or(a.when.Compare(b.when), cmp.Compare(a.seq, b.seq))
}

type timer struct {
	clock *Clock
	when  time.Time
	seq   uint64
	ch    chan time.Time
}

func (t *timer) C() <-chan time.Time { return t.ch }

func (t *timer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	i, found := slices.BinarySearchFunc(c.pending, t, compareTimers)
	if !found {
		return false
	}
	c.pending = slices.Delete(c.pending, i, i+1)
	return true
}
