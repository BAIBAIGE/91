package persistence

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRLockContextStopsWaitingWhenCanceled(t *testing.T) {
	Lock()
	defer Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := RLockContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RLockContext error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("RLockContext did not stop promptly after cancellation")
	}
}
