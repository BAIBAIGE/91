package catalog

import (
	"context"
	"slices"
	"testing"
)

func finalizeTaggedImport(t *testing.T, c *Catalog, id, kind string, manual []string, auto []TagAssignment) *Video {
	t.Helper()
	ctx := context.Background()
	if kind == "telegram" {
		enqueueTelegram(t, c, 1, id, id)
	} else if _, err := c.CreateRemoteUploadJob(ctx, id, "https://example.com/video.mp4", "example.com", "TG video", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.TransitionRemoteUploadJob(ctx, id, RemoteUploadQueued, RemoteUploadSaving); err != nil {
		t.Fatal(err)
	}
	v := &Video{ID: "video-" + id, DriveID: "local-upload", FileID: id + ".mp4", FileName: id + ".mp4", Title: "TG video", Size: 5, Ext: "mp4"}
	if err := c.FinalizeRemoteUpload(ctx, id, v, manual, auto); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTelegramSourceTagCoexistsWithContentTags(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "automatic"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			c := telegramTestCatalog(t)
			ctx := context.Background()
			if _, err := c.EnsureTag(ctx, "content", "user"); err != nil {
				t.Fatal(err)
			}
			var selected []string
			if manual {
				selected = []string{"content"}
			}
			v := finalizeTaggedImport(t, c, "tg", "telegram", selected, []TagAssignment{{Label: "content", Source: "auto"}})
			if len(v.Tags) != 2 || !slices.Contains(v.Tags, "TG") || !slices.Contains(v.Tags, "content") {
				t.Fatalf("content tags lost: %v", v.Tags)
			}
			var locked bool
			if err := c.db.QueryRow(`SELECT tags_manual FROM videos WHERE id=?`, v.ID).Scan(&locked); err != nil || locked != manual {
				t.Fatalf("source tag changed manual lock: %v %v", locked, err)
			}
			var source string
			if err := c.db.QueryRow(`SELECT vt.source FROM video_tags vt JOIN tags t ON t.id=vt.tag_id WHERE vt.video_id=? AND t.label='TG'`, v.ID).Scan(&source); err != nil || source != "telegram" {
				t.Fatalf("wrong source metadata: %q %v", source, err)
			}
			matched, err := c.MatchTagAssignments(ctx, "TG video", "TG.mp4", "", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, tag := range matched {
				if tag.Label == "TG" {
					t.Fatal("source tag matched an unrelated title")
				}
			}
			other := finalizeTaggedImport(t, c, "http", "http", nil, matched)
			if slices.Contains(other.Tags, "TG") {
				t.Fatal("HTTP import received Telegram source tag")
			}
			if _, err := c.ReplaceAutoVideoTags(ctx, v.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := c.ResetGeneratedTagState(ctx); err != nil {
				t.Fatal(err)
			}
			saved, err := c.GetVideo(ctx, v.ID)
			if err != nil || !slices.Contains(saved.Tags, "TG") {
				t.Fatalf("source tag lost during maintenance: %v %v", saved, err)
			}
		})
	}
}
