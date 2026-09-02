package scanner

import (
	"errors"
	"sync"
	"time"
)

const (
	RateLimitCooldown   = 10 * time.Minute
	RateLimitRetryLimit = 3
)

var ErrRateLimitBudgetExhausted = errors.New("scanner rate-limit cooldown budget exhausted")

// RateLimitBudget is shared by every discovery performed for one drive task,
// including skip-policy legacy discovery and the normal scan. It prevents each
// directory or Scanner instance from receiving a fresh retry allowance.
type RateLimitBudget struct {
	mu      sync.Mutex
	retries int
}

func NewRateLimitBudget() *RateLimitBudget {
	return &RateLimitBudget{}
}

func (s *Scanner) retryBudget() *RateLimitBudget {
	if s.RateLimitBudget == nil {
		s.RateLimitBudget = NewRateLimitBudget()
	}
	return s.RateLimitBudget
}

func (b *RateLimitBudget) reserveRetry() (int, bool) {
	if b == nil {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.retries >= RateLimitRetryLimit {
		return b.retries, false
	}
	b.retries++
	return b.retries, true
}

func (b *RateLimitBudget) UsedRetries() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.retries
}
