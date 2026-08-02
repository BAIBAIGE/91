package catalog

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDropLegacyDuplicateReviewTableIsIdempotent(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	if dropped, err := cat.DropLegacyDuplicateReviewTable(ctx); err != nil || dropped {
		t.Fatalf("fresh catalog drop = %v, err=%v", dropped, err)
	}
	if _, err := cat.db.ExecContext(ctx, `
		CREATE TABLE duplicate_review_pairs (
			id INTEGER PRIMARY KEY,
			left_video_id TEXT NOT NULL,
			right_video_id TEXT NOT NULL,
			status TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("seed legacy table: %v", err)
	}
	if dropped, err := cat.DropLegacyDuplicateReviewTable(ctx); err != nil || !dropped {
		t.Fatalf("legacy catalog drop = %v, err=%v", dropped, err)
	}
	if dropped, err := cat.DropLegacyDuplicateReviewTable(ctx); err != nil || dropped {
		t.Fatalf("second drop = %v, err=%v", dropped, err)
	}
}
