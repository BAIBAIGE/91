package api

import (
	"testing"
	"time"
)

func TestAssetStatsFreshnessTracksApplicationWork(t *testing.T) {
	idle := (&AdminServer{}).assetStatsFreshFor(nil)
	if idle != 30*time.Second {
		t.Fatalf("idle freshness = %s, want 30s", idle)
	}

	busy := (&AdminServer{}).assetStatsFreshFor(map[string]DriveGenerationStatuses{
		"drive": {Thumbnail: GenerationStatus{State: "generating"}},
	})
	if busy != 3*time.Second {
		t.Fatalf("generation freshness = %s, want 3s", busy)
	}

	nightly := (&AdminServer{
		GetNightlyJobStatus: func() NightlyJobStatus {
			return NightlyJobStatus{State: "queued", Queued: true}
		},
	}).assetStatsFreshFor(nil)
	if nightly != 3*time.Second {
		t.Fatalf("nightly freshness = %s, want 3s", nightly)
	}
}
