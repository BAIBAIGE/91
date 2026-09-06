// Package tasklimit coordinates resource-intensive tasks across drive workers.
package tasklimit

import (
	"context"
	"sync"
)

// Limiter grants slots in arrival order. Changing the limit never interrupts
// running tasks; after a reduction, new tasks wait for enough slots to drain.
// A nil limiter imposes no limit, for callers without a shared resource budget.
type Limiter struct {
	mu      sync.Mutex
	limit   int
	active  int
	waiters []*waiter
}

type waiter struct {
	ready   chan struct{}
	granted bool
}

func New(limit int) *Limiter {
	l := &Limiter{}
	l.SetLimit(limit)
	return l
}

func (l *Limiter) SetLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
	l.dispatchLocked()
}

// Acquire waits without starting work. The returned release function is
// idempotent and must be called when work finishes, before retry delays.
func (l *Limiter) Acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil {
		return func() {}, nil
	}
	w := &waiter{ready: make(chan struct{})}
	l.mu.Lock()
	l.waiters = append(l.waiters, w)
	l.dispatchLocked()
	l.mu.Unlock()

	select {
	case <-ctx.Done():
	case <-w.ready:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Cancellation may race a grant. In either case return the slot, so a
	// stopped drive cannot leak capacity or start work after waiting.
	if err := ctx.Err(); err != nil {
		if w.granted {
			l.active--
		} else {
			for i, pending := range l.waiters {
				if pending == w {
					l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
					break
				}
			}
		}
		l.dispatchLocked()
		return nil, err
	}
	return sync.OnceFunc(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.active--
		l.dispatchLocked()
	}), nil
}

func (l *Limiter) dispatchLocked() {
	for l.active < l.limit && len(l.waiters) > 0 {
		w := l.waiters[0]
		l.waiters[0] = nil
		l.waiters = l.waiters[1:]
		w.granted = true
		l.active++
		close(w.ready)
	}
}
