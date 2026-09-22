package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
)

func TestProgressNotificationEditsOriginalAndSkipsUnchangedContent(t *testing.T) {
	s, cat := testService(t)
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	receipts, err := cat.PendingTelegramReceipts(ctx, 123)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%v err=%v", receipts, err)
	}
	r := receipts[0]
	type sentMessage struct {
		method string
		text   string
		id     float64
	}
	sent := make(chan sentMessage, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["parse_mode"] != "HTML" {
			t.Errorf("missing HTML parse mode: %+v", input)
		}
		if filepath.Base(req.URL.Path) == "sendMessage" {
			reply, ok := input["reply_parameters"].(map[string]any)
			if !ok || reply["message_id"] != float64(r.MessageID) || reply["allow_sending_without_reply"] != true {
				t.Errorf("notification is not linked to the original video: %+v", input)
			}
		} else if _, ok := input["reply_parameters"]; ok {
			t.Errorf("edit request contains send-only reply parameters: %+v", input)
		}
		id, _ := input["message_id"].(float64)
		sent <- sentMessage{filepath.Base(req.URL.Path), input["text"].(string), id}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]int{"message_id": 51}})
	}))
	defer server.Close()
	c := &client{base: server.URL, token: "123:token", http: server.Client()}
	s.notifyOne(ctx, c, r)
	first := <-sent
	if first.method != "sendMessage" || !strings.Contains(first.text, "已加入队列") {
		t.Fatalf("first notification=%+v", first)
	}
	if err := cat.TransitionRemoteUploadJob(ctx, r.JobID, catalog.RemoteUploadQueued, catalog.RemoteUploadDownloading); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetImportStage(ctx, r.JobID, "telegram_download"); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateRemoteUploadProgress(ctx, r.JobID, 512<<20, 1<<30); err != nil {
		t.Fatal(err)
	}
	r.ReplyID = 51
	s.notifyOne(ctx, c, r)
	progress := <-sent
	if progress.method != "editMessageText" || progress.id != 51 || !strings.Contains(progress.text, "正在从 TG 获取") || strings.Contains(progress.text, "%") {
		t.Fatalf("progress notification=%+v", progress)
	}
	if pending, err := cat.PendingTelegramReceipts(ctx, 123); err != nil || len(pending) != 0 {
		t.Fatalf("progress cooldown not applied: %v %v", pending, err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for {
		receipts, err = cat.PendingTelegramReceipts(ctx, 123)
		if err != nil {
			t.Fatal(err)
		}
		if len(receipts) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("active progress was never revisited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	r = receipts[0]
	s.notifyOne(ctx, c, r)
	if len(sent) != 0 {
		t.Fatal("unchanged progress caused a duplicate edit")
	}
	if err := cat.FailRemoteUploadJob(ctx, r.JobID, "测试失败"); err != nil {
		t.Fatal(err)
	}
	s.notifyOne(ctx, c, r)
	final := <-sent
	if final.method != "editMessageText" || final.id != 51 || !strings.Contains(final.text, "保存失败") {
		t.Fatalf("final notification=%+v", final)
	}
}

func TestDeletedNotificationIsResentWithFormattingAndReplyContext(t *testing.T) {
	s, cat := testService(t)
	ctx := context.Background()
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	receipts, err := cat.PendingTelegramReceipts(ctx, s.BotID())
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%v err=%v", receipts, err)
	}
	r := receipts[0]
	r.ReplyID = 51
	if err := cat.FailRemoteUploadJob(ctx, r.JobID, "读取 <video> 失败"); err != nil {
		t.Fatal(err)
	}
	type request struct {
		method string
		body   map[string]any
	}
	sent := make(chan request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		method := filepath.Base(req.URL.Path)
		sent <- request{method, input}
		if method == "editMessageText" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]int{"message_id": 52}})
	}))
	defer server.Close()
	s.notifyOne(ctx, &client{base: server.URL, token: "123:token", http: server.Client()}, r)
	if len(sent) != 2 {
		t.Fatalf("expected an edit followed by a resend, got %d requests", len(sent))
	}
	edit, resend := <-sent, <-sent
	if edit.method != "editMessageText" || edit.body["message_id"] != float64(51) || resend.method != "sendMessage" {
		t.Fatalf("unexpected delivery sequence: %+v %+v", edit, resend)
	}
	if _, exists := resend.body["message_id"]; exists {
		t.Fatal("resend retained the deleted message ID")
	}
	reply, ok := resend.body["reply_parameters"].(map[string]any)
	if !ok || reply["message_id"] != float64(r.MessageID) || reply["allow_sending_without_reply"] != true {
		t.Fatalf("resend lost the original video context: %+v", resend.body)
	}
	if resend.body["parse_mode"] != "HTML" || resend.body["text"] != edit.body["text"] {
		t.Fatalf("resend lost formatting: %+v", resend.body)
	}
	if plain := assertValidMessage(t, resend.body["text"].(string)); !strings.Contains(plain, "读取 <video> 失败") {
		t.Fatalf("error text changed: %s", plain)
	}
	pending, err := cat.PendingTelegramReceipts(ctx, s.BotID())
	if err != nil || len(pending) != 0 {
		t.Fatalf("successful resend remains pending: %v %v", pending, err)
	}
	job, err := cat.GetRemoteUploadJob(ctx, r.JobID)
	if err != nil || job.State != catalog.RemoteUploadFailed {
		t.Fatalf("resend changed the import outcome: %+v %v", job, err)
	}
}

func TestImportProgressReflectsMeasuredStage(t *testing.T) {
	for _, tc := range []struct {
		name      string
		job       catalog.RemoteUploadJob
		want      string
		noPercent bool
	}{
		{"fetch", catalog.RemoteUploadJob{State: "downloading", Stage: "telegram_download", BytesDownloaded: 50, TotalBytes: 100}, "正在从 TG 获取", true},
		{"unknown size", catalog.RemoteUploadJob{State: "downloading", Stage: "telegram_download", BytesDownloaded: 1024}, "正在从 TG 获取", true},
		{"download complete", catalog.RemoteUploadJob{State: "downloading", Stage: "telegram_download", BytesDownloaded: 100, TotalBytes: 100}, "正在从 TG 获取", true},
		{"validating", catalog.RemoteUploadJob{State: "validating"}, "正在校验", true},
		{"saving", catalog.RemoteUploadJob{State: "saving"}, "正在入库", true},
		{"retry", catalog.RemoteUploadJob{State: "queued", Stage: "retry_wait"}, "等待重试", true},
		{"cancel", catalog.RemoteUploadJob{State: "downloading", CancelRequested: true}, "正在取消", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := importProgressText(&tc.job)
			if !strings.Contains(got, tc.want) || (tc.noPercent && strings.Contains(got, "%")) {
				t.Fatalf("unexpected progress %q", got)
			}
		})
	}
}
