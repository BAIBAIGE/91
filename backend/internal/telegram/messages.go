package telegram

import (
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/video-site/backend/internal/catalog"
)

// Receipts store outcomes, not Telegram markup. Rendering stays here so changes
// to wording and layout do not affect reception, persistence, or delivery.
const (
	responseHelp        = "help"
	responseUserID      = "user_id"
	responseNeedsAccess = "needs_access"
	responseUnsupported = "unsupported_media"
)

type botMessage struct {
	text     string
	videoURL string
}

func renderReceiptMessage(r catalog.TelegramReceipt) botMessage {
	switch r.Response {
	case responseHelp:
		return botMessage{text: "🎬 <b>保存视频到媒体库</b>\n\n" +
			"直接发送或转发视频，也可以发送视频文件。\n" +
			"说明的第一行会作为标题；一组视频可一起发送。\n\n" +
			"进度会在同一条回复中更新。\n" +
			"/help 使用说明 · /id 我的 ID"}
	case responseUserID:
		return botMessage{text: "👤 <b>你的 Telegram ID</b>\n\n<code>" + strconv.FormatInt(r.SenderID, 10) +
			"</code>\n\n可填入网站 Telegram 配置的「允许的用户 ID」。"}
	case responseNeedsAccess:
		return botMessage{text: "🔒 <b>需要开通权限</b>\n\n" +
			"请联系管理员，将你的 ID 加入允许名单。\n" +
			"你的 ID：<code>" + strconv.FormatInt(r.SenderID, 10) + "</code>"}
	case responseUnsupported:
		return botMessage{text: "ℹ️ <b>请发送视频</b>\n\n" +
			"请转发视频或发送视频文件。\n" +
			"消息链接、单独的图片和动画暂不支持。\n\n/help 查看使用说明"}
	case catalog.TelegramResponseQueueFull:
		return botMessage{text: "⏳ <b>队列已满</b>\n\n请稍后重新发送视频。"}
	default:
		// Plain rejection reasons and receipts from older versions are untrusted
		// text. They must never be interpreted as Telegram HTML.
		return botMessage{text: "ℹ️ <b>提示</b>\n\n" + messageText(r.Response, 600)}
	}
}

func renderImportMessage(j *catalog.RemoteUploadJob, siteBaseURL string) botMessage {
	title := strings.TrimSpace(j.ResolvedTitle)
	if title == "" {
		title = strings.TrimSpace(j.RequestedTitle)
	}
	if title == "" {
		title = "未命名视频"
	}
	title = messageText(title, 120)
	var out botMessage
	switch j.State {
	case catalog.RemoteUploadCompleted:
		out.text = "✅ <b>已保存</b>\n" + title + "\n\n封面和预览将在后台生成。"
		if j.CompletedVideoID != "" && siteBaseURL != "" {
			out.videoURL = strings.TrimRight(siteBaseURL, "/") + "/video/" + url.PathEscape(j.CompletedVideoID)
		}
	case catalog.RemoteUploadFailed:
		reason := messageText(j.ErrorMessage, 300)
		if reason == "" {
			reason = "暂时无法完成保存，请在网站后台查看详情。"
		}
		out.text = "❌ <b>保存失败</b>\n" + title + "\n\n" + reason + "\n可在网站后台的 Telegram 页面重试。"
		if j.ID != "" {
			out.text += "\n\n任务：<code>" + messageText(j.ID, 100) + "</code>"
		}
	case catalog.RemoteUploadCanceled:
		out.text = "⏹ <b>已取消保存</b>\n" + title
	default:
		out.text = "⏳ <b>" + importProgressText(j) + "</b>\n" + title
	}
	return out
}

func importProgressText(j *catalog.RemoteUploadJob) string {
	if j.CancelRequested {
		return "正在取消保存"
	}
	switch j.State {
	case catalog.RemoteUploadDownloading:
		// Bot API file acquisition does not expose live download progress.
		return "正在从 TG 获取视频"
	case catalog.RemoteUploadValidating:
		return "正在校验视频"
	case catalog.RemoteUploadSaving:
		return "正在入库"
	case catalog.RemoteUploadQueued:
		if j.Stage == "retry_wait" {
			return "等待重试保存"
		}
	}
	return "已加入队列"
}

// Bound dynamic fields before escaping, keeping messages short and HTML intact.
// Collapse embedded newlines so captions and errors cannot disrupt the layout.
func messageText(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > limit {
		runes = append(runes[:limit-1], '…')
	}
	return html.EscapeString(string(runes))
}
