// Package ratelimit provides a limiter that pauses calls to an external
// system after it has reported that the rate limit is exceeded.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter tracks a pause requested by an external system (for example via
// 429 Too Many Requests with Retry-After). It is safe for concurrent use.
// The zero value is ready to use and not paused.
type Limiter struct {
	mu         sync.Mutex
	pauseUntil time.Time
}

// Hit registers a rate limit response asking to wait for d. A shorter pause
// never shrinks a longer one that is already in effect.
func (l *Limiter) Hit(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(d); until.After(l.pauseUntil) {
		l.pauseUntil = until
	}
}

// Paused reports whether calls should be held back right now.
func (l *Limiter) Paused() bool {
	return l.remaining() > 0
}

// Wait blocks until the pause is over or ctx is cancelled.
func (l *Limiter) Wait(ctx context.Context) {
	d := l.remaining()
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (l *Limiter) remaining() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Until(l.pauseUntil)
}
