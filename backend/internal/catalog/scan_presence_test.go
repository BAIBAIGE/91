package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestConfirmMissingDriveFilesRequiresConsecutiveEligibleScans(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()
	now := time.Now()
	if err := cat.UpsertVideo(ctx, &Video{
		ID: "drive-file", DriveID: "drive", FileID: "file", ParentID: "dir",
		Title: "video", PublishedAt: now, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertVideo: %v", err)
	}

	confirmed, err := cat.ConfirmMissingDriveFiles(ctx, "drive", nil, nil, true, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", map[string]struct{}{"file": {}}, nil, true, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("live snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, map[string]struct{}{"other-dir": {}}, false, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("unvisited partial snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, map[string]struct{}{"dir": {}}, false, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first eligible missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, map[string]struct{}{"dir": {}}, false, 2)
	if err != nil {
		t.Fatalf("second eligible snapshot: %v", err)
	}
	if _, ok := confirmed["file"]; !ok || len(confirmed) != 1 {
		t.Fatalf("confirmed = %#v, want file", confirmed)
	}
}

func TestConfirmMissingDriveFilesRejectsUnsafeThreshold(t *testing.T) {
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()
	if _, err := cat.ConfirmMissingDriveFiles(context.Background(), "drive", nil, nil, true, 1); err == nil {
		t.Fatal("unsafe threshold was accepted")
	}
}
