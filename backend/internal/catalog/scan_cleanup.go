package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DriveSkipCleanupState is internal progress for the skip-directory policy
// cleanup. Initialized distinguishes a recorded empty list from an upgraded
// database that has never run the cleanup.
type DriveSkipCleanupState struct {
	DirIDs           []string
	Initialized      bool
	LegacyDoneDirIDs []string
}

// effectiveAncestorDirIDs applies the no-backfill compatibility rule shared by
// presence and policy cleanup: a stored chain wins, otherwise use parent_id.
func effectiveAncestorDirIDs(stored []string, parentID string) []string {
	if stored != nil {
		return stored
	}
	if parentID == "" {
		return nil
	}
	return []string{parentID}
}

// ListVideosInAncestorDirs returns rows whose effective ancestor chain
// intersects dirIDs. json_each keeps exact matching correct for provider IDs
// that themselves contain slashes or other separator characters.
func (c *Catalog) ListVideosInAncestorDirs(ctx context.Context, driveID string, dirIDs []string) ([]*Video, error) {
	dirIDs = uniqueDirectoryIDs(dirIDs)
	if c == nil || c.db == nil {
		return nil, errors.New("catalog: database is not open")
	}
	if strings.TrimSpace(driveID) == "" || len(dirIDs) == 0 {
		return []*Video{}, nil
	}

	dirIDsJSON, _ := json.Marshal(dirIDs)
	query := `SELECT ` + allVideoCols + ` FROM videos
WHERE drive_id = ?
  AND EXISTS (
    SELECT 1
      FROM json_each(
        CASE
          WHEN COALESCE(ancestor_dir_ids, '') != ''
           AND json_type(
                 CASE WHEN json_valid(ancestor_dir_ids) THEN ancestor_dir_ids ELSE '[]' END
               ) = 'array'
            THEN ancestor_dir_ids
          WHEN COALESCE(parent_id, '') != '' THEN json_array(parent_id)
          ELSE '[]'
        END
      ) AS ancestor
     WHERE CAST(ancestor.value AS TEXT) IN (
       SELECT CAST(value AS TEXT) FROM json_each(?)
     )
  )
ORDER BY created_at ASC, id ASC`
	return c.listScanCleanupVideos(ctx, query, driveID, string(dirIDsJSON))
}

// ListVideosByParentDirIDs supports the one-time legacy backfill after the
// scanner has enumerated the subtree rooted at a configured skip directory.
func (c *Catalog) ListVideosByParentDirIDs(ctx context.Context, driveID string, dirIDs []string) ([]*Video, error) {
	dirIDs = uniqueDirectoryIDs(dirIDs)
	if c == nil || c.db == nil {
		return nil, errors.New("catalog: database is not open")
	}
	if strings.TrimSpace(driveID) == "" || len(dirIDs) == 0 {
		return []*Video{}, nil
	}

	dirIDsJSON, _ := json.Marshal(dirIDs)
	query := `SELECT ` + allVideoCols + ` FROM videos
WHERE drive_id = ?
  AND COALESCE(parent_id, '') IN (
    SELECT CAST(value AS TEXT) FROM json_each(?)
  )
ORDER BY created_at ASC, id ASC`
	return c.listScanCleanupVideos(ctx, query, driveID, string(dirIDsJSON))
}

func (c *Catalog) listScanCleanupVideos(ctx context.Context, query string, args ...any) ([]*Video, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*Video, 0)
	for rows.Next() {
		video, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, video)
	}
	return items, rows.Err()
}

func (c *Catalog) GetDriveSkipCleanupState(ctx context.Context, driveID string) (DriveSkipCleanupState, error) {
	if c == nil || c.db == nil {
		return DriveSkipCleanupState{}, errors.New("catalog: database is not open")
	}
	var payload sql.NullString
	err := c.db.QueryRowContext(ctx, `
SELECT skip_cleanup_dir_ids
  FROM drives
 WHERE id = ?`, driveID).Scan(&payload)
	if err != nil {
		return DriveSkipCleanupState{}, err
	}
	state := DriveSkipCleanupState{
		Initialized: payload.Valid,
	}
	if payload.Valid {
		if err := json.Unmarshal([]byte(payload.String), &state.DirIDs); err != nil {
			return DriveSkipCleanupState{}, fmt.Errorf("catalog: decode skip cleanup directory IDs: %w", err)
		}
		if state.DirIDs == nil {
			state.DirIDs = []string{}
		}
	}
	rows, err := c.db.QueryContext(ctx, `
SELECT dir_id
  FROM drive_skip_cleanup_legacy_dirs
 WHERE drive_id = ?
 ORDER BY dir_id`, driveID)
	if err != nil {
		return DriveSkipCleanupState{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var dirID string
		if err := rows.Scan(&dirID); err != nil {
			return DriveSkipCleanupState{}, err
		}
		state.LegacyDoneDirIDs = append(state.LegacyDoneDirIDs, dirID)
	}
	if err := rows.Err(); err != nil {
		return DriveSkipCleanupState{}, err
	}
	return state, nil
}

func (c *Catalog) SetDriveSkipCleanupDirIDs(ctx context.Context, driveID string, dirIDs []string) error {
	if c == nil || c.db == nil {
		return errors.New("catalog: database is not open")
	}
	dirIDs = uniqueDirectoryIDs(dirIDs)
	if dirIDs == nil {
		dirIDs = []string{}
	}
	payload, err := json.Marshal(dirIDs)
	if err != nil {
		return fmt.Errorf("catalog: encode skip cleanup directory IDs: %w", err)
	}
	result, err := c.db.ExecContext(ctx,
		`UPDATE drives SET skip_cleanup_dir_ids = ? WHERE id = ?`, string(payload), driveID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (c *Catalog) MarkDriveSkipCleanupLegacyDirDone(ctx context.Context, driveID, dirID string) error {
	if c == nil || c.db == nil {
		return errors.New("catalog: database is not open")
	}
	driveID = strings.TrimSpace(driveID)
	dirID = strings.TrimSpace(dirID)
	if driveID == "" || dirID == "" {
		return errors.New("catalog: empty drive or directory id for legacy cleanup completion")
	}
	result, err := c.db.ExecContext(ctx, `
INSERT INTO drive_skip_cleanup_legacy_dirs (drive_id, dir_id, completed_at)
SELECT ?, ?, ?
 WHERE EXISTS (SELECT 1 FROM drives WHERE id = ?)
ON CONFLICT(drive_id, dir_id) DO UPDATE SET completed_at = excluded.completed_at`,
		driveID, dirID, time.Now().UnixMilli(), driveID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (c *Catalog) DriveHasVideosWithoutAncestorDirIDs(ctx context.Context, driveID string) (bool, error) {
	if c == nil || c.db == nil {
		return false, errors.New("catalog: database is not open")
	}
	var exists int
	err := c.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
    FROM videos
   WHERE drive_id = ?
     AND COALESCE(ancestor_dir_ids, '') = ''
)`, strings.TrimSpace(driveID)).Scan(&exists)
	return exists != 0, err
}

func uniqueDirectoryIDs(dirIDs []string) []string {
	if len(dirIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(dirIDs))
	out := make([]string, 0, len(dirIDs))
	for _, dirID := range dirIDs {
		dirID = strings.TrimSpace(dirID)
		if dirID == "" {
			continue
		}
		if _, exists := seen[dirID]; exists {
			continue
		}
		seen[dirID] = struct{}{}
		out = append(out, dirID)
	}
	return out
}
