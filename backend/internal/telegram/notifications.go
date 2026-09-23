package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/video-site/backend/internal/applog"
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
	message, state := renderReceiptMessage(r), "replied"
	if r.JobID != "" {
		j, err := s.cat.GetRemoteUploadJob(ctx, r.JobID)
		if err != nil {
			return
		}
		message = renderImportMessage(j, s.cfg.SiteBaseURL)
		state = j.State
		if !j.Terminal() {
			// Persist the rendered content identity so restarts and unchanged
			// progress do not cause duplicate edits.
			state = fmt.Sprintf("progress:%x", sha256.Sum256([]byte(message.text)))
		}
	} else if r.Response == responseStatus {
		message = renderReceiptMessage(catalog.TelegramReceipt{Response: responseNeedsAccess, SenderID: r.SenderID})
		if s.Allowed(r.SenderID) {
			var err error
			message, err = s.statusMessage(ctx, time.Now())
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				applog.Warn(ctx, "读取 TG 转存统计失败", err, applog.Fields{Component: "telegram", Stage: "status"})
				message = botMessage{text: "⚠️ 暂时无法读取转存统计，请稍后重试 /status。"}
			}
		}
	}
	if r.Delivered == state {
		// Move unchanged tasks behind other eligible receipts as well.
		_ = s.cat.SaveTelegramNotification(ctx, r, r.ReplyID, state, 0)
		return
	}
	request := map[string]any{"chat_id": r.ChatID, "text": message.text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}}
	if message.videoURL != "" {
		request["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "打开视频", "url": message.videoURL}}}}
	}
	replyParameters := map[string]any{"message_id": r.MessageID, "allow_sending_without_reply": true}
	method := "sendMessage"
	if r.ReplyID != 0 {
		method = "editMessageText"
		request["message_id"] = r.ReplyID
	} else if r.MessageID > 0 {
		request["reply_parameters"] = replyParameters
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
		if r.MessageID > 0 {
			request["reply_parameters"] = replyParameters
		}
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
