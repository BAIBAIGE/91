package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/video-site/backend/internal/catalog"
)

// 疑似重复复核：夜间内容级查重把 0.80~0.92 的视频对写进 duplicate_review_pairs，
// 这里提供列表与裁决接口。合并 = 保留一方、按重复墓碑删除另一方（走外层注入的
// OnMergeDuplicateVideo，与夜间自动删除同一条路径）；忽略 = 标记 dismissed，
// 之后夜间维护不会再把这对写回来。

type adminDuplicateReviewVideo struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	DriveID         string `json:"driveId"`
	Size            int64  `json:"size"`
	DurationSeconds int    `json:"durationSeconds"`
	ThumbnailURL    string `json:"thumbnailUrl"`
	Views           int    `json:"views"`
	Likes           int    `json:"likes"`
	CreatedAt       int64  `json:"createdAt"`
}

type adminDuplicateReviewPair struct {
	ID          int64                      `json:"id"`
	MedianSSIM  float64                    `json:"medianSsim"`
	MinSSIM     float64                    `json:"minSsim"`
	Comparisons int                        `json:"comparisons"`
	Status      string                     `json:"status"`
	CreatedAt   int64                      `json:"createdAt"`
	UpdatedAt   int64                      `json:"updatedAt"`
	Left        *adminDuplicateReviewVideo `json:"left"`
	Right       *adminDuplicateReviewVideo `json:"right"`
}

func mapDuplicateReviewVideo(v *catalog.Video) *adminDuplicateReviewVideo {
	if v == nil {
		return nil
	}
	return &adminDuplicateReviewVideo{
		ID:              v.ID,
		Title:           v.Title,
		DriveID:         v.DriveID,
		Size:            v.Size,
		DurationSeconds: v.DurationSeconds,
		ThumbnailURL:    v.ThumbnailURL,
		Views:           v.Views,
		Likes:           v.Likes,
		CreatedAt:       v.CreatedAt.UnixMilli(),
	}
}

func (a *AdminServer) handleListDuplicateReviews(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	status := strings.TrimSpace(q.Get("status"))
	pairs, total, err := a.Catalog.ListDuplicateReviewPairs(r.Context(), status, page, 20)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]*adminDuplicateReviewPair, 0, len(pairs))
	for _, p := range pairs {
		items = append(items, &adminDuplicateReviewPair{
			ID:          p.ID,
			MedianSSIM:  p.MedianSSIM,
			MinSSIM:     p.MinSSIM,
			Comparisons: p.Comparisons,
			Status:      p.Status,
			CreatedAt:   p.CreatedAt.UnixMilli(),
			UpdatedAt:   p.UpdatedAt.UnixMilli(),
			Left:        mapDuplicateReviewVideo(p.Left),
			Right:       mapDuplicateReviewVideo(p.Right),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

type resolveDuplicateReviewReq struct {
	// Action：keep-left（保留左、删右）/ keep-right（保留右、删左）/ dismiss（忽略）
	Action string `json:"action"`
}

func (a *AdminServer) handleResolveDuplicateReview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "id")), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("invalid review pair id"))
		return
	}
	var body resolveDuplicateReviewReq
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	pair, err := a.Catalog.GetDuplicateReviewPair(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if pair.Status != catalog.DuplicateReviewStatusPending {
		writeErr(w, http.StatusConflict, errors.New("review pair already resolved"))
		return
	}

	switch strings.TrimSpace(body.Action) {
	case "dismiss":
		if err := a.Catalog.ResolveDuplicateReviewPair(r.Context(), id, catalog.DuplicateReviewStatusDismissed); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	case "keep-left", "keep-right":
		keepID, removeID := pair.LeftVideoID, pair.RightVideoID
		if body.Action == "keep-right" {
			keepID, removeID = pair.RightVideoID, pair.LeftVideoID
		}
		if a.OnMergeDuplicateVideo == nil {
			writeErr(w, http.StatusNotImplemented, errors.New("merge not supported"))
			return
		}
		if err := a.OnMergeDuplicateVideo(r.Context(), keepID, removeID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if err := a.Catalog.ResolveDuplicateReviewPair(r.Context(), id, catalog.DuplicateReviewStatusMerged); err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// 被删视频可能还挂在其他 pending 对里，顺手清掉。
		if _, err := a.Catalog.PruneDuplicateReviewPairs(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, errors.New("invalid action"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
