package mailworld

import (
	"sync"
	"time"
)

// Clock is an injected, manually advanced clock. It starts at a fixed instant
// so seeded data is deterministic (STANDARDS.md section 2).
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *Clock {
	return &Clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

// Now returns the current fake time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance moves the clock forward. Negative durations are ignored.
func (c *Clock) Advance(d time.Duration) {
	if d < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
