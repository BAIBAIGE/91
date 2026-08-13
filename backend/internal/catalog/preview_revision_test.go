package catalog

import (
	"context"
	"testing"
	"time"
)

func TestPreviewRevisionChangesOnlyWithPreviewAsset(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() {
		if err := cat.Close(); err != nil {
			t.Fatalf("close catalog: %v", err)
		}
	})

	revision := time.UnixMilli(1_778_863_000_123)
	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:               "video-1",
		DriveID:          "drive-1",
		FileID:           "file-1",
		Title:            "Video",
		PreviewLocal:     "/tmp/video-1.mp4",
		PreviewStatus:    "ready",
		PreviewUpdatedAt: revision,
		PublishedAt:      now,
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	initial, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get initial video: %v", err)
	}
	if !initial.PreviewUpdatedAt.Equal(revision) {
		t.Fatalf("initial preview revision = %v, want %v", initial.PreviewUpdatedAt, revision)
	}

	if _, err := cat.IncrementView(ctx, "video-1"); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	afterMetadata, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after metadata update: %v", err)
	}
	if !afterMetadata.PreviewUpdatedAt.Equal(revision) {
		t.Fatalf("metadata update changed preview revision: got %v, want %v", afterMetadata.PreviewUpdatedAt, revision)
	}

	if err := cat.UpdatePreview(ctx, "video-1", "/tmp/video-1-v2.mp4", "ready"); err != nil {
		t.Fatalf("update preview: %v", err)
	}
	afterPreview, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after preview update: %v", err)
	}
	if !afterPreview.PreviewUpdatedAt.After(revision) {
		t.Fatalf("preview update revision = %v, want after %v", afterPreview.PreviewUpdatedAt, revision)
	}

	if err := cat.ClearGeneratedAssets(ctx, "video-1", true, false); err != nil {
		t.Fatalf("clear preview: %v", err)
	}
	afterClear, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after clear: %v", err)
	}
	if !afterClear.PreviewUpdatedAt.IsZero() {
		t.Fatalf("cleared preview revision = %v, want zero", afterClear.PreviewUpdatedAt)
	}
}
