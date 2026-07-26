package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/mediasim"
)

func syntheticFrames(seed int64) [][]byte {
	rng := rand.New(rand.NewSource(seed))
	frames := make([][]byte, mediasim.FrameSignatureMaxFrames)
	for i := range frames {
		frame := make([]byte, mediasim.FrameSignatureGridSize*mediasim.FrameSignatureGridSize)
		for j := range frame {
			frame[j] = byte(rng.Intn(256))
		}
		frames[i] = frame
	}
	return frames
}

func TestCleanupContentDuplicateVideos(t *testing.T) {
	ctx := context.Background()
	localDir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	type seedVideo struct {
		id       string
		size     int64
		duration int
		sigSeed  int64
	}
	// original/compressed 内容相同（帧签名同源），other 不同内容但同时长，
	// short 内容相同但时长低于内容通道门槛。
	seeds := []seedVideo{
		{id: "video-original", size: 977_000_000, duration: 1129, sigSeed: 1},
		{id: "video-compressed", size: 129_000_000, duration: 1129, sigSeed: 1},
		{id: "video-other", size: 500_000_000, duration: 1130, sigSeed: 2},
		{id: "video-short", size: 400_000_000, duration: 60, sigSeed: 1},
	}
	sigByPath := map[string]*mediasim.FrameSignature{}
	videos := make([]*catalog.Video, 0, len(seeds))
	for i, s := range seeds {
		teaser := filepath.Join(localDir, s.id+".mp4")
		if err := os.WriteFile(teaser, []byte("teaser"), 0o644); err != nil {
			t.Fatalf("write teaser: %v", err)
		}
		base := syntheticFrames(s.sigSeed)
		frames := make([][]byte, len(base))
		for k, f := range base {
			frame := make([]byte, len(f))
			copy(frame, f)
			// 每个视频叠加各自的轻微噪声，模拟不同压制。
			rng := rand.New(rand.NewSource(int64(i*1000 + k)))
			for j := range frame {
				delta := rng.Intn(5) - 2
				v := int(frame[j]) + delta
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				frame[j] = byte(v)
			}
			frames[k] = frame
		}
		sigByPath[teaser] = &mediasim.FrameSignature{Frames: frames}

		v := &catalog.Video{
			ID:              s.id,
			DriveID:         "115",
			FileID:          "file-" + s.id,
			Title:           "标题 " + s.id,
			Size:            s.size,
			DurationSeconds: s.duration,
			PreviewLocal:    teaser,
			PreviewStatus:   "ready",
			PublishedAt:     now.Add(time.Duration(i) * time.Second),
			CreatedAt:       now.Add(time.Duration(i) * time.Second),
			UpdatedAt:       now.Add(time.Duration(i) * time.Second),
		}
		if err := cat.UpsertVideo(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", s.id, err)
		}
		if err := cat.UpdateVideoFingerprint(ctx, s.id, fmt.Sprintf("%064d", i), "ready", ""); err != nil {
			t.Fatalf("fingerprint %s: %v", s.id, err)
		}
		videos = append(videos, v)
	}

	restore := contentSignatureExtractor
	contentSignatureExtractor = func(ctx context.Context, ffmpegPath, teaserPath string) (*mediasim.FrameSignature, error) {
		sig, ok := sigByPath[teaserPath]
		if !ok {
			return nil, fmt.Errorf("unexpected teaser path %s", teaserPath)
		}
		return sig, nil
	}
	t.Cleanup(func() { contentSignatureExtractor = restore })

	app := &App{
		cfg: &config.Config{Storage: config.Storage{LocalPreviewDir: localDir}},
		cat: cat,
	}
	deleted := map[string]struct{}{}
	stats, err := app.cleanupContentDuplicateVideos(ctx, localDir, videos, deleted)
	if err != nil {
		t.Fatalf("cleanup content duplicates: %v", err)
	}
	if stats.Candidates != 3 {
		t.Fatalf("candidates = %d, want 3 (short video must be excluded)", stats.Candidates)
	}
	if stats.Groups != 1 || stats.Deleted != 1 {
		t.Fatalf("stats = %+v, want 1 group / 1 deleted", stats)
	}
	if _, ok := deleted["video-compressed"]; !ok {
		t.Fatalf("compressed duplicate not deleted, deleted=%v", deleted)
	}

	if _, err := cat.GetVideo(ctx, "video-compressed"); err != sql.ErrNoRows {
		t.Fatalf("compressed video lookup = %v, want sql.ErrNoRows", err)
	}
	for _, id := range []string{"video-original", "video-other", "video-short"} {
		if _, err := cat.GetVideo(ctx, id); err != nil {
			t.Fatalf("%s should survive: %v", id, err)
		}
	}
	deletedItems, _, err := cat.ListDeletedVideos(ctx, catalog.ListParams{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list deleted videos: %v", err)
	}
	if len(deletedItems) != 1 ||
		deletedItems[0].ID != "video-compressed" ||
		deletedItems[0].Reason != catalog.DeletedVideoReasonDuplicate ||
		deletedItems[0].CanonicalVideoID != "video-original" {
		t.Fatalf("tombstone = %#v", deletedItems)
	}
	if _, err := os.Stat(filepath.Join(localDir, "video-compressed.mp4")); !os.IsNotExist(err) {
		t.Fatalf("compressed teaser still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(localDir, "video-original.mp4")); err != nil {
		t.Fatalf("original teaser missing: %v", err)
	}
}

// TestCleanupContentDuplicateVideosNearMissQueuesReview 构造相似度落在疑似区
// （0.80~0.92）的一对：不自动删除，但要写入复核队列。
func TestCleanupContentDuplicateVideosNearMissQueuesReview(t *testing.T) {
	ctx := context.Background()
	localDir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	base := syntheticFrames(77)
	// ±73 均匀噪声 → 单帧 SSIM ≈ 0.86，落在疑似区。
	noisy := make([][]byte, len(base))
	for k, f := range base {
		frame := make([]byte, len(f))
		rng := rand.New(rand.NewSource(int64(4000 + k)))
		for j := range f {
			v := int(f[j]) + rng.Intn(147) - 73
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			frame[j] = byte(v)
		}
		noisy[k] = frame
	}

	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	sigByPath := map[string]*mediasim.FrameSignature{}
	videos := make([]*catalog.Video, 0, 2)
	for i, seed := range []struct {
		id     string
		frames [][]byte
	}{
		{id: "gray-a", frames: base},
		{id: "gray-b", frames: noisy},
	} {
		teaser := filepath.Join(localDir, seed.id+".mp4")
		if err := os.WriteFile(teaser, []byte("teaser"), 0o644); err != nil {
			t.Fatalf("write teaser: %v", err)
		}
		sigByPath[teaser] = &mediasim.FrameSignature{Frames: seed.frames}
		v := &catalog.Video{
			ID: seed.id, DriveID: "115", FileID: "file-" + seed.id, Title: "标题 " + seed.id,
			Size: int64(1000 + i), DurationSeconds: 400, PreviewLocal: teaser, PreviewStatus: "ready",
			PublishedAt: now, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now,
		}
		if err := cat.UpsertVideo(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
		videos = append(videos, v)
	}

	restore := contentSignatureExtractor
	contentSignatureExtractor = func(ctx context.Context, ffmpegPath, teaserPath string) (*mediasim.FrameSignature, error) {
		return sigByPath[teaserPath], nil
	}
	t.Cleanup(func() { contentSignatureExtractor = restore })

	app := &App{
		cfg: &config.Config{Storage: config.Storage{LocalPreviewDir: localDir}},
		cat: cat,
	}
	deleted := map[string]struct{}{}
	stats, err := app.cleanupContentDuplicateVideos(ctx, localDir, videos, deleted)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.Deleted != 0 {
		t.Fatalf("stats = %+v, near-miss must not delete", stats)
	}
	if stats.NearMisses != 1 {
		t.Fatalf("stats = %+v, want 1 near miss", stats)
	}
	pairs, total, err := cat.ListDuplicateReviewPairs(ctx, catalog.DuplicateReviewStatusPending, 1, 10)
	if err != nil || total != 1 || len(pairs) != 1 {
		t.Fatalf("review queue total=%d err=%v, want 1", total, err)
	}
	if pairs[0].LeftVideoID != "gray-a" || pairs[0].RightVideoID != "gray-b" {
		t.Fatalf("queued pair = (%s, %s)", pairs[0].LeftVideoID, pairs[0].RightVideoID)
	}
	if pairs[0].MedianSSIM < 0.80 || pairs[0].MedianSSIM >= 0.92 {
		t.Fatalf("queued median = %f, expected in near-miss band", pairs[0].MedianSSIM)
	}
}

func TestCleanupContentDuplicateVideosNearMissDoesNotDelete(t *testing.T) {
	ctx := context.Background()
	localDir := t.TempDir()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	videos := make([]*catalog.Video, 0, 2)
	teasers := make([]string, 0, 2)
	for i, id := range []string{"near-a", "near-b"} {
		teaser := filepath.Join(localDir, id+".mp4")
		if err := os.WriteFile(teaser, []byte("teaser"), 0o644); err != nil {
			t.Fatalf("write teaser: %v", err)
		}
		teasers = append(teasers, teaser)
		v := &catalog.Video{
			ID:              id,
			DriveID:         "115",
			FileID:          "file-" + id,
			Title:           "标题 " + id,
			Size:            int64(1000 + i),
			DurationSeconds: 300,
			PreviewLocal:    teaser,
			PreviewStatus:   "ready",
			PublishedAt:     now,
			CreatedAt:       now.Add(time.Duration(i) * time.Second),
			UpdatedAt:       now,
		}
		if err := cat.UpsertVideo(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		videos = append(videos, v)
	}

	// 构造两份有效但不相似的签名：比较结果落在疑似区之下由随机帧保证,
	// 这里直接分别注入不同 seed。
	sigByPath := map[string]*mediasim.FrameSignature{
		teasers[0]: {Frames: syntheticFrames(11)},
		teasers[1]: {Frames: syntheticFrames(22)},
	}
	restore := contentSignatureExtractor
	contentSignatureExtractor = func(ctx context.Context, ffmpegPath, teaserPath string) (*mediasim.FrameSignature, error) {
		return sigByPath[teaserPath], nil
	}
	t.Cleanup(func() { contentSignatureExtractor = restore })

	app := &App{
		cfg: &config.Config{Storage: config.Storage{LocalPreviewDir: localDir}},
		cat: cat,
	}
	deleted := map[string]struct{}{}
	stats, err := app.cleanupContentDuplicateVideos(ctx, localDir, videos, deleted)
	if err != nil {
		t.Fatalf("cleanup content duplicates: %v", err)
	}
	if stats.Deleted != 0 || stats.Groups != 0 {
		t.Fatalf("stats = %+v, want no deletions", stats)
	}
	for _, id := range []string{"near-a", "near-b"} {
		if _, err := cat.GetVideo(ctx, id); err != nil {
			t.Fatalf("%s should survive: %v", id, err)
		}
	}
}
