package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
)

func (s *Service) notify(ctx context.Context) {
	for ctx.Err() == nil {
		s.mu.RLock()
		c, botID := s.client, s.botID
		s.mu.RUnlock()
		// Restore and setup errors must not trigger notifications to old chats.
		if c != nil && s.Available() {
			receipts, err := s.cat.PendingTelegramReceipts(ctx, botID)
			if err == nil {
				for _, r := range receipts {
					if ctx.Err() != nil {
						return
					}
					s.notifyOne(ctx, c, r)
					if !wait(ctx, time.Second) {
						return
					}
				}
			}
		}
		if !wait(ctx, 2*time.Second) {
			return
		}
	}
}
func (s *Service) notifyOne(ctx context.Context, c *client, r catalog.TelegramReceipt) {
	body, state := r.Response, "replied"
	href := ""
	if r.JobID != "" {
		j, err := s.cat.GetRemoteUploadJob(ctx, r.JobID)
		if err != nil {
			return
		}
		title := j.ResolvedTitle
		if title == "" {
			title = j.RequestedTitle
		}
		state = j.State
		switch j.State {
		case catalog.RemoteUploadCompleted:
			body = "已保存 · " + title + "\n封面和预览将在后台生成。"
			if j.CompletedVideoID != "" && s.cfg.SiteBaseURL != "" {
				href = strings.TrimRight(s.cfg.SiteBaseURL, "/") + "/video/" + url.PathEscape(j.CompletedVideoID)
			}
		case catalog.RemoteUploadFailed:
			body = "保存失败：" + j.ErrorMessage + "\n可在后台重试。"
		case catalog.RemoteUploadCanceled:
			body = "保存任务已取消。"
		default:
			body = importProgressText(j) + " · " + title
		}
		body += "\n任务 " + j.ID
		if !j.Terminal() {
			// Persist the rendered content identity so restarts and unchanged
			// progress do not cause duplicate edits.
			state = fmt.Sprintf("progress:%x", sha256.Sum256([]byte(body)))
		}
	}
	if r.Delivered == state {
		// Move unchanged tasks behind other eligible receipts as well.
		_ = s.cat.SaveTelegramNotification(ctx, r, r.ReplyID, state, 0)
		return
	}
	request := map[string]any{"chat_id": r.ChatID, "text": body, "link_preview_options": map[string]bool{"is_disabled": true}}
	if href != "" {
		request["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "打开视频", "url": href}}}}
	}
	method := "sendMessage"
	if r.ReplyID != 0 {
		method = "editMessageText"
		request["message_id"] = r.ReplyID
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var response struct {
		ID int64 `json:"message_id"`
	}
	err := c.call(sendCtx, method, request, &response)
	// An edit may fail because the original response was deleted. Retry with a
	// new message, without ever changing the import result.
	var apiErr *APIError
	if err != nil && r.ReplyID != 0 && errors.As(err, &apiErr) && apiErr.Code == 400 {
		delete(request, "message_id")
		err = c.call(sendCtx, "sendMessage", request, &response)
	}
	if err != nil {
		delay := time.Duration(1<<min(r.Attempts, 8)) * 10 * time.Second
		if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
			delay = apiErr.RetryAfter
		}
		_ = s.cat.SaveTelegramNotification(ctx, r, 0, "", delay)
		return
	}
	if response.ID == 0 {
		response.ID = r.ReplyID
	}
	_ = s.cat.SaveTelegramNotification(ctx, r, response.ID, state, 0)
}

func importProgressText(j *catalog.RemoteUploadJob) string {
	if j.CancelRequested {
		return "正在取消保存"
	}
	switch j.State {
	case catalog.RemoteUploadDownloading:
		return "正在从 TG 获取视频，请稍候…"
	case catalog.RemoteUploadValidating:
		return "正在校验视频…"
	case catalog.RemoteUploadSaving:
		return "正在入库…"
	case catalog.RemoteUploadQueued:
		if j.Stage == "retry_wait" {
			return "等待重试保存…"
		}
	}
	return "已加入队列"
}
