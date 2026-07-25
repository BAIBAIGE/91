package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestVisitReactionFollowsThreeStateTransitions(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	seedReactionTestVideo(t, cat, "video-1", 10, 2)

	assertReaction := func(
		visitID string,
		reaction VideoReaction,
		wantLikes int,
		wantDislikes int,
	) {
		t.Helper()
		got, err := cat.SetVisitReaction(ctx, "video-1", visitID, reaction)
		if err != nil {
			t.Fatalf("set reaction %q for %s: %v", reaction, visitID, err)
		}
		if got.Reaction != reaction ||
			got.Likes != wantLikes ||
			got.Dislikes != wantDislikes {
			t.Fatalf(
				"reaction result = %#v, want reaction=%q likes=%d dislikes=%d",
				got,
				reaction,
				wantLikes,
				wantDislikes,
			)
		}
	}

	const firstVisit = "visit-0000000000000001"
	assertReaction(firstVisit, VideoReactionLike, 11, 2)
	assertReaction(firstVisit, VideoReactionLike, 11, 2) // idempotent retry
	assertReaction(firstVisit, VideoReactionDislike, 10, 3)
	assertReaction(firstVisit, VideoReactionNone, 10, 2)
	assertReaction(firstVisit, VideoReactionNone, 10, 2) // idempotent retry

	// A refreshed/re-entered page carries a new visit ID and therefore starts
	// from none even though an older visit for the same video already exists.
	const secondVisit = "visit-0000000000000002"
	assertReaction(secondVisit, VideoReactionDislike, 10, 3)
	assertReaction(secondVisit, VideoReactionLike, 11, 2)
}

func TestVisitReactionRejectsInvalidInputAndUnavailableVideos(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	seedReactionTestVideo(t, cat, "video-1", 0, 0)

	if _, err := cat.SetVisitReaction(
		ctx,
		"video-1",
		"short",
		VideoReactionLike,
	); !errors.Is(err, ErrInvalidVideoReactionVisitID) {
		t.Fatalf("short visit id error = %v, want invalid visit id", err)
	}
	if _, err := cat.SetVisitReaction(
		ctx,
		"video-1",
		"visit-0000000000000001",
		VideoReaction("love"),
	); !errors.Is(err, ErrInvalidVideoReaction) {
		t.Fatalf("invalid reaction error = %v, want invalid reaction", err)
	}
	if _, err := cat.SetVisitReaction(
		ctx,
		"missing-video",
		"visit-0000000000000001",
		VideoReactionLike,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing video error = %v, want sql.ErrNoRows", err)
	}
}

func TestConcurrentIdenticalVisitReactionOnlyCountsOnce(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	seedReactionTestVideo(t, cat, "video-1", 0, 0)

	const visitID = "visit-concurrent-00000001"
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := cat.SetVisitReaction(
				ctx,
				"video-1",
				visitID,
				VideoReactionLike,
			)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent set reaction: %v", err)
		}
	}

	video, err := cat.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if video.Likes != 1 || video.Dislikes != 0 {
		t.Fatalf("counts after concurrent retry = %d/%d, want 1/0", video.Likes, video.Dislikes)
	}
}

func TestDeletingVideoDeletesVisitReactions(t *testing.T) {
	ctx := context.Background()
	cat, err := Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	seedReactionTestVideo(t, cat, "video-1", 0, 0)

	if _, err := cat.SetVisitReaction(
		ctx,
		"video-1",
		"visit-0000000000000001",
		VideoReactionLike,
	); err != nil {
		t.Fatalf("set reaction: %v", err)
	}
	if err := cat.DeleteVideo(ctx, "video-1"); err != nil {
		t.Fatalf("delete video: %v", err)
	}

	var count int
	if err := cat.db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM video_reaction_visits WHERE video_id = ?`,
		"video-1",
	).Scan(&count); err != nil {
		t.Fatalf("count visit reactions: %v", err)
	}
	if count != 0 {
		t.Fatalf("visit reactions after delete = %d, want 0", count)
	}
}

func seedReactionTestVideo(
	t *testing.T,
	cat *Catalog,
	id string,
	likes int,
	dislikes int,
) {
	t.Helper()
	now := time.Now()
	if err := cat.UpsertVideo(context.Background(), &Video{
		ID:          id,
		DriveID:     "drive-1",
		FileID:      "file-1",
		Title:       "Reaction test video",
		Likes:       likes,
		Dislikes:    dislikes,
		PublishedAt: now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed video: %v", err)
	}
}
