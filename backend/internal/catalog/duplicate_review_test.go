package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func seedReviewVideo(t *testing.T, cat *Catalog, id string) {
	t.Helper()
	now := time.Now()
	if err := cat.UpsertVideo(context.Background(), &Video{
		ID: id, DriveID: "drive", FileID: "file-" + id, FileName: id + ".mp4",
		Title: "标题 " + id, PublishedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestDuplicateReviewPairLifecycle(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cat.Close()
	seedReviewVideo(t, cat, "video-b")
	seedReviewVideo(t, cat, "video-a")

	// 入队；键按字典序归一化，两种参数顺序落到同一行。
	if err := cat.UpsertDuplicateReviewPair(ctx, "video-b", "video-a", 0.85, 0.60, 12); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := cat.UpsertDuplicateReviewPair(ctx, "video-a", "video-b", 0.88, 0.65, 12); err != nil {
		t.Fatalf("upsert refresh: %v", err)
	}
	pairs, total, err := cat.ListDuplicateReviewPairs(ctx, "", 1, 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(pairs) != 1 {
		t.Fatalf("total=%d len=%d, want 1/1", total, len(pairs))
	}
	pair := pairs[0]
	if pair.LeftVideoID != "video-a" || pair.RightVideoID != "video-b" {
		t.Fatalf("pair key = (%s, %s), want normalized order", pair.LeftVideoID, pair.RightVideoID)
	}
	if pair.MedianSSIM != 0.88 {
		t.Fatalf("median = %f, want refreshed 0.88", pair.MedianSSIM)
	}
	if pair.Left == nil || pair.Right == nil {
		t.Fatalf("video snapshots missing")
	}
	if n, err := cat.CountPendingDuplicateReviewPairs(ctx); err != nil || n != 1 {
		t.Fatalf("pending count = %d err=%v, want 1", n, err)
	}

	// 忽略后不再出现在 pending，且夜间重新 upsert 不会复活。
	if err := cat.ResolveDuplicateReviewPair(ctx, pair.ID, DuplicateReviewStatusDismissed); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if err := cat.ResolveDuplicateReviewPair(ctx, pair.ID, DuplicateReviewStatusDismissed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second resolve err = %v, want sql.ErrNoRows", err)
	}
	if err := cat.UpsertDuplicateReviewPair(ctx, "video-a", "video-b", 0.90, 0.70, 12); err != nil {
		t.Fatalf("upsert after dismiss: %v", err)
	}
	if _, total, _ := cat.ListDuplicateReviewPairs(ctx, DuplicateReviewStatusPending, 1, 20); total != 0 {
		t.Fatalf("dismissed pair resurrected, pending total=%d", total)
	}
	if got, err := cat.GetDuplicateReviewPair(ctx, pair.ID); err != nil || got.Status != DuplicateReviewStatusDismissed {
		t.Fatalf("status = %v err=%v, want dismissed", got, err)
	}

	// 非法状态被拒绝。
	if err := cat.ResolveDuplicateReviewPair(ctx, pair.ID, "pending"); err == nil {
		t.Fatalf("invalid status accepted")
	}
}

func TestDuplicateReviewPairPrune(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cat.Close()
	seedReviewVideo(t, cat, "keep-1")
	seedReviewVideo(t, cat, "keep-2")
	seedReviewVideo(t, cat, "gone-1")
	if err := cat.UpsertDuplicateReviewPair(ctx, "keep-1", "keep-2", 0.85, 0.6, 12); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := cat.UpsertDuplicateReviewPair(ctx, "keep-1", "gone-1", 0.84, 0.6, 12); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := cat.DeleteVideoWithTombstone(ctx, "gone-1"); err != nil {
		t.Fatalf("delete video: %v", err)
	}
	// 一方已删的 pending 对不出现在列表里，Prune 把它清掉。
	if _, total, _ := cat.ListDuplicateReviewPairs(ctx, DuplicateReviewStatusPending, 1, 20); total != 1 {
		t.Fatalf("pending total = %d, want 1", total)
	}
	pruned, err := cat.PruneDuplicateReviewPairs(ctx)
	if err != nil || pruned != 1 {
		t.Fatalf("pruned = %d err=%v, want 1", pruned, err)
	}
}
