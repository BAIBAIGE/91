package catalog

import (
	"context"
	"time"
)

type TelegramImportStats struct {
	Active         int64
	TodayCompleted int64
	TotalCompleted int64
}

// CountTelegramImports counts tasks across all senders and bot identities.
// Task rows survive history compaction, and repeated forwards sharing a task
// count only once. The caller supplies the timezone used for calendar days.
func (c *Catalog) CountTelegramImports(ctx context.Context, now time.Time) (TelegramImportStats, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, 1)
	var stats TelegramImportStats
	err := c.db.QueryRowContext(ctx, `
SELECT COUNT(CASE WHEN state IN ('queued','downloading','validating','saving') THEN 1 END),
       COUNT(CASE WHEN state='completed' AND finished_at>=? AND finished_at<? THEN 1 END),
       COUNT(CASE WHEN state='completed' THEN 1 END)
FROM remote_upload_jobs WHERE source_kind='telegram'`, start.UnixMilli(), end.UnixMilli()).Scan(
		&stats.Active, &stats.TodayCompleted, &stats.TotalCompleted,
	)
	return stats, err
}
