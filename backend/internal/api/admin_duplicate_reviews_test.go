package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/video-site/backend/internal/catalog"
)

func seedDuplicateReviewFixture(t *testing.T) (*catalog.Catalog, int64) {
	t.Helper()
	ctx := context.Background()
	cat, err := catalog.Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	now := time.Now()
	for _, id := range []string{"dup-left", "dup-right"} {
		if err := cat.UpsertVideo(ctx, &catalog.Video{
			ID: id, DriveID: "drive", FileID: "file-" + id, FileName: id + ".mp4",
			Title: "标题 " + id, Size: 100, DurationSeconds: 300,
			PublishedAt: now, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := cat.UpsertDuplicateReviewPair(ctx, "dup-left", "dup-right", 0.86, 0.61, 12); err != nil {
		t.Fatalf("seed pair: %v", err)
	}
	pairs, _, err := cat.ListDuplicateReviewPairs(ctx, "", 1, 10)
	if err != nil || len(pairs) != 1 {
		t.Fatalf("list pairs: %v len=%d", err, len(pairs))
	}
	return cat, pairs[0].ID
}

func resolveReviewRequest(t *testing.T, srv *AdminServer, pairID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/admin/api/duplicate-reviews/%d/resolve", pairID), strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", pairID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()
	srv.handleResolveDuplicateReview(rr, req)
	return rr
}

func TestHandleListDuplicateReviews(t *testing.T) {
	cat, _ := seedDuplicateReviewFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/duplicate-reviews?status=pending&page=1", nil)
	rr := httptest.NewRecorder()
	(&AdminServer{Catalog: cat}).handleListDuplicateReviews(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Items []adminDuplicateReviewPair `json:"items"`
		Total int                        `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Total != 1 || len(payload.Items) != 1 {
		t.Fatalf("total=%d items=%d, want 1/1", payload.Total, len(payload.Items))
	}
	item := payload.Items[0]
	if item.Left == nil || item.Right == nil || item.Left.ID != "dup-left" || item.Right.ID != "dup-right" {
		t.Fatalf("video snapshots wrong: %+v", item)
	}
	if item.MedianSSIM != 0.86 {
		t.Fatalf("median = %f", item.MedianSSIM)
	}
}

func TestHandleResolveDuplicateReviewDismiss(t *testing.T) {
	cat, pairID := seedDuplicateReviewFixture(t)
	rr := resolveReviewRequest(t, &AdminServer{Catalog: cat}, pairID, `{"action":"dismiss"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	pair, err := cat.GetDuplicateReviewPair(context.Background(), pairID)
	if err != nil || pair.Status != catalog.DuplicateReviewStatusDismissed {
		t.Fatalf("status = %v err=%v, want dismissed", pair, err)
	}
	// 两个视频都还在。
	for _, id := range []string{"dup-left", "dup-right"} {
		if _, err := cat.GetVideo(context.Background(), id); err != nil {
			t.Fatalf("%s should survive: %v", id, err)
		}
	}
	// 已裁决的对再次裁决 → 409。
	rr = resolveReviewRequest(t, &AdminServer{Catalog: cat}, pairID, `{"action":"dismiss"}`)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second resolve status = %d, want 409", rr.Code)
	}
}

func TestHandleResolveDuplicateReviewMerge(t *testing.T) {
	cat, pairID := seedDuplicateReviewFixture(t)
	var gotKeep, gotRemove string
	srv := &AdminServer{
		Catalog: cat,
		OnMergeDuplicateVideo: func(ctx context.Context, keepID, removeID string) error {
			gotKeep, gotRemove = keepID, removeID
			// 模拟外层删除重复视频。
			return cat.DeleteVideoWithTombstone(ctx, removeID)
		},
	}
	rr := resolveReviewRequest(t, srv, pairID, `{"action":"keep-right"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if gotKeep != "dup-right" || gotRemove != "dup-left" {
		t.Fatalf("merge args keep=%q remove=%q", gotKeep, gotRemove)
	}
	pair, err := cat.GetDuplicateReviewPair(context.Background(), pairID)
	if err != nil || pair.Status != catalog.DuplicateReviewStatusMerged {
		t.Fatalf("status = %v err=%v, want merged", pair, err)
	}

	// 未接线 OnMergeDuplicateVideo 时返回 501 而不是误标状态。
	cat2, pairID2 := seedDuplicateReviewFixture(t)
	rr = resolveReviewRequest(t, &AdminServer{Catalog: cat2}, pairID2, `{"action":"keep-left"}`)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("unwired merge status = %d, want 501", rr.Code)
	}
	if pair, err := cat2.GetDuplicateReviewPair(context.Background(), pairID2); err != nil || pair.Status != catalog.DuplicateReviewStatusPending {
		t.Fatalf("pair status = %v err=%v, want still pending", pair, err)
	}
}
