package catalog

import (
	"context"
)

// ListVideoDuplicateCandidates returns all matches so discovery can discard
// stale sources without overlooking a later, still-live duplicate.
func (c *Catalog) ListVideoDuplicateCandidates(ctx context.Context, hash, fileName string, size int64) ([]*Video, error) {
	hash = normalizeContentHash(hash)
	rows, err := c.db.QueryContext(ctx, `SELECT `+allVideoCols+` FROM videos
WHERE (? != '' AND content_hash = ?)
   OR (? != '' AND ? > 0 AND file_name = ? AND size_bytes = ?)
ORDER BY CASE WHEN ? != '' AND content_hash = ? THEN 0 ELSE 1 END, created_at ASC, id ASC`,
		hash, hash, fileName, size, fileName, size, hash, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var videos []*Video
	for rows.Next() {
		video, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		videos = append(videos, video)
	}
	return videos, rows.Err()
}
