package catalog

import (
	"database/sql"
	"errors"
	"testing"
)

func TestMigrationRemovesRetiredTagSettingsAndAssignments(t *testing.T) {
	cat, ctx := openTagMaintenanceTestCatalog(t)
	seedTagMaintenanceVideo(t, cat, "old-tags", "ordinary clip", "clip.mp4")
	userTag, err := cat.EnsureTag(ctx, "user-label", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.db.ExecContext(ctx, `
INSERT INTO tags (label, source, created_at, updated_at) VALUES ('old-generated', 'generated', 1, 1);
INSERT INTO video_tags (video_id, tag_id, source, created_at)
SELECT 'old-tags', id, 'auto', 1 FROM tags WHERE label = 'old-generated';`); err != nil {
		t.Fatal(err)
	}
	if err := cat.insertVideoTag(ctx, "old-tags", userTag.ID, "propagated", "标题聚类"); err != nil {
		t.Fatal(err)
	}
	keys := []string{"tags.auto_generate_enabled", "tags.retag.v2_done", "tags.maintenance.last_run_ms"}
	for _, key := range keys {
		if err := cat.SetSetting(ctx, key, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := cat.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cat.ReconcileVideoTags(ctx); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if value, err := cat.GetSetting(ctx, key, "missing"); err != nil || value != "missing" {
			t.Fatalf("retired setting %q = %q, %v", key, value, err)
		}
	}
	if _, err := cat.getTagByLabel(ctx, "old-generated"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retired generated tag lookup = %v", err)
	}
	if _, err := cat.getTagByID(ctx, userTag.ID); err != nil {
		t.Fatalf("user tag was removed: %v", err)
	}
	video, err := cat.GetVideo(ctx, "old-tags")
	if err != nil || len(video.Tags) != 0 {
		t.Fatalf("retired assignments survived migration: video=%#v, err=%v", video, err)
	}
}
