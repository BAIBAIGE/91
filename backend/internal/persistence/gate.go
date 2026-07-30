// Package persistence coordinates the short critical sections that update
// SQLite rows together with files in the data directory.
//
// Normal writers take the shared side of Gate. A backup takes the exclusive
// side only while it creates the SQLite snapshot and hard-link file snapshot;
// hashing and ZIP compression happen after the gate is released.
package persistence

import "sync"

var gate sync.RWMutex

// RLock enters a normal persistence mutation critical section.
func RLock() {
	gate.RLock()
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
