package backuptransfer

import (
	"testing"
	"time"
)

func TestStreamProgressReportsInflightBytesAndRecentThroughput(t *testing.T) {
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	progress := newStreamProgress(100, start)
	progress.begin(1)
	progress.add(1, 400, start.Add(time.Second))
	processed, rate, _ := progress.snapshot(start.Add(2*time.Second), 1000)
	if processed != 500 {
		t.Fatalf("processed = %d, want 500", processed)
	}
	if rate != 200 {
		t.Fatalf("rate = %d, want 200", rate)
	}
	progress.commit(1, 500, start.Add(2*time.Second))
	processed, rate, _ = progress.snapshot(start.Add(5*time.Second), 1000)
	if processed != 500 || rate != 0 {
		t.Fatalf("idle snapshot = (%d, %d), want (500, 0)", processed, rate)
	}
}
