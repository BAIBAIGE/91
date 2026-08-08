// Package persistence coordinates the short critical sections that update
// SQLite rows together with files in the data directory.
//
// Normal writers take the shared side of Gate. A backup takes the exclusive
// side only while it creates the SQLite snapshot and hard-link file snapshot;
// hashing and ZIP compression happen after the gate is released.
package persistence

import (
	"context"
	"sync"
	"time"
)

var gate sync.RWMutex

// RLock enters a normal persistence mutation critical section.
func RLock() {
	gate.RLock()
}

// RLockContext enters a normal mutation section without trapping a canceled
// background worker behind a backup/restore barrier. Callers must still pair a
// successful return with RUnlock.
func RLockContext(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if gate.TryRLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// RUnlock leaves a normal persistence mutation critical section.
func RUnlock() {
	gate.RUnlock()
}

// Lock enters the short, exclusive snapshot critical section.
func Lock() {
	gate.Lock()
}

// Unlock leaves the exclusive snapshot critical section.
func Unlock() {
	gate.Unlock()
}

// WithMutation runs fn while backups cannot establish a new snapshot.
func WithMutation(fn func() error) error {
	RLock()
	defer RUnlock()
	return fn()
}
