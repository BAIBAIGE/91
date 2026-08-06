package scriptcrawler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestItemActivityTrackerReportsGrowingAtomicFile(t *testing.T) {
	tmp := t.TempDir()
	drv := New(Config{ID: "activity-test", RootDir: filepath.Join(tmp, "crawler")})
	activities := make(chan CrawlActivity, 32)
	crawler := NewCrawler(CrawlerConfig{
		Driver:              drv,
		ActivityLogInterval: 5 * time.Millisecond,
		OnActivity: func(activity CrawlActivity) {
			activities <- activity
		},
	})

	mediaPath := filepath.Join(tmp, "video.mp4")
	tracker := crawler.startItemActivity("source-1", Item{
		Title: "Slow HLS video",
		Media: MediaRef{URL: "https://example.com/video.m3u8"},
	}, mediaPath)
	if err := os.WriteFile(mediaPath+".part", make([]byte, 4096), 0o644); err != nil {
		tracker.Finish(false)
		t.Fatalf("write growing part file: %v", err)
	}

	deadline := time.After(time.Second)
	var heartbeat CrawlActivity
	for heartbeat.Event != CrawlActivityProgress || heartbeat.Bytes < 4096 {
		select {
		case heartbeat = <-activities:
		case <-deadline:
			tracker.Finish(false)
			t.Fatal("timed out waiting for download heartbeat")
		}
	}
	if heartbeat.Phase != CrawlPhaseDownloading || heartbeat.SourceID != "source-1" {
		t.Fatalf("heartbeat = %+v, want downloading source-1", heartbeat)
	}
	if heartbeat.Transport != "hls" || heartbeat.Title != "Slow HLS video" {
		t.Fatalf("heartbeat = %+v, want HLS title metadata", heartbeat)
	}
	if heartbeat.Elapsed <= 0 || heartbeat.ItemElapsed <= 0 {
		t.Fatalf("heartbeat elapsed = %s item elapsed = %s, want positive", heartbeat.Elapsed, heartbeat.ItemElapsed)
	}

	tracker.Transition(CrawlPhaseValidating, mediaPath, 4096)
	tracker.Finish(true)

	foundValidationStart := false
	foundValidationComplete := false
	for len(activities) > 0 {
		activity := <-activities
		if activity.Phase != CrawlPhaseValidating {
			continue
		}
		if activity.Event == CrawlActivityStarted {
			foundValidationStart = true
		}
		if activity.Event == CrawlActivityCompleted {
			foundValidationComplete = true
		}
	}
	if !foundValidationStart || !foundValidationComplete {
		t.Fatalf("validation events start=%v complete=%v, want both", foundValidationStart, foundValidationComplete)
	}
}

func TestNewCrawlerUsesBoundedActivityLogInterval(t *testing.T) {
	crawler := NewCrawler(CrawlerConfig{})
	if crawler.cfg.ActivityLogInterval != defaultActivityLogInterval {
		t.Fatalf("activity interval = %s, want %s", crawler.cfg.ActivityLogInterval, defaultActivityLogInterval)
	}
}
