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

// ScanPresenceScope is the discovery proof used to evaluate missing catalog
// rows. The E/F/X maps are mutually exclusive directory classifications.
type ScanPresenceScope struct {
	EnumeratedDirIDs map[string]struct{}
	FailedDirIDs     map[string]struct{}
	ExcludedDirIDs   map[string]struct{}
	FullDriveScan    bool
}

type scanPresenceVideo struct {
	fileID         string
	parentID       string
	ancestorDirIDs []string
}

// ConfirmMissingDriveFiles advances the durable missing counter only when the
// snapshot proves that a file or one of its ancestor directories disappeared.
// Failed and policy-excluded subtrees remain protected. A live file clears its
// counter immediately, and destructive cleanup still requires threshold
// consecutive eligible snapshots.
func (c *Catalog) ConfirmMissingDriveFiles(
	ctx context.Context,
	driveID string,
	liveFileIDs map[string]struct{},
	scope ScanPresenceScope,
	threshold int,
) (map[string]struct{}, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog: database is not open")
	}
	driveID = strings.TrimSpace(driveID)
	if driveID == "" {
		return nil, errors.New("catalog: empty drive id")
	}
	if threshold < 2 {
		return nil, fmt.Errorf("catalog: unsafe missing-file confirmation threshold %d", threshold)
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT file_id, COALESCE(parent_id, ''), COALESCE(ancestor_dir_ids, '')
  FROM videos
 WHERE drive_id = ?`, driveID)
	if err != nil {
		return nil, err
	}
	var videos []scanPresenceVideo
	for rows.Next() {
		var video scanPresenceVideo
		var ancestorDirIDsJSON string
		if err := rows.Scan(&video.fileID, &video.parentID, &ancestorDirIDsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		if ancestorDirIDsJSON != "" {
			if err := json.Unmarshal([]byte(ancestorDirIDsJSON), &video.ancestorDirIDs); err != nil {
				rows.Close()
				return nil, fmt.Errorf("catalog: decode video %s ancestor directory IDs: %w", video.fileID, err)
			}
			if video.ancestorDirIDs == nil {
				video.ancestorDirIDs = []string{}
			}
		}
		videos = append(videos, video)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	confirmed := make(map[string]struct{})
	now := time.Now().UnixMilli()
	for _, video := range videos {
		fileID := strings.TrimSpace(video.fileID)
		if fileID == "" {
			continue
		}
		if _, live := liveFileIDs[fileID]; live {
			if _, err := tx.ExecContext(ctx, `DELETE FROM drive_scan_misses WHERE drive_id = ? AND file_id = ?`, driveID, fileID); err != nil {
				return nil, err
			}
			continue
		}
		if !missingFileEligible(video, scope) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO drive_scan_misses (drive_id, file_id, consecutive_misses, last_missing_at)
VALUES (?, ?, 1, ?)
ON CONFLICT(drive_id, file_id) DO UPDATE SET
  consecutive_misses = drive_scan_misses.consecutive_misses + 1,
  last_missing_at = excluded.last_missing_at`, driveID, fileID, now); err != nil {
			return nil, err
		}
		var misses int
		if err := tx.QueryRowContext(ctx,
			`SELECT consecutive_misses FROM drive_scan_misses WHERE drive_id = ? AND file_id = ?`,
			driveID, fileID).Scan(&misses); err != nil {
			return nil, err
		}
		if misses >= threshold {
			confirmed[fileID] = struct{}{}
		}
	}

	// DeleteVideo removes the matching counter transactionally. Keep this repair
	// path for orphan rows left by versions that predate that behavior.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM drive_scan_misses
WHERE drive_id = ?
  AND NOT EXISTS (
    SELECT 1 FROM videos
    WHERE videos.drive_id = drive_scan_misses.drive_id
      AND videos.file_id = drive_scan_misses.file_id
  )`, driveID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return confirmed, nil
}

func missingFileEligible(video scanPresenceVideo, scope ScanPresenceScope) bool {
	ancestorDirIDs := effectiveAncestorDirIDs(video.ancestorDirIDs, video.parentID)
	if len(ancestorDirIDs) == 0 {
		return scope.FullDriveScan && len(scope.FailedDirIDs) == 0
	}

	for index, dirID := range ancestorDirIDs {
		if _, enumerated := scope.EnumeratedDirIDs[dirID]; enumerated {
			continue
		}
		if _, failed := scope.FailedDirIDs[dirID]; failed {
			return false
		}
		if _, excluded := scope.ExcludedDirIDs[dirID]; excluded {
			return false
		}
		if index == 0 {
			return scope.FullDriveScan && len(scope.FailedDirIDs) == 0
		}
		// The preceding ancestor was enumerated successfully but did not list
		// this directory, proving that the subtree no longer exists.
		return true
	}

	// Every ancestor was enumerated, so the direct parent exists and the file
	// itself is the first missing element.
	return true
}
