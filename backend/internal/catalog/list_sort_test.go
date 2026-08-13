package catalog

import (
	"context"
	"testing"
	"time"
)

func TestIncrementViewStoresLastViewedAt(t *testing.T) {
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

	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:          "video-1",
		DriveID:     "drive",
		FileID:      "file-1",
		Title:       "Video 1",
		PublishedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	if _, err := cat.IncrementView(ctx, "video-1"); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	got, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if got.Views != 1 {
		t.Fatalf("views = %d, want 1", got.Views)
	}
	if got.LastViewedAt.IsZero() {
		t.Fatal("last viewed time was not stored")
	}
}

func TestIncrementLikeStoresLastLikedAt(t *testing.T) {
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

	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:          "video-1",
		DriveID:     "drive",
		FileID:      "file-1",
		Title:       "Video 1",
		PublishedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	if _, err := cat.IncrementLike(ctx, "video-1"); err != nil {
		t.Fatalf("increment like: %v", err)
	}
	got, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if got.Likes != 1 {
		t.Fatalf("likes = %d, want 1", got.Likes)
	}
	if got.LastLikedAt.IsZero() {
		t.Fatal("last liked time was not stored")
	}

	if _, err := cat.DecrementLike(ctx, "video-1"); err != nil {
		t.Fatalf("decrement like: %v", err)
	}
	got, err = cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after unlike: %v", err)
	}
	if got.Likes != 0 {
		t.Fatalf("likes after unlike = %d, want 0", got.Likes)
	}
	if !got.LastLikedAt.IsZero() {
		t.Fatal("last liked time should be cleared when likes reaches zero")
	}
}

func TestThumbnailRevisionOnlyChangesWhenThumbnailChanges(t *testing.T) {
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

	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:           "video-1",
		DriveID:      "drive",
		FileID:       "file-1",
		Title:        "Video 1",
		ThumbnailURL: "/p/thumb/video-1",
		PublishedAt:  now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	initial, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get initial video: %v", err)
	}
	if initial.ThumbnailUpdatedAt.IsZero() {
		t.Fatal("thumbnail revision was not initialized")
	}

	if _, err := cat.IncrementView(ctx, "video-1"); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	if _, err := cat.IncrementLike(ctx, "video-1"); err != nil {
		t.Fatalf("increment like: %v", err)
	}
	afterMetadata, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after metadata updates: %v", err)
	}
	if !afterMetadata.ThumbnailUpdatedAt.Equal(initial.ThumbnailUpdatedAt) {
		t.Fatalf(
			"thumbnail revision changed with views/reactions: %v -> %v",
			initial.ThumbnailUpdatedAt,
			afterMetadata.ThumbnailUpdatedAt,
		)
	}

	if err := cat.UpdateVideoMeta(ctx, "video-1", VideoMetaPatch{
		ThumbnailURL: "/p/thumb/video-1",
	}); err != nil {
		t.Fatalf("rewrite thumbnail: %v", err)
	}
	afterThumbnail, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video after thumbnail update: %v", err)
	}
	if !afterThumbnail.ThumbnailUpdatedAt.After(initial.ThumbnailUpdatedAt) {
		t.Fatalf(
			"thumbnail revision did not advance: %v -> %v",
			initial.ThumbnailUpdatedAt,
			afterThumbnail.ThumbnailUpdatedAt,
		)
	}
}

func TestListVideosHotSortUsesLikesThenLastLikedAt(t *testing.T) {
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

	now := time.Now()
	for _, v := range []*Video{
		{ID: "newer-published", DriveID: "drive", FileID: "newer-published", Title: "Newer Published", PublishedAt: now.Add(4 * time.Hour), CreatedAt: now, UpdatedAt: now},
		{ID: "older-like", DriveID: "drive", FileID: "older-like", Title: "Older Like", PublishedAt: now.Add(3 * time.Hour), CreatedAt: now, UpdatedAt: now},
		{ID: "recent-like", DriveID: "drive", FileID: "recent-like", Title: "Recent Like", PublishedAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: "more-likes", DriveID: "drive", FileID: "more-likes", Title: "More Likes", PublishedAt: now.Add(-time.Hour), CreatedAt: now, UpdatedAt: now},
	} {
		if err := cat.UpsertVideo(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", v.ID, err)
		}
	}
	if _, err := cat.db.ExecContext(ctx,
		`UPDATE videos SET
			likes = CASE id
				WHEN 'more-likes' THEN 3
				WHEN 'recent-like' THEN 2
				WHEN 'older-like' THEN 2
				WHEN 'newer-published' THEN 2
				ELSE likes
			END,
			last_liked_at = CASE id
				WHEN 'more-likes' THEN ?
				WHEN 'recent-like' THEN ?
				WHEN 'older-like' THEN ?
				WHEN 'newer-published' THEN 0
				ELSE last_liked_at
			END`,
		now.Add(-2*time.Hour).UnixMilli(),
		now.Add(2*time.Hour).UnixMilli(),
		now.Add(-time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("seed likes: %v", err)
	}

	items, _, err := cat.ListVideos(ctx, ListParams{Sort: "hot", Page: 1, PageSize: 4})
	if err != nil {
		t.Fatalf("list hot videos: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	got := []string{items[0].ID, items[1].ID, items[2].ID, items[3].ID}
	want := []string{"more-likes", "recent-like", "older-like", "newer-published"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hot order = %#v, want %#v", got, want)
		}
	}
}

func TestListVideosRecentSortUsesLastViewedAt(t *testing.T) {
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

	now := time.Now()
	for _, v := range []*Video{
		{ID: "old-view", DriveID: "drive", FileID: "old-view", Title: "Old View", PublishedAt: now.Add(3 * time.Hour), CreatedAt: now, UpdatedAt: now},
		{ID: "recent-view", DriveID: "drive", FileID: "recent-view", Title: "Recent View", PublishedAt: now, CreatedAt: now, UpdatedAt: now},
		{ID: "unviewed", DriveID: "drive", FileID: "unviewed", Title: "Unviewed", PublishedAt: now.Add(4 * time.Hour), CreatedAt: now, UpdatedAt: now},
	} {
		if err := cat.UpsertVideo(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", v.ID, err)
		}
	}
	if _, err := cat.db.ExecContext(ctx,
		`UPDATE videos SET last_viewed_at = CASE id
			WHEN 'old-view' THEN ?
			WHEN 'recent-view' THEN ?
			ELSE 0
		END`,
		now.Add(-time.Hour).UnixMilli(),
		now.Add(time.Hour).UnixMilli(),
	); err != nil {
		t.Fatalf("seed last_viewed_at: %v", err)
	}

	items, _, err := cat.ListVideos(ctx, ListParams{Sort: "recent", Page: 1, PageSize: 3})
	if err != nil {
		t.Fatalf("list recent videos: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	got := []string{items[0].ID, items[1].ID, items[2].ID}
	want := []string{"recent-view", "old-view", "unviewed"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recent order = %#v, want %#v", got, want)
		}
	}
}
