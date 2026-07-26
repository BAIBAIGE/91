package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 内容级查重疑似区（near-miss）的复核队列。夜间维护把 0.80~0.92 区间的
// 视频对写进来，管理后台并排展示后人工裁决：合并（保留一方删另一方）或忽略。

const (
	DuplicateReviewStatusPending   = "pending"
	DuplicateReviewStatusMerged    = "merged"
	DuplicateReviewStatusDismissed = "dismissed"
)

// DuplicateReviewPair 是一对待复核的疑似重复视频。Left/Right 是当前
// catalog 里的视频快照，仅在列表查询时填充。
type DuplicateReviewPair struct {
	ID           int64     `json:"id"`
	LeftVideoID  string    `json:"leftVideoId"`
	RightVideoID string    `json:"rightVideoId"`
	MedianSSIM   float64   `json:"medianSsim"`
	MinSSIM      float64   `json:"minSsim"`
	Comparisons  int       `json:"comparisons"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Left         *Video    `json:"left,omitempty"`
	Right        *Video    `json:"right,omitempty"`
}

func normalizeDuplicateReviewPairKey(a, b string) (string, string, error) {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" || a == b {
		return "", "", fmt.Errorf("invalid duplicate review pair (%q, %q)", a, b)
	}
	if b < a {
		a, b = b, a
	}
	return a, b, nil
}

// UpsertDuplicateReviewPair 写入或刷新一对疑似重复。已被人工裁决过
// （merged / dismissed）的对保持原状，不会被夜间维护重新置回待复核。
func (c *Catalog) UpsertDuplicateReviewPair(ctx context.Context, leftID, rightID string, medianSSIM, minSSIM float64, comparisons int) error {
	left, right, err := normalizeDuplicateReviewPairKey(leftID, rightID)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO duplicate_review_pairs (left_video_id, right_video_id, median_ssim, min_ssim, comparisons, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)
		 ON CONFLICT(left_video_id, right_video_id) DO UPDATE SET
		   median_ssim = excluded.median_ssim,
		   min_ssim    = excluded.min_ssim,
		   comparisons = excluded.comparisons,
		   updated_at  = excluded.updated_at
		 WHERE duplicate_review_pairs.status = 'pending'`,
		left, right, medianSSIM, minSSIM, comparisons, now, now)
	return err
}

// ListDuplicateReviewPairs 按状态分页列出复核对；pending 只返回双方视频都
// 还在库里的对（一方已被删除的对没有复核意义，由 Prune 清走）。
func (c *Catalog) ListDuplicateReviewPairs(ctx context.Context, status string, page, pageSize int) ([]*DuplicateReviewPair, int, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = DuplicateReviewStatusPending
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	where := `WHERE p.status = ?`
	if status == DuplicateReviewStatusPending {
		where += ` AND EXISTS (SELECT 1 FROM videos lv WHERE lv.id = p.left_video_id)
		           AND EXISTS (SELECT 1 FROM videos rv WHERE rv.id = p.right_video_id)`
	}
	var total int
	if err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM duplicate_review_pairs p `+where, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT p.id, p.left_video_id, p.right_video_id, p.median_ssim, p.min_ssim, p.comparisons, p.status, p.created_at, p.updated_at
		   FROM duplicate_review_pairs p `+where+`
		  ORDER BY p.median_ssim DESC, p.id ASC
		  LIMIT ? OFFSET ?`,
		status, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*DuplicateReviewPair
	for rows.Next() {
		p := &DuplicateReviewPair{}
		var createdAt, updatedAt int64
		if err := rows.Scan(&p.ID, &p.LeftVideoID, &p.RightVideoID, &p.MedianSSIM, &p.MinSSIM, &p.Comparisons, &p.Status, &createdAt, &updatedAt); err != nil {
			return nil, 0, err
		}
		p.CreatedAt = time.UnixMilli(createdAt)
		p.UpdatedAt = time.UnixMilli(updatedAt)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for _, p := range out {
		if v, err := c.GetVideo(ctx, p.LeftVideoID); err == nil {
			p.Left = v
		}
		if v, err := c.GetVideo(ctx, p.RightVideoID); err == nil {
			p.Right = v
		}
	}
	return out, total, nil
}

// GetDuplicateReviewPair 取单个复核对（不带视频快照）。
func (c *Catalog) GetDuplicateReviewPair(ctx context.Context, id int64) (*DuplicateReviewPair, error) {
	p := &DuplicateReviewPair{}
	var createdAt, updatedAt int64
	err := c.db.QueryRowContext(ctx,
		`SELECT id, left_video_id, right_video_id, median_ssim, min_ssim, comparisons, status, created_at, updated_at
		   FROM duplicate_review_pairs WHERE id = ?`, id).
		Scan(&p.ID, &p.LeftVideoID, &p.RightVideoID, &p.MedianSSIM, &p.MinSSIM, &p.Comparisons, &p.Status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	p.CreatedAt = time.UnixMilli(createdAt)
	p.UpdatedAt = time.UnixMilli(updatedAt)
	return p, nil
}

// ResolveDuplicateReviewPair 把 pending 的复核对标记为 merged / dismissed。
// 返回 sql.ErrNoRows 表示对不存在或已被裁决。
func (c *Catalog) ResolveDuplicateReviewPair(ctx context.Context, id int64, status string) error {
	if status != DuplicateReviewStatusMerged && status != DuplicateReviewStatusDismissed {
		return fmt.Errorf("invalid duplicate review status %q", status)
	}
	res, err := c.db.ExecContext(ctx,
		`UPDATE duplicate_review_pairs SET status = ?, updated_at = ? WHERE id = ? AND status = 'pending'`,
		status, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// PruneDuplicateReviewPairs 清掉一方视频已不在库里的 pending 对
// （例如被后续维护删除或人工删除），返回清理数量。
func (c *Catalog) PruneDuplicateReviewPairs(ctx context.Context) (int, error) {
	res, err := c.db.ExecContext(ctx,
		`DELETE FROM duplicate_review_pairs
		  WHERE status = 'pending'
		    AND (NOT EXISTS (SELECT 1 FROM videos lv WHERE lv.id = left_video_id)
		      OR NOT EXISTS (SELECT 1 FROM videos rv WHERE rv.id = right_video_id))`)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}

// CountPendingDuplicateReviewPairs 返回待复核数量（双方视频都在库里的）。
func (c *Catalog) CountPendingDuplicateReviewPairs(ctx context.Context) (int, error) {
	var total int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM duplicate_review_pairs p
		  WHERE p.status = 'pending'
		    AND EXISTS (SELECT 1 FROM videos lv WHERE lv.id = p.left_video_id)
		    AND EXISTS (SELECT 1 FROM videos rv WHERE rv.id = p.right_video_id)`).Scan(&total)
	return total, err
}
