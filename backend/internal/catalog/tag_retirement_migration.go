package catalog

import (
	"context"
	"log"
)

// removeAutomaticTaggingArtifacts removes the retired "create new labels from
// content" model. It preserves builtin/user tag definitions plus crawler-owned
// tags, and leaves engine assignments that point at preserved tags for the
// subsequent existing-tag retag pass to refresh.
func (c *Catalog) removeAutomaticTaggingArtifacts(ctx context.Context) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	generatedTagFilter := `
SELECT t.id
  FROM tags t
 WHERE lower(trim(COALESCE(t.source, ''))) = 'generated'
   AND lower(trim(COALESCE(t.origin, ''))) != 'crawler'
   AND lower(trim(COALESCE(t.origin, ''))) != '` + avSeriesOrigin + `'
   AND NOT EXISTS (
     SELECT 1
       FROM video_tags vt_crawler
      WHERE vt_crawler.tag_id = t.id
        AND lower(trim(COALESCE(vt_crawler.source, ''))) = 'crawler'
   )`

	affectedRows, err := tx.QueryContext(ctx, `
SELECT DISTINCT vt.video_id
  FROM video_tags vt
  LEFT JOIN tags t ON t.id = vt.tag_id
 WHERE lower(trim(COALESCE(vt.source, ''))) IN ('series', 'propagated')
    OR vt.tag_id IN (`+generatedTagFilter+`)`)
	if err != nil {
		return err
	}
	var videoIDs []string
	for affectedRows.Next() {
		var videoID string
		if err := affectedRows.Scan(&videoID); err != nil {
			affectedRows.Close()
			return err
		}
		videoIDs = append(videoIDs, videoID)
	}
	if err := affectedRows.Err(); err != nil {
		affectedRows.Close()
		return err
	}
	if err := affectedRows.Close(); err != nil {
		return err
	}

	removedAssignments := int64(0)
	res, err := tx.ExecContext(ctx, `
DELETE FROM video_tags
 WHERE lower(trim(COALESCE(source, ''))) IN ('series', 'propagated')`)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil {
		removedAssignments += n
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM video_tags WHERE tag_id IN (`+generatedTagFilter+`)`)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil {
		removedAssignments += n
	}

	res, err = tx.ExecContext(ctx, `DELETE FROM tags WHERE id IN (`+generatedTagFilter+`)`)
	if err != nil {
		return err
	}
	removedTags, _ := res.RowsAffected()

	staleRows, err := tx.QueryContext(ctx, `
SELECT id
  FROM videos
 WHERE COALESCE(tags_manual, 0) = 0
   AND COALESCE(tags, '') NOT IN ('', '[]', 'null')
   AND NOT EXISTS (
     SELECT 1
       FROM video_tags vt
      WHERE vt.video_id = videos.id
   )`)
	if err != nil {
		return err
	}
	for staleRows.Next() {
		var videoID string
		if err := staleRows.Scan(&videoID); err != nil {
			staleRows.Close()
			return err
		}
		videoIDs = append(videoIDs, videoID)
	}
	if err := staleRows.Err(); err != nil {
		staleRows.Close()
		return err
	}
	if err := staleRows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE videos
   SET tags = '[]'
 WHERE COALESCE(tags_manual, 0) = 0
   AND COALESCE(tags, '') NOT IN ('', '[]', 'null')
   AND NOT EXISTS (
     SELECT 1
       FROM video_tags vt
      WHERE vt.video_id = videos.id
   )`); err != nil {
		return err
	}

	for _, videoID := range uniqueStrings(videoIDs) {
		manual := hasManualTagsTx(ctx, tx, videoID)
		if err := syncVideoTagsJSONTx(ctx, tx, videoID, manual); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM settings WHERE key IN (
  'tags.auto_generate_enabled', 'tags.retag.v2_done', 'tags.maintenance.last_run_ms'
)`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if removedAssignments > 0 || removedTags > 0 {
		log.Printf("[catalog] removed retired automatic tagging artifacts: assignments=%d tags=%d", removedAssignments, removedTags)
		if removedTags > 0 {
			if err := c.bumpTagRulesVersion(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
