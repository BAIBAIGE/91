package catalog

import "context"

const TelegramLocalDriveID = "telegram-local"

// TelegramLocalFile is private storage metadata. FileID is an opaque basename;
// Root is captured at acquisition so later configuration edits cannot redirect
// existing videos. This table is never included in portable backups.
type TelegramLocalFile struct {
	FileID string
	Root   string
	JobID  string
}

func (c *Catalog) TelegramLocalFile(ctx context.Context, fileID string) (TelegramLocalFile, error) {
	var f TelegramLocalFile
	err := c.db.QueryRowContext(ctx, `SELECT file_id,root,job_id FROM telegram_local_files WHERE file_id=?`, fileID).Scan(&f.FileID, &f.Root, &f.JobID)
	return f, err
}

// ReserveTelegramLocalFile records the destination before moving the bytes.
// A restart or a changed bot configuration reuses the original destination.
func (c *Catalog) ReserveTelegramLocalFile(ctx context.Context, f TelegramLocalFile) (TelegramLocalFile, error) {
	_, err := c.db.ExecContext(ctx, `INSERT INTO telegram_local_files(file_id,root,job_id) VALUES(?,?,?) ON CONFLICT(file_id) DO NOTHING`, f.FileID, f.Root, f.JobID)
	if err != nil {
		return TelegramLocalFile{}, err
	}
	return c.TelegramLocalFile(ctx, f.FileID)
}

func (c *Catalog) DeleteTelegramLocalFile(ctx context.Context, fileID string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM telegram_local_files WHERE file_id=?`, fileID)
	return err
}

func (c *Catalog) AbandonedTelegramLocalFiles(ctx context.Context) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT f.file_id FROM telegram_local_files f
 LEFT JOIN remote_upload_jobs j ON j.id=f.job_id
 WHERE (j.id IS NULL OR j.state IN ('failed','canceled'))
 AND NOT EXISTS (SELECT 1 FROM videos v WHERE v.drive_id='telegram-local' AND v.file_id=f.file_id)
 AND NOT EXISTS (SELECT 1 FROM deleted_videos v WHERE v.drive_id='telegram-local' AND v.file_id=f.file_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (c *Catalog) TelegramLocalStorageSize(ctx context.Context) (int, int64, error) {
	var count int
	var size int64
	err := c.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(size_bytes),0) FROM videos WHERE drive_id='telegram-local'`).Scan(&count, &size)
	return count, size, err
}
