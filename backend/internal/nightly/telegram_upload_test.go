package nightly

import (
	"context"
	"errors"
	"testing"
)

func TestTelegramUploadRunsWithoutCrawlersButNotDuringScanAll(t *testing.T) {
	calls := 0
	r := New(Config{Settings: newStubSettings(), RunTelegramUpload: func(context.Context) error { calls++; return nil }})
	r.runPipeline(context.Background())
	if calls != 1 {
		t.Fatal("TG upload was skipped when no crawler exists")
	}
	r.runScanAllPipeline(context.Background())
	if calls != 1 {
		t.Fatal("scan all triggered a TG upload")
	}
}

func TestTelegramUploadFailureIsReportedAndCleanupContinues(t *testing.T) {
	cleaned := false
	r := New(Config{Settings: newStubSettings(), RunTelegramUpload: func(context.Context) error { return errors.New("upload failed") }, RunDedupeAssetCleanup: func(context.Context) error { cleaned = true; return nil }})
	r.runPipeline(context.Background())
	if !cleaned || len(r.issues) != 1 || r.issues[0].Stage != "telegram_upload" {
		t.Fatalf("failure handling: cleaned=%v issues=%v", cleaned, r.issues)
	}
}

func TestCanceledPipelineDoesNotUploadTelegramVideos(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := New(Config{Settings: newStubSettings(), RunTelegramUpload: func(context.Context) error { t.Fatal("uploaded after cancellation"); return nil }})
	r.runPipeline(ctx)
}
