package ratelimit

import (
	"context"
	"sync"
	"time"
)

type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func New(calls int, per time.Duration) *Limiter {
	if calls <= 0 {
		calls = 1
	}
	return &Limiter{interval: per / time.Duration(calls)}
}

func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil || l.interval <= 0 {
		return nil
	}

	l.mu.Lock()
	now := time.Now()
	if l.next.IsZero() || now.After(l.next) {
		l.next = now
	}
	wait := l.next.Sub(now)
	l.next = l.next.Add(l.interval)
	l.mu.Unlock()

	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
