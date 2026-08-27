package preview

import (
	"context"
	"sync"
)

// workerConcurrency limits in-flight preview tasks for one Worker. Every
// drive owns a separate instance, while the configured limit is copied to all
// workers by the application. Updating the limit never cancels work already in
// flight; a lower value takes effect as those tasks finish.
type workerConcurrency struct {
	mu      sync.Mutex
	limit   int
	active  int
	changed chan struct{}
}

func (c *workerConcurrency) setLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	c.mu.Lock()
	if c.limit != limit {
		c.limit = limit
		c.notifyLocked()
	}
	c.mu.Unlock()
}

func (c *workerConcurrency) currentLimit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limit < 1 {
		return 1
	}
	return c.limit
}

func (c *workerConcurrency) acquire(ctx context.Context) bool {
	for {
		c.mu.Lock()
		limit := c.limit
		if limit < 1 {
			limit = 1
		}
		if c.active < limit {
			c.active++
			c.mu.Unlock()
			return true
		}
		if c.changed == nil {
			c.changed = make(chan struct{})
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
}

func (c *workerConcurrency) release() {
	c.mu.Lock()
	if c.active > 0 {
		c.active--
	}
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *workerConcurrency) notifyLocked() {
	if c.changed != nil {
		close(c.changed)
	}
	c.changed = make(chan struct{})
}
