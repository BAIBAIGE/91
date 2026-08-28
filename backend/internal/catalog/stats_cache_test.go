package catalog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatsCacheColdLoadIsSingleFlight(t *testing.T) {
	var cache statsCache[int]
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	t.Cleanup(cancelRefresh)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (int, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return 42, nil
	}

	type result struct {
		value int
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			value, err := cache.get(
				context.Background(), refreshContext, "test", "one", time.Hour, load,
			)
			results <- result{value: value, err: err}
		}()
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cold load did not start")
	}
	close(release)
	for range 2 {
		select {
		case got := <-results:
			if got.err != nil || got.value != 42 {
				t.Fatalf("cold result = %d, %v; want 42, nil", got.value, got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("cold caller did not finish")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cold loader calls = %d, want 1", got)
	}
}

func TestStatsCacheReturnsStaleAndRefreshesOnceOutsideRequestContext(t *testing.T) {
	var cache statsCache[int]
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	t.Cleanup(cancelRefresh)

	if value, err := cache.get(
		context.Background(), refreshContext, "test", "one", time.Hour,
		func(context.Context) (int, error) { return 1, nil },
	); err != nil || value != 1 {
		t.Fatalf("seed cache = %d, %v; want 1, nil", value, err)
	}
	cache.mu.Lock()
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.mu.Unlock()

	requestContext, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return 2, nil
	}

	value, err := cache.get(
		requestContext, refreshContext, "test", "one", time.Hour, load,
	)
	if err != nil || value != 1 {
		t.Fatalf("stale result = %d, %v; want 1, nil", value, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	for range 8 {
		value, err = cache.get(
			requestContext, refreshContext, "test", "one", time.Hour, load,
		)
		if err != nil || value != 1 {
			t.Fatalf("concurrent stale result = %d, %v; want 1, nil", value, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("background loader calls = %d, want 1", got)
	}

	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		cache.mu.Lock()
		refreshed := cache.value != nil && *cache.value == 2 && !cache.refreshing
		cache.mu.Unlock()
		if refreshed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not publish its result")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStatsCacheInvalidationRejectsAnOlderRefresh(t *testing.T) {
	var cache statsCache[int]
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	t.Cleanup(cancelRefresh)

	if _, err := cache.get(
		context.Background(), refreshContext, "test", "one", time.Hour,
		func(context.Context) (int, error) { return 1, nil },
	); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	cache.mu.Lock()
	cache.fetchedAt = time.Now().Add(-2 * time.Hour)
	cache.mu.Unlock()

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	load := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return 2, nil
		}
		return 3, nil
	}

	if value, err := cache.get(
		context.Background(), refreshContext, "test", "one", time.Hour, load,
	); err != nil || value != 1 {
		t.Fatalf("stale result = %d, %v; want 1, nil", value, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background refresh did not start")
	}
	cache.invalidate()
	close(release)

	value, err := cache.get(
		context.Background(), refreshContext, "test", "one", time.Hour, load,
	)
	if err != nil || value != 3 {
		t.Fatalf("post-invalidation result = %d, %v; want 3, nil", value, err)
	}
	cache.mu.Lock()
	stored := *cache.value
	cache.mu.Unlock()
	if stored != 3 {
		t.Fatalf("older refresh overwrote value with %d, want 3", stored)
	}
}

func TestCachedDriveAssetStatsAreClonedAndExplicitlyInvalidated(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	video := &Video{
		ID: "video", DriveID: "drive", FileID: "video.mp4", Title: "Video",
		Size: 100, PreviewStatus: "pending", PublishedAt: now,
	}
	if err := cat.UpsertVideo(ctx, video); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	stats, err := cat.CachedDriveAssetStats(ctx, time.Hour)
	if err != nil {
		t.Fatalf("load cached stats: %v", err)
	}
	if got := stats.Teasers["drive"]; got.Pending != 1 {
		t.Fatalf("initial teaser counts = %#v, want pending=1", got)
	}
	stats.Teasers["drive"] = DriveTeaserCounts{Ready: 99}
	stats, err = cat.CachedDriveAssetStats(ctx, time.Hour)
	if err != nil {
		t.Fatalf("reload cached stats: %v", err)
	}
	if got := stats.Teasers["drive"]; got.Pending != 1 || got.Ready != 0 {
		t.Fatalf("caller mutated cached teaser counts: %#v", got)
	}

	if err := cat.UpdatePreview(ctx, video.ID, "/tmp/video-preview.mp4", "ready"); err != nil {
		t.Fatalf("mark preview ready: %v", err)
	}
	stats, err = cat.CachedDriveAssetStats(ctx, time.Hour)
	if err != nil {
		t.Fatalf("read still-fresh cached stats: %v", err)
	}
	if got := stats.Teasers["drive"]; got.Pending != 1 {
		t.Fatalf("fresh cache unexpectedly changed before invalidation: %#v", got)
	}

	cat.InvalidateAssetStats()
	stats, err = cat.CachedDriveAssetStats(ctx, time.Hour)
	if err != nil {
		t.Fatalf("reload invalidated stats: %v", err)
	}
	if got := stats.Teasers["drive"]; got.Ready != 1 || got.Pending != 0 {
		t.Fatalf("invalidated teaser counts = %#v, want ready=1", got)
	}
}

func TestCountDriveAssetStatsKeepsCanonicalAndFingerprintScopes(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	for _, video := range []*Video{
		{
			ID: "canonical", DriveID: "drive", FileID: "canonical.mp4",
			Title: "Canonical", Size: 100, PreviewStatus: "pending", PublishedAt: now,
		},
		{
			ID: "duplicate", DriveID: "drive", FileID: "duplicate.mp4",
			Title: "Duplicate", Size: 200, ThumbnailURL: "/p/thumb/duplicate",
			PreviewStatus: "ready", PublishedAt: now,
		},
	} {
		if err := cat.UpsertVideo(ctx, video); err != nil {
			t.Fatalf("seed %s: %v", video.ID, err)
		}
	}
	if err := cat.UpdateVideoFingerprint(ctx, "duplicate", "sha-duplicate", "ready", ""); err != nil {
		t.Fatalf("mark duplicate fingerprint ready: %v", err)
	}
	if _, err := cat.db.ExecContext(ctx, `UPDATE videos SET is_canonical = 0 WHERE id = 'duplicate'`); err != nil {
		t.Fatalf("mark duplicate non-canonical: %v", err)
	}

	stats, err := cat.CountDriveAssetStats(ctx)
	if err != nil {
		t.Fatalf("count drive assets: %v", err)
	}
	if got := stats.Teasers["drive"]; got.Ready != 0 || got.Pending != 1 {
		t.Fatalf("teaser counts = %#v, want only canonical pending row", got)
	}
	if got := stats.Thumbnails["drive"]; got.Ready != 0 || got.Pending != 1 {
		t.Fatalf("thumbnail counts = %#v, want only canonical pending row", got)
	}
	if got := stats.Fingerprints["drive"]; got.Ready != 1 || got.Pending != 1 {
		t.Fatalf("fingerprint counts = %#v, want ready duplicate and pending canonical", got)
	}
}

func TestCachedCrawlerAssetStatsKeyCoversTheCompleteCrawlerSet(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	for _, video := range []*Video{
		{ID: "scriptcrawler-a-one", DriveID: "a", FileID: "a.mp4", Title: "A", Size: 1, PublishedAt: now},
		{ID: "scriptcrawler-b-one", DriveID: "b", FileID: "b1.mp4", Title: "B1", Size: 2, PublishedAt: now},
		{ID: "scriptcrawler-b-two", DriveID: "b", FileID: "b2.mp4", Title: "B2", Size: 3, PublishedAt: now},
	} {
		if err := cat.UpsertVideo(ctx, video); err != nil {
			t.Fatalf("seed %s: %v", video.ID, err)
		}
	}

	stats, err := cat.CachedCrawlerAssetStats(ctx, []CrawlerAssetStatsSpec{
		{ID: "a", Prefixes: []string{"scriptcrawler-a-"}},
	}, time.Hour)
	if err != nil {
		t.Fatalf("load crawler a stats: %v", err)
	}
	if got := stats["a"].Total; got != 1 {
		t.Fatalf("crawler a total = %d, want 1", got)
	}

	stats, err = cat.CachedCrawlerAssetStats(ctx, []CrawlerAssetStatsSpec{
		{ID: "b", Prefixes: []string{"scriptcrawler-b-"}},
	}, time.Hour)
	if err != nil {
		t.Fatalf("load crawler b stats: %v", err)
	}
	if got := stats["b"].Total; got != 2 {
		t.Fatalf("crawler b total = %d, want 2", got)
	}
	if _, exists := stats["a"]; exists {
		t.Fatal("crawler a leaked from a differently keyed snapshot")
	}
}
