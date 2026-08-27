package catalog

import (
	"context"
	"testing"
	"time"
)

func TestOpenDropsRetiredTranscodeColumnsWithoutLosingVideo(t *testing.T) {
	ctx := context.Background()
	dbPath := t.TempDir() + "/catalog.db"
	cat, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID:          "video-1",
		DriveID:     "drive-1",
		FileID:      "original.mp4",
		Title:       "original video",
		Size:        1234,
		Ext:         "mp4",
		PublishedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	for _, statement := range []string{
		`ALTER TABLE videos ADD COLUMN transcode_status TEXT DEFAULT ''`,
		`ALTER TABLE videos ADD COLUMN transcode_error TEXT DEFAULT ''`,
		`ALTER TABLE videos ADD COLUMN transcoded_file_id TEXT DEFAULT ''`,
		`ALTER TABLE videos ADD COLUMN transcoded_size INTEGER DEFAULT 0`,
	} {
		if _, err := cat.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("add retired column: %v", err)
		}
	}
	if _, err := cat.db.ExecContext(ctx, `
UPDATE videos
   SET transcode_status = 'ready',
       transcode_error = 'legacy error',
       transcoded_file_id = 'legacy-output.mp4',
       transcoded_size = 5678
 WHERE id = 'video-1'`); err != nil {
		t.Fatalf("seed retired values: %v", err)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open migrated catalog: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, column := range []string{
		"transcode_status",
		"transcode_error",
		"transcoded_file_id",
		"transcoded_size",
	} {
		if hasColumn(t, reopened, "videos", column) {
			t.Fatalf("retired column videos.%s still exists", column)
		}
	}
	video, err := reopened.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get migrated video: %v", err)
	}
	if video.Title != "original video" || video.FileID != "original.mp4" || video.Size != 1234 {
		t.Fatalf("migrated video lost source data: %#v", video)
	}
}
