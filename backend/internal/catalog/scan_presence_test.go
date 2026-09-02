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

	confirmed, err := cat.ConfirmMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{FullDriveScan: true}, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", map[string]struct{}{"file": {}}, ScanPresenceScope{FullDriveScan: true}, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("live snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"other-dir": {}},
	}, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("unvisited partial snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"dir": {}},
	}, 2)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first eligible missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.ConfirmMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"dir": {}},
	}, 2)
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
	if _, err := cat.ConfirmMissingDriveFiles(context.Background(), "drive", nil, ScanPresenceScope{FullDriveScan: true}, 1); err == nil {
		t.Fatal("unsafe threshold was accepted")
	}
}

func TestMissingFileEligibilityUsesDirectoryClassification(t *testing.T) {
	tests := []struct {
		name  string
		video scanPresenceVideo
		scope ScanPresenceScope
		want  bool
	}{
		{
			name:  "file missing below fully enumerated chain",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "parent"}},
			scope: ScanPresenceScope{EnumeratedDirIDs: stringSet("root", "parent")},
			want:  true,
		},
		{
			name:  "chain starts outside partial scan",
			video: scanPresenceVideo{ancestorDirIDs: []string{"outside", "parent"}},
			scope: ScanPresenceScope{EnumeratedDirIDs: stringSet("root")},
			want:  false,
		},
		{
			name:  "failed subtree protected",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "failed", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs: stringSet("root"),
				FailedDirIDs:     stringSet("failed"),
				FullDriveScan:    true,
			},
			want: false,
		},
		{
			name:  "excluded subtree left to policy cleanup",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "excluded", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs: stringSet("root"),
				ExcludedDirIDs:   stringSet("excluded"),
				FullDriveScan:    true,
			},
			want: false,
		},
		{
			name:  "enumerated parent proves child directory was removed",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "removed", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs: stringSet("root"),
				FullDriveScan:    true,
			},
			want: true,
		},
		{
			name:  "legacy first ancestor uses clean full-scan fallback",
			video: scanPresenceVideo{parentID: "legacy-parent"},
			scope: ScanPresenceScope{
				EnumeratedDirIDs: stringSet("root"),
				FullDriveScan:    true,
			},
			want: true,
		},
		{
			name:  "legacy first ancestor protected when any directory failed",
			video: scanPresenceVideo{parentID: "legacy-parent"},
			scope: ScanPresenceScope{
				EnumeratedDirIDs: stringSet("root"),
				FailedDirIDs:     stringSet("failed"),
				FullDriveScan:    true,
			},
			want: false,
		},
		{
			name:  "empty legacy chain uses clean full-scan fallback",
			video: scanPresenceVideo{},
			scope: ScanPresenceScope{FullDriveScan: true},
			want:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := missingFileEligible(test.video, test.scope); got != test.want {
				t.Fatalf("eligible = %v, want %v", got, test.want)
			}
		})
	}
}

func stringSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
