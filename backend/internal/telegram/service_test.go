package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/drives/telegramstorage"
	"github.com/video-site/backend/internal/mediaimport"
)

func testService(t *testing.T) (*Service, *catalog.Catalog) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cat.Close() })
	cfg := config.Telegram{Enabled: true, BotToken: "123:test", AllowedUserIDs: []int64{42}, LocalFilesRoot: t.TempDir()}
	cfg.APIFilesRoot = cfg.LocalFilesRoot
	if err = cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s := New(cfg, "123:test_token", cat, t.TempDir(), 1)
	s.botID = 123
	s.status.State = "connected"
	if _, _, err = cat.TelegramOffset(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	return s, cat
}
func videoUpdate(id, userID int64) update {
	m := &message{ID: id, From: user{ID: userID}, Caption: "海边日落", Video: &media{FileID: "file", UniqueID: "unique", Name: "sunset.mp4", Size: 5, MIME: "video/mp4"}}
	m.Chat.Type = "private"
	m.Chat.ID = userID
	return update{ID: id, Message: m}
}
func TestReceiptsDeduplicateAndRecoverCursor(t *testing.T) {
	s, cat := testService(t)
	ctx := context.Background()
	for _, u := range []update{videoUpdate(1, 42), videoUpdate(1, 42), videoUpdate(2, 42), videoUpdate(3, 99)} {
		if err := s.accept(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs=%v err=%v", jobs, err)
	}
	if len(jobs[0].Tags) != 0 {
		t.Fatalf("Telegram task must leave content tags to automatic matching: %v", jobs[0].Tags)
	}
	off, _, err := cat.TelegramOffset(ctx, 123)
	if err != nil || off != 4 {
		t.Fatalf("offset=%d err=%v", off, err)
	}
	replies, err := cat.PendingTelegramReceipts(ctx, 123)
	if err != nil || len(replies) != 2 {
		t.Fatalf("receipts=%v err=%v", replies, err)
	}
	if replies[0].JobID != replies[1].JobID {
		t.Fatal("duplicate file created separate tasks")
	}
	public, err := cat.ListRemoteUploadJobs(ctx, 30)
	if err != nil || len(public) != 0 {
		t.Fatal("Telegram tasks leaked into HTTP list")
	}
}
func TestQueueLimitsAndBootstrapID(t *testing.T) {
	s, cat := testService(t)
	s.cfg.MaxPendingJobs = 1
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	u := videoUpdate(2, 42)
	u.Message.Video.UniqueID = "second"
	if err := s.accept(ctx, u); err != nil {
		t.Fatal(err)
	}
	u = videoUpdate(3, 99)
	u.Message.Video = nil
	u.Message.Text = "/id"
	if err := s.accept(ctx, u); err != nil {
		t.Fatal(err)
	}
	receipts, err := cat.PendingTelegramReceipts(ctx, 123)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 3 || !strings.Contains(receipts[1].Response, "队列已满") || !strings.Contains(receipts[2].Response, "99") {
		t.Fatalf("receipts=%+v", receipts)
	}
}
func TestCachePathRejectsEscapesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.mp4")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	for _, path := range []string{outside, link, "relative.mp4", root} {
		f, _, err := openCachedFile(root, path)
		if err == nil {
			f.Close()
			t.Fatalf("accepted %s", path)
		}
	}
	file := filepath.Join(root, "video.mp4")
	if err := os.WriteFile(file, []byte("valid"), 0600); err != nil {
		t.Fatal(err)
	}
	f, _, err := openCachedFile(root, file)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
func TestClientSanitizesCredentialsAndRejectsRedirect(t *testing.T) {
	const token = "12345:PRIVATE_TOKEN"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed redirect") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	t.Setenv("TEST_TG_TOKEN", token)
	c, err := newClient(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	err = c.call(context.Background(), "getMe", struct{}{}, nil)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unsafe error: %v", err)
	}
}
func TestFetchUsesBotScopedFileAndVerifiesBytes(t *testing.T) {
	s, cat := testService(t)
	s.cfg.APIFilesRoot = "/bot-api/data"
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	jobs, _ := cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	file := filepath.Join(s.cfg.LocalFilesRoot, "video.mp4")
	if err := os.WriteFile(file, []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["file_id"] != "file" {
			t.Error("wrong file id")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"file_path": "/bot-api/data/video.mp4", "file_size": 5}})
	}))
	defer server.Close()
	s.client = &client{base: server.URL, token: "123:token", http: server.Client()}
	before, _ := os.Stat(file)
	var stages []string
	result, err := s.Fetch(ctx, jobs[0], func(stage string, done, total int64) error { stages = append(stages, stage); return nil })
	if err != nil || result.Size != 5 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if stages[0] != "telegram_download" || stages[len(stages)-1] != "telegram_download" {
		t.Fatal(stages)
	}
	after, e := os.Stat(result.Path)
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("acquisition copied or lost the original file")
	}
	if _, e := os.Stat(file); !os.IsNotExist(e) {
		t.Fatal("download cache copy remains")
	}
	if entries, e := os.ReadDir(s.uploadDir); e != nil || len(entries) != 0 {
		t.Fatal("TG acquisition wrote into upload storage")
	}
	// Moving the complete directory preserves the acquisition identity.
	relocated := filepath.Join(t.TempDir(), "relocated")
	if err := os.Rename(s.cfg.LocalFilesRoot, relocated); err != nil {
		t.Fatal(err)
	}
	s.cfg.LocalFilesRoot = relocated
	s.client = nil
	resumed, e := s.Fetch(ctx, jobs[0], func(string, int64, int64) error { return nil })
	if e != nil || resumed.Path != filepath.Join(relocated, "library", result.FileID) {
		t.Fatalf("acquisition cannot resume: %+v %v", resumed, e)
	}
	s.cfg.AllowedUserIDs = nil
	_, err = s.Fetch(ctx, jobs[0], func(string, int64, int64) error { return nil })
	if err == nil {
		t.Fatal("revoked sender was allowed")
	}
}
func TestNotificationFailureDoesNotChangeTask(t *testing.T) {
	s, cat := testService(t)
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	jobs, _ := cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	receipts, _ := cat.PendingTelegramReceipts(ctx, 123)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 429, "parameters": map[string]int{"retry_after": 60}})
	}))
	defer server.Close()
	c := &client{base: server.URL, token: "123:token", http: server.Client()}
	s.notifyOne(ctx, c, receipts[0])
	job, err := cat.GetRemoteUploadJob(ctx, jobs[0].ID)
	if err != nil || job.State != catalog.RemoteUploadQueued {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	ready, _ := cat.PendingTelegramReceipts(ctx, 123)
	if len(ready) != 0 {
		t.Fatal("ignored notification retry_after")
	}
}
func TestTelegramImportEndToEndWithLocalBotAPI(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	s, cat := testService(t)
	// The fixed source tag must not require configuration or pre-creation.
	fixture := filepath.Join(s.cfg.LocalFilesRoot, "fixture.mp4")
	command := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=32x32:d=0.2", "-c:v", "mpeg4", "-y", fixture)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	info, _ := os.Stat(fixture)
	u := videoUpdate(10, 42)
	u.Message.Video.Size = info.Size()
	var mu sync.Mutex
	sent := []string{}
	delivered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		method := filepath.Base(r.URL.Path)
		var result any
		switch method {
		case "getMe":
			result = map[string]any{"id": 123, "username": "test_bot"}
		case "getWebhookInfo":
			result = map[string]any{"url": ""}
		case "getUpdates":
			mu.Lock()
			if !delivered {
				result = []update{u}
				delivered = true
			} else {
				result = []update{}
			}
			mu.Unlock()
			if len(result.([]update)) == 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		case "getFile":
			result = map[string]any{"file_path": fixture, "file_size": info.Size()}
		case "sendMessage", "editMessageText":
			var input map[string]any
			_ = json.NewDecoder(r.Body).Decode(&input)
			mu.Lock()
			sent = append(sent, input["text"].(string))
			mu.Unlock()
			result = map[string]any{"message_id": 55}
		default:
			t.Errorf("unexpected method %s", method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer server.Close()
	t.Setenv("TEST_TG_TOKEN", "123:token")
	s.token = "123:token"
	s.cfg.APIBaseURL = server.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saved := make(chan *catalog.Video, 1)
	manager, err := mediaimport.New(mediaimport.Config{Catalog: cat, UploadDir: s.uploadDir, DiskReserve: 1, TelegramSource: s, OnVideoUploaded: func(v *catalog.Video) { saved <- v }})
	if err != nil {
		t.Fatal(err)
	}
	s.SetWake(manager.Wake)
	if err = manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	defer func() {
		cancel()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(shutdown)
		_ = manager.Shutdown(shutdown)
	}()
	var v *catalog.Video
	select {
	case v = <-saved:
	case <-time.After(10 * time.Second):
		t.Fatal("video was not imported")
	}
	if len(v.Tags) != 1 || v.Tags[0] != "TG" {
		t.Fatalf("Telegram source tag missing or duplicated: %v", v.Tags)
	}
	if v.Author != "" {
		t.Fatalf("Telegram import without author must remain empty: %q", v.Author)
	}
	if v.DriveID != telegramstorage.DriveID {
		t.Fatalf("wrong storage: %s", v.DriveID)
	}
	storedPath, err := telegramstorage.New(cat, func() string { return s.cfg.LocalFilesRoot }).LocalPath(ctx, v.FileID)
	if err != nil {
		t.Fatal(err)
	}
	moved, e := os.Stat(storedPath)
	if e != nil || !os.SameFile(info, moved) {
		t.Fatal("import did not keep original inode")
	}
	if entries, e := os.ReadDir(s.uploadDir); e != nil || len(entries) != 0 {
		t.Fatal("import copied video into uploads")
	}
	data, err := os.ReadFile(storedPath)
	if err != nil || int64(len(data)) != info.Size() {
		t.Fatalf("saved bytes=%d err=%v", len(data), err)
	}
	if err = s.accept(ctx, u); err != nil {
		t.Fatal(err)
	}
	u.ID++
	u.Message.ID++
	if err = s.accept(ctx, u); err != nil {
		t.Fatal(err)
	}
	jobs, err := cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	if err != nil || len(jobs) != 1 || jobs[0].State != "completed" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		found := false
		for _, body := range sent {
			found = found || strings.Contains(body, "已保存")
		}
		mu.Unlock()
		if found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("completion notification was not sent")
}

func TestInvalidAcquisitionIsDiscardedWithoutTouchingBotState(t *testing.T) {
	s, cat := testService(t)
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	jobs, _ := cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	root := filepath.Join(s.cfg.LocalFilesRoot, "library")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	id := jobs[0].ID + ".media"
	if _, err := cat.ReserveTelegramLocalFile(ctx, catalog.TelegramLocalFile{FileID: id, JobID: jobs[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	botState := filepath.Join(s.cfg.LocalFilesRoot, "panel-status.json")
	if err := os.WriteFile(botState, []byte("state"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := mediaimport.New(mediaimport.Config{Catalog: cat, UploadDir: s.uploadDir, DiskReserve: 1, TelegramSource: s, FFprobePath: "/does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, e := cat.GetRemoteUploadJob(ctx, jobs[0].ID)
		if e != nil {
			t.Fatal(e)
		}
		if job.State == "failed" {
			if _, e := os.Stat(filepath.Join(root, id)); !os.IsNotExist(e) {
				t.Fatal("invalid library file remains")
			}
			if data, e := os.ReadFile(botState); e != nil || string(data) != "state" {
				t.Fatal("Bot API state was touched")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("invalid acquisition was not finalized")
}
