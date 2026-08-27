package preview

import (
	"context"
	"testing"
)

func TestPreviewWorkersUseIndependentPerDriveConcurrency(t *testing.T) {
	const perDrive = 3
	workers := []*Worker{
		NewWorker(nil, nil, nil),
		NewWorker(nil, nil, nil),
	}

	active := 0
	for driveIndex, worker := range workers {
		worker.SetConcurrency(perDrive)
		for slot := 0; slot < perDrive; slot++ {
			if !worker.concurrency.acquire(context.Background()) {
				t.Fatalf("drive %d failed to acquire preview slot %d", driveIndex, slot)
			}
			active++
		}
	}
	defer func() {
		for _, worker := range workers {
			for slot := 0; slot < perDrive; slot++ {
				worker.concurrency.release()
			}
		}
	}()

	if want := len(workers) * perDrive; active != want {
		t.Fatalf("active preview slots = %d, want %d", active, want)
	}
	for driveIndex, worker := range workers {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		acquired := worker.concurrency.acquire(ctx)
		if acquired {
			worker.concurrency.release()
			t.Fatalf("drive %d acquired more than its own %d preview slots", driveIndex, perDrive)
		}
	}
}
