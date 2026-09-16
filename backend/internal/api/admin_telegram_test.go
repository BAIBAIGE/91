package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/catalog"
)

func TestAdminTelegramVideoFilterSurvivesCloudMigrationAndIgnoresTags(t *testing.T) {
	ctx := context.Background()
	c := openRemoteUploadAPICatalog(t)
	if _, _, err := c.TelegramOffset(ctx, 123); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 2; i++ {
		id := fmt.Sprintf("tg-%d", i)
		if err := c.AcceptTelegramUpdate(ctx, catalog.TelegramReceipt{BotID: 123, UpdateID: i, MessageID: i, ChatID: 42, SenderID: 42}, &catalog.TelegramSource{BotID: 123, SenderID: 42, FileID: id, UniqueID: id, Size: 5}, id, id, 100); err != nil {
			t.Fatal(err)
		}
		if err := c.TransitionRemoteUploadJob(ctx, id, catalog.RemoteUploadQueued, catalog.RemoteUploadSaving); err != nil {
			t.Fatal(err)
		}
		if err := c.FinalizeRemoteUpload(ctx, id, &catalog.Video{ID: id, DriveID: "local-upload", FileID: id + ".mp4", FileName: id + ".mp4", Title: id, Size: 5}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.UpsertDrive(ctx, &catalog.Drive{ID: "cloud", Name: "Cloud", Kind: "webdav", RootID: "/", Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := c.MigrateVideoToDrive(ctx, "tg-2", catalog.VideoDriveMigration{DriveID: "cloud", FileID: "remote-file"}); err != nil {
		t.Fatal(err)
	}
	// Removing TG from actual imports, or attaching it to ordinary uploads,
	// must never change membership of the provenance filter.
	tag, err := c.EnsureTag(ctx, "TG", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteTag(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertVideo(ctx, &catalog.Video{ID: "ordinary", DriveID: "local-upload", FileID: "ordinary.mp4", Title: "ordinary", Tags: []string{"TG"}, Size: 5}); err != nil {
		t.Fatal(err)
	}
	a := &AdminServer{Catalog: c}
	for _, tc := range []struct {
		query        string
		total, count int
		wantID       string
	}{
		{"sourceKind=telegram", 2, 2, ""},
		{"sourceKind=telegram&driveId=local-upload", 1, 1, "tg-1"},
		{"sourceKind=telegram&driveId=cloud", 1, 1, "tg-2"},
		{"sourceKind=telegram&keyword=tg-2", 1, 1, "tg-2"},
		{"sourceKind=telegram&size=1&page=2", 2, 1, ""},
		{"sourceKind=telegram&keyword=ordinary", 0, 0, ""},
	} {
		w := httptest.NewRecorder()
		a.handleAdminListVideos(w, httptest.NewRequest("GET", "/admin/api/videos?"+tc.query, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		var response struct {
			Items []catalog.Video `json:"items"`
			Total int             `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Total != tc.total || len(response.Items) != tc.count || (tc.wantID != "" && response.Items[0].ID != tc.wantID) {
			t.Fatalf("%s: %+v", tc.query, response)
		}
	}
	w := httptest.NewRecorder()
	a.handleAdminListVideos(w, httptest.NewRequest("GET", "/admin/api/videos?sourceKind=unsupported", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown source silently ignored: %d", w.Code)
	}
}

func TestTelegramAdminRoutesRejectAnonymousAndViewers(t *testing.T) {
	c := openRemoteUploadAPICatalog(t)
	a := &auth.Authenticator{Catalog: c}
	r := chi.NewRouter()
	(&AdminServer{Catalog: c, Auth: a}).Register(r)
	hash, err := auth.HashPassword("viewer-secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.CreateUser(context.Background(), "tg-viewer", hash, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.CreateSessionUntil(context.Background(), "viewer", time.Now().Add(time.Hour), id); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/config.yaml"}, {"PUT", "/config.yaml"}, {"GET", "/telegram/status"}, {"POST", "/telegram/test"}, {"POST", "/telegram/prepare-polling"}, {"POST", "/telegram/resume"},
		{"GET", "/import-jobs"}, {"POST", "/import-jobs/job/cancel"}, {"POST", "/import-jobs/job/retry"},
	} {
		for _, viewer := range []bool{false, true} {
			req := httptest.NewRequest(route.method, "/admin/api"+route.path, strings.NewReader("{}"))
			expected := 401
			if viewer {
				req.AddCookie(&http.Cookie{Name: "vs_admin", Value: "viewer"})
				expected = 403
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != expected {
				t.Fatalf("%s viewer=%v status=%d", route.path, viewer, w.Code)
			}
		}
	}
}
func TestImportAPIHidesPrivateSourceAndUsesCursor(t *testing.T) {
	ctx := context.Background()
	c := openRemoteUploadAPICatalog(t)
	if _, _, err := c.TelegramOffset(ctx, 123); err != nil {
		t.Fatal(err)
	}
	source := &catalog.TelegramSource{BotID: 123, SenderID: 42, FileID: "PRIVATE_FILE_ID", UniqueID: "PRIVATE_UNIQUE", FileName: "raw-name.mp4"}
	if err := c.AcceptTelegramUpdate(ctx, catalog.TelegramReceipt{BotID: 123, ChatID: 42, MessageID: 1, UpdateID: 1, SenderID: 42}, source, "tg-job", "video", 100); err != nil {
		t.Fatal(err)
	}
	server := &AdminServer{Catalog: c}
	rr := httptest.NewRecorder()
	server.handleImportList(rr, httptest.NewRequest("GET", "/admin/api/import-jobs?source=telegram", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "PRIVATE") || strings.Contains(rr.Body.String(), "sourcePayload") {
		t.Fatal("private source leaked")
	}
	var jobs []ImportJobDTO
	if err := json.Unmarshal(rr.Body.Bytes(), &jobs); err != nil || len(jobs) != 1 || jobs[0].SenderID != "42" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	rr = httptest.NewRecorder()
	server.handleImportList(rr, httptest.NewRequest("GET", "/admin/api/import-jobs?before="+jobs[0].Sequence, nil))
	if rr.Code != 200 || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("cursor response %d %s", rr.Code, rr.Body.String())
	}
}
