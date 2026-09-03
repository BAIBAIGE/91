package catalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEvaluateMissingDriveFilesRequiresConsecutiveEligibleScans(t *testing.T) {
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

	confirmed, err := cat.EvaluateMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{PresenceAuthoritative: true}, MissingFileCleanupConfirmTwice)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.EvaluateMissingDriveFiles(ctx, "drive", map[string]struct{}{"file": {}}, ScanPresenceScope{PresenceAuthoritative: true}, MissingFileCleanupConfirmTwice)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("live snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.EvaluateMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"other-dir": {}},
	}, MissingFileCleanupConfirmTwice)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("unvisited partial snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.EvaluateMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"dir": {}},
	}, MissingFileCleanupConfirmTwice)
	if err != nil || len(confirmed) != 0 {
		t.Fatalf("first eligible missing snapshot = %#v, err=%v", confirmed, err)
	}
	confirmed, err = cat.EvaluateMissingDriveFiles(ctx, "drive", nil, ScanPresenceScope{
		EnumeratedDirIDs: map[string]struct{}{"dir": {}},
	}, MissingFileCleanupConfirmTwice)
	if err != nil {
		t.Fatalf("second eligible snapshot: %v", err)
	}
	if _, ok := confirmed["file"]; !ok || len(confirmed) != 1 {
		t.Fatalf("confirmed = %#v, want file", confirmed)
	}
}

func TestEvaluateMissingDriveFilesImmediatelyWithoutMarking(t *testing.T) {
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
	scope := ScanPresenceScope{
		EnumeratedDirIDs:      stringSet("dir"),
		PresenceAuthoritative: true,
	}
	if _, err := cat.EvaluateMissingDriveFiles(ctx, "drive", nil,
		scope, MissingFileCleanupConfirmTwice); err != nil {
		t.Fatalf("record guarded miss: %v", err)
	}

	confirmed, err := cat.EvaluateMissingDriveFiles(ctx, "drive", nil,
		scope, MissingFileCleanupImmediate)
	if err != nil {
		t.Fatalf("evaluate immediate miss: %v", err)
	}
	if _, ok := confirmed["file"]; !ok || len(confirmed) != 1 {
		t.Fatalf("confirmed = %#v, want file", confirmed)
	}
	var marks int
	if err := cat.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM drive_scan_misses WHERE drive_id = 'drive' AND file_id = 'file'`).Scan(&marks); err != nil {
		t.Fatalf("count missing marks: %v", err)
	}
	if marks != 0 {
		t.Fatalf("missing marks = %d, want 0", marks)
	}
	if _, err := cat.GetVideo(ctx, "drive-file"); err != nil {
		t.Fatalf("evaluation deleted catalog row: %v", err)
	}
}

func TestEvaluateMissingDriveFilesRejectsInvalidMode(t *testing.T) {
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()
	if _, err := cat.EvaluateMissingDriveFiles(context.Background(), "drive", nil,
		ScanPresenceScope{PresenceAuthoritative: true}, MissingFileCleanupMode(99)); err == nil {
		t.Fatal("invalid cleanup mode was accepted")
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
			name:  "chain starts outside incomplete scan",
			video: scanPresenceVideo{ancestorDirIDs: []string{"outside", "parent"}},
			scope: ScanPresenceScope{EnumeratedDirIDs: stringSet("root")},
			want:  false,
		},
		{
			name:  "chain starts outside complete configured scope",
			video: scanPresenceVideo{ancestorDirIDs: []string{"old-root", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("new-root"),
				PresenceAuthoritative: true,
			},
			want: true,
		},
		{
			name:  "unlocated chain protected while skip-policy backfill is incomplete",
			video: scanPresenceVideo{ancestorDirIDs: []string{"old-root", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("new-root"),
				PresenceAuthoritative: true,
				ProtectUnlocated:      true,
			},
			want: false,
		},
		{
			name:  "failed subtree protected",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "failed", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("root"),
				FailedDirIDs:          stringSet("failed"),
				PresenceAuthoritative: true,
			},
			want: false,
		},
		{
			name:  "excluded subtree left to policy cleanup",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "excluded", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("root"),
				ExcludedDirIDs:        stringSet("excluded"),
				PresenceAuthoritative: true,
			},
			want: false,
		},
		{
			name:  "enumerated parent proves child directory was removed",
			video: scanPresenceVideo{ancestorDirIDs: []string{"root", "removed", "parent"}},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("root"),
				PresenceAuthoritative: true,
			},
			want: true,
		},
		{
			name:  "legacy first ancestor uses authoritative-scope fallback",
			video: scanPresenceVideo{parentID: "legacy-parent"},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("root"),
				PresenceAuthoritative: true,
			},
			want: true,
		},
		{
			name:  "legacy first ancestor protected when any directory failed",
			video: scanPresenceVideo{parentID: "legacy-parent"},
			scope: ScanPresenceScope{
				EnumeratedDirIDs:      stringSet("root"),
				FailedDirIDs:          stringSet("failed"),
				PresenceAuthoritative: true,
			},
			want: false,
		},
		{
			name:  "empty legacy chain uses authoritative-scope fallback",
			video: scanPresenceVideo{},
			scope: ScanPresenceScope{PresenceAuthoritative: true},
			want:  true,
		},
		{
			name:  "empty legacy chain protected while skip-policy backfill is incomplete",
			video: scanPresenceVideo{},
			scope: ScanPresenceScope{
				PresenceAuthoritative: true,
				ProtectUnlocated:      true,
			},
			want: false,
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
