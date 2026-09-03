package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestScanCleanupSchemaMigratesExistingCatalog(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "catalog.db")
	cat, err := Open(databasePath)
	if err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	if err := cat.UpsertDrive(ctx, &Drive{ID: "drive", Kind: "fake", Name: "Drive", RootID: "root"}); err != nil {
		t.Fatalf("seed drive: %v", err)
	}
	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID: "legacy-video", DriveID: "drive", FileID: "file", ParentID: "legacy-parent",
		Title: "Legacy", PublishedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("close seeded catalog: %v", err)
	}

	legacyDB, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open catalog for legacy schema setup: %v", err)
	}
	for _, statement := range []string{
		`ALTER TABLE videos DROP COLUMN ancestor_dir_ids`,
		`ALTER TABLE drives DROP COLUMN skip_cleanup_dir_ids`,
		`DROP TABLE drive_skip_cleanup_legacy_dirs`,
	} {
		if _, err := legacyDB.ExecContext(ctx, statement); err != nil {
			legacyDB.Close()
			t.Fatalf("prepare legacy schema with %q: %v", statement, err)
		}
	}
	if err := legacyDB.Close(); err != nil {
		t.Fatalf("close legacy schema setup: %v", err)
	}

	cat, err = Open(databasePath)
	if err != nil {
		t.Fatalf("open and migrate legacy catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	video, err := cat.GetVideo(ctx, "legacy-video")
	if err != nil {
		t.Fatalf("get migrated video: %v", err)
	}
	if video.AncestorDirIDs != nil || video.ParentID != "legacy-parent" {
		t.Fatalf("migrated video ancestry = %#v / %q, want nil / legacy-parent", video.AncestorDirIDs, video.ParentID)
	}
	state, err := cat.GetDriveSkipCleanupState(ctx, "drive")
	if err != nil {
		t.Fatalf("get migrated cleanup state: %v", err)
	}
	if state.Initialized || len(state.LegacyDoneDirIDs) != 0 {
		t.Fatalf("migrated cleanup state = %#v, want never run", state)
	}
}

func TestListVideosInAncestorDirsUsesStoredChainAndLegacyParentFallback(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	for _, video := range []*Video{
		{
			ID: "chained", DriveID: "drive", FileID: "chained-file", ParentID: "/skip/deep",
			AncestorDirIDs: []string{"/", "/skip", "/skip/deep"}, Title: "Chained", Size: 1,
		},
		{
			ID: "legacy", DriveID: "drive", FileID: "legacy-file", ParentID: "/skip",
			Title: "Legacy", Size: 2,
		},
		{
			ID: "other", DriveID: "drive", FileID: "other-file", ParentID: "/other",
			AncestorDirIDs: []string{"/", "/other"}, Title: "Other", Size: 3,
		},
	} {
		video.PublishedAt = now
		video.CreatedAt = now
		video.UpdatedAt = now
		if err := cat.UpsertVideo(ctx, video); err != nil {
			t.Fatalf("upsert %s: %v", video.ID, err)
		}
	}

	items, err := cat.ListVideosInAncestorDirs(ctx, "drive", []string{"/skip"})
	if err != nil {
		t.Fatalf("list videos in ancestor directory: %v", err)
	}
	if got, want := scanCleanupVideoIDs(items), []string{"chained", "legacy"}; !sameStringSlice(got, want) {
		t.Fatalf("video IDs = %#v, want %#v", got, want)
	}
	if hasLegacy, err := cat.DriveHasVideosWithoutAncestorDirIDs(ctx, "drive"); err != nil || !hasLegacy {
		t.Fatalf("has videos without ancestor IDs = %v, error = %v; want true/nil", hasLegacy, err)
	}

	stored, err := cat.GetVideo(ctx, "chained")
	if err != nil {
		t.Fatalf("get chained video: %v", err)
	}
	if got, want := stored.AncestorDirIDs, []string{"/", "/skip", "/skip/deep"}; !sameStringSlice(got, want) {
		t.Fatalf("stored ancestor IDs = %#v, want %#v", got, want)
	}

	if err := cat.UpdateVideoMeta(ctx, "legacy", VideoMetaPatch{
		AncestorDirIDs:    []string{},
		AncestorDirIDsSet: true,
	}); err != nil {
		t.Fatalf("store known empty ancestor chain: %v", err)
	}
	items, err = cat.ListVideosInAncestorDirs(ctx, "drive", []string{"/skip"})
	if err != nil {
		t.Fatalf("list after empty chain patch: %v", err)
	}
	if got, want := scanCleanupVideoIDs(items), []string{"chained"}; !sameStringSlice(got, want) {
		t.Fatalf("video IDs after empty chain = %#v, want %#v", got, want)
	}
	if hasLegacy, err := cat.DriveHasVideosWithoutAncestorDirIDs(ctx, "drive"); err != nil || hasLegacy {
		t.Fatalf("has videos without ancestor IDs = %v, error = %v; want false/nil", hasLegacy, err)
	}
}

func TestDriveSkipCleanupStateDistinguishesNeverRunFromEmptyList(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	if err := cat.UpsertDrive(ctx, &Drive{ID: "drive", Kind: "fake", Name: "Drive", RootID: "root"}); err != nil {
		t.Fatalf("upsert drive: %v", err)
	}

	state, err := cat.GetDriveSkipCleanupState(ctx, "drive")
	if err != nil {
		t.Fatalf("get initial cleanup state: %v", err)
	}
	if state.Initialized || len(state.LegacyDoneDirIDs) != 0 {
		t.Fatalf("initial state = %#v, want never run", state)
	}
	if err := cat.SetDriveSkipCleanupDirIDs(ctx, "drive", nil); err != nil {
		t.Fatalf("record empty cleanup list: %v", err)
	}
	if err := cat.MarkDriveSkipCleanupLegacyDirDone(ctx, "drive", "skip-dir"); err != nil {
		t.Fatalf("mark legacy directory cleanup done: %v", err)
	}

	state, err = cat.GetDriveSkipCleanupState(ctx, "drive")
	if err != nil {
		t.Fatalf("get completed cleanup state: %v", err)
	}
	if !state.Initialized || len(state.DirIDs) != 0 || !sameStringSlice(state.LegacyDoneDirIDs, []string{"skip-dir"}) {
		t.Fatalf("completed state = %#v, want initialized empty list and completed skip-dir", state)
	}
}

func TestDeleteVideoRemovesDriveScanMiss(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID: "video", DriveID: "drive", FileID: "file", ParentID: "parent",
		AncestorDirIDs: []string{"root", "parent"}, Title: "Video", Size: 1,
		PublishedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert video: %v", err)
	}
	if _, err := cat.EvaluateMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs:      stringSet("root", "parent"),
		PresenceAuthoritative: true,
	}, MissingFileCleanupConfirmTwice); err != nil {
		t.Fatalf("record missing file: %v", err)
	}
	if err := cat.DeleteVideo(ctx, "video"); err != nil {
		t.Fatalf("delete video: %v", err)
	}
	var count int
	if err := cat.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM drive_scan_misses WHERE drive_id = 'drive' AND file_id = 'file'`).Scan(&count); err != nil {
		t.Fatalf("count scan misses: %v", err)
	}
	if count != 0 {
		t.Fatalf("scan miss rows = %d, want 0", count)
	}
}

func scanCleanupVideoIDs(videos []*Video) []string {
	ids := make([]string, 0, len(videos))
	for _, video := range videos {
		ids = append(ids, video.ID)
	}
	return ids
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
