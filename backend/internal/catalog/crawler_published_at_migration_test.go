package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAlignCrawlerPublishedAtWithCreatedAtOnce(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	createdAt := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	sourcePublishedAt := time.Date(2021, time.November, 5, 0, 0, 0, 0, time.UTC)
	seed := func(id string) {
		t.Helper()
		if err := cat.UpsertVideo(ctx, &Video{
			ID:          id,
			DriveID:     "target-drive",
			FileID:      id + ".mp4",
			FileName:    id + ".mp4",
			Title:       id,
			Size:        1,
			PublishedAt: sourcePublishedAt,
			CreatedAt:   createdAt,
			UpdatedAt:   createdAt,
		}); err != nil {
			t.Fatalf("upsert video %s: %v", id, err)
		}
	}

	seed("scriptcrawler-crawler-demo-prefixed")
	seed("legacy-crawler-canonical")
	seed("ordinary-video")
	seed("duplicate-canonical")
	if err := cat.MarkCrawlerSourceSeen(ctx, "scriptcrawler", "crawler-demo", "legacy-source", "imported", "legacy-crawler-canonical", "", 1); err != nil {
		t.Fatalf("mark imported crawler source: %v", err)
	}
	if err := cat.MarkCrawlerSourceSeen(ctx, "scriptcrawler", "crawler-demo", "duplicate-source", "duplicate", "duplicate-canonical", "", 1); err != nil {
		t.Fatalf("mark duplicate crawler source: %v", err)
	}
	if err := cat.SetSetting(ctx, settingCrawlerPublishedAtAligned, ""); err != nil {
		t.Fatalf("reset migration marker: %v", err)
	}

	aligned, err := cat.alignCrawlerPublishedAtWithCreatedAtOnce(ctx)
	if err != nil {
		t.Fatalf("align crawler timestamps: %v", err)
	}
	if aligned != 2 {
		t.Fatalf("aligned = %d, want 2", aligned)
	}

	assertPublishedAt := func(id string, want time.Time) {
		t.Helper()
		video, err := cat.GetVideo(ctx, id)
		if err != nil {
			t.Fatalf("get video %s: %v", id, err)
		}
		if !video.PublishedAt.Equal(want) {
			t.Fatalf("video %s published_at = %s, want %s", id, video.PublishedAt, want)
		}
	}
	assertPublishedAt("scriptcrawler-crawler-demo-prefixed", createdAt)
	assertPublishedAt("legacy-crawler-canonical", createdAt)
	assertPublishedAt("ordinary-video", sourcePublishedAt)
	assertPublishedAt("duplicate-canonical", sourcePublishedAt)

	aligned, err = cat.alignCrawlerPublishedAtWithCreatedAtOnce(ctx)
	if err != nil {
		t.Fatalf("rerun crawler timestamp migration: %v", err)
	}
	if aligned != 0 {
		t.Fatalf("second aligned = %d, want 0", aligned)
	}
}
