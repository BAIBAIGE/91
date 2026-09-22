package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestManagedPathMigrationConvertsMovedLegacyRecordsAtomically(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cat, err := Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	previewDir := filepath.Join(root, "new", "previews")
	if err := os.MkdirAll(filepath.Join(root, "new", "crawler-scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new", "crawler-scripts", "test.py"), []byte("imported script"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(root, "gone", "previews")
	now := time.Now().Truncate(time.Millisecond)
	video := &Video{ID: "video", DriveID: "cloud", FileID: "file", Title: "Kept", PreviewLocal: filepath.Join(oldDir, "video.mp4"), PreviewStatus: "ready", PreviewUpdatedAt: now, Views: 42, PublishedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := cat.UpsertVideo(ctx, video); err != nil {
		t.Fatal(err)
	}
	before, err := cat.GetVideo(ctx, video.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.db.Exec(`INSERT INTO duplicate_asset_cleanup_jobs(video_id, preview_local, created_at, updated_at) VALUES ('cleanup', ?, 1, 1)`, filepath.Join(oldDir, "cleanup.mp4")); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"version": 1, "video": map[string]any{"id": "deleted", "previewLocal": filepath.Join(oldDir, "deleted.mp4"), "description": filepath.Join(oldDir, "keep-this-text.mp4")}, "unknown": "preserved"}
	encoded, _ := json.Marshal(payload)
	if _, err := cat.db.Exec(`INSERT INTO deleted_videos(id, restore_payload, deleted_at) VALUES ('deleted', ?, 1)`, string(encoded)); err != nil {
		t.Fatal(err)
	}
	for _, drive := range []*Drive{
		{ID: "imported", Kind: "scriptcrawler", Name: "Imported", Credentials: map[string]string{"script_path": filepath.Join(root, "gone", "crawler-scripts", "test.py"), "proxy": "http://localhost:8888"}},
		{ID: "external", Kind: "scriptcrawler", Name: "External", Credentials: map[string]string{"script_path": filepath.Join(root, "external.py")}},
		{ID: "disk", Kind: "localstorage", Name: "Disk", Credentials: map[string]string{"path": filepath.Join(root, "external-disk")}},
	} {
		if err := cat.UpsertDrive(ctx, drive); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cat.MigrateManagedPaths(ctx, previewDir); err != nil {
		t.Fatal(err)
	}
	after, err := cat.GetVideo(ctx, video.ID)
	if err != nil {
		t.Fatal(err)
	}
	before.PreviewLocal = "video.mp4"
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("migration changed video metadata: got %+v, want %+v", after, before)
	}
	jobs, err := cat.ListDuplicateAssetCleanupJobs(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].PreviewLocal != "cleanup.mp4" {
		t.Fatalf("cleanup jobs = %+v, %v", jobs, err)
	}
	var restoredPayload string
	if err := cat.db.QueryRow(`SELECT restore_payload FROM deleted_videos WHERE id='deleted'`).Scan(&restoredPayload); err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal([]byte(restoredPayload), &restored); err != nil {
		t.Fatal(err)
	}
	payload["video"].(map[string]any)["previewLocal"] = "deleted.mp4"
	expected, _ := json.Marshal(payload)
	if restoredPayload != string(expected) {
		t.Fatalf("restore payload = %s, want %s", restoredPayload, expected)
	}
	imported, _ := cat.GetDrive(ctx, "imported")
	if imported.Credentials["script_file"] != "test.py" || imported.Credentials["script_path"] != "" || imported.Credentials["proxy"] != "http://localhost:8888" {
		t.Fatalf("imported crawler = %+v", imported)
	}
	external, _ := cat.GetDrive(ctx, "external")
	if external.Credentials["script_path"] != filepath.Join(root, "external.py") || external.Credentials["script_file"] != "" {
		t.Fatalf("external crawler = %+v", external)
	}
	disk, _ := cat.GetDrive(ctx, "disk")
	if disk.Credentials["path"] != filepath.Join(root, "external-disk") {
		t.Fatalf("external disk = %+v", disk)
	}
	if changed, err := cat.MigrateManagedPaths(ctx, filepath.Join(root, "another-move", "previews")); err != nil || changed != 0 {
		t.Fatalf("second move rewrote portable data: changed=%d, err=%v", changed, err)
	}
}

func TestManagedPathMigrationRollsBackOnMalformedPayload(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	oldPath := filepath.Join(t.TempDir(), "old", "video.mp4")
	if err := cat.UpsertVideo(ctx, &Video{ID: "video", DriveID: "cloud", FileID: "file", PreviewLocal: oldPath, PreviewStatus: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.db.Exec(`INSERT INTO deleted_videos(id, restore_payload, deleted_at) VALUES ('bad', '{', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.MigrateManagedPaths(ctx, filepath.Join(t.TempDir(), "previews")); err == nil {
		t.Fatal("malformed payload did not abort migration")
	}
	video, err := cat.GetVideo(ctx, "video")
	if err != nil || video.PreviewLocal != oldPath {
		t.Fatalf("partially committed migration: %+v, %v", video, err)
	}
}

func TestPortablePreviewReferencePreservesNestedPathsAndExternalBoundaries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "previews")
	for _, tc := range []struct{ value, want string }{
		{filepath.Join(root, "nested", "custom.mp4"), "nested/custom.mp4"},
		{"nested/custom.mp4", "nested/custom.mp4"},
		{"nested/video.mp4", "nested/video.mp4"},
		{"./data/previews/video.mp4", "video.mp4"},
		{"data/previews/video.mp4", "video.mp4"},
		{filepath.Join(root+"-private", "secret.mp4"), filepath.Join(root+"-private", "secret.mp4")},
	} {
		if got := portablePreviewReference(root, "video", tc.value); got != tc.want {
			t.Fatalf("portablePreviewReference(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestLegacyScriptMigrationRequiresMatchingManagedFile(t *testing.T) {
	oldRoot := filepath.Join(t.TempDir(), "crawler-scripts")
	newRoot := filepath.Join(t.TempDir(), "crawler-scripts")
	for _, root := range []string{oldRoot, newRoot} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldPath := filepath.Join(oldRoot, "demo.py")
	newPath := filepath.Join(newRoot, "demo.py")
	for path, content := range map[string]string{oldPath: "external script", newPath: "imported script"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if relocatedImportedScript(oldPath, newRoot) {
		t.Fatal("unrelated external script was classified as imported")
	}
	if err := os.WriteFile(newPath, []byte("external script"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !relocatedImportedScript(oldPath, newRoot) {
		t.Fatal("copied imported script was not recognized while original remains")
	}
}
