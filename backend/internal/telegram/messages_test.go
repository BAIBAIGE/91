package telegram

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/video-site/backend/internal/catalog"
)

// Decode rendered HTML to catch broken tags/entities and enforce Telegram's
// message limit even for long input containing non-BMP characters.
func assertValidMessage(t *testing.T, text string) string {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Fatal("message contains invalid UTF-8")
	}
	decoder := xml.NewDecoder(strings.NewReader("<message>" + text + "</message>"))
	var plain strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid message HTML: %v", err)
		}
		switch value := token.(type) {
		case xml.CharData:
			plain.Write(value)
		case xml.StartElement:
			if value.Name.Local != "message" && value.Name.Local != "b" && value.Name.Local != "code" {
				t.Fatalf("unexpected HTML tag: %s", value.Name.Local)
			}
		}
	}
	if size := len(utf16.Encode([]rune(plain.String()))); size == 0 || size > 4096 {
		t.Fatalf("message length out of bounds: %d", size)
	}
	return plain.String()
}

func TestImportMessagesKeepStatusTitleAndActionsDistinct(t *testing.T) {
	for _, tc := range []struct {
		state, heading string
		wantTask       bool
		wantLink       bool
	}{
		{catalog.RemoteUploadQueued, "已加入队列", false, false},
		{catalog.RemoteUploadDownloading, "正在从 TG 获取视频", false, false},
		{catalog.RemoteUploadValidating, "正在校验视频", false, false},
		{catalog.RemoteUploadSaving, "正在入库", false, false},
		{catalog.RemoteUploadCompleted, "已保存", false, true},
		{catalog.RemoteUploadFailed, "保存失败", true, false},
		{catalog.RemoteUploadCanceled, "已取消保存", false, false},
	} {
		t.Run(tc.state, func(t *testing.T) {
			job := &catalog.RemoteUploadJob{
				ID: "tg-task", State: tc.state, RequestedTitle: "原始标题",
				ResolvedTitle: "海边 & <日落>\n第二行", ErrorMessage: "读取 <video> 失败 & 请重试",
				CompletedVideoID: "video #1", TotalBytes: 100, BytesDownloaded: 50,
			}
			message := renderImportMessage(job, "https://example.com/library/")
			plain := assertValidMessage(t, message.text)
			if !strings.Contains(message.text, "<b>"+tc.heading+"</b>\n海边 &amp; &lt;日落&gt; 第二行") {
				t.Fatalf("status and title are not separated or escaped: %s", message.text)
			}
			if strings.Contains(plain, "原始标题") || strings.Contains(plain, "%") {
				t.Fatalf("wrong title or unmeasured progress: %s", plain)
			}
			if strings.Contains(message.text, "<code>tg-task</code>") != tc.wantTask {
				t.Fatalf("task ID visibility: %s", message.text)
			}
			if (message.videoURL != "") != tc.wantLink {
				t.Fatalf("unexpected video action: %q", message.videoURL)
			}
			if tc.wantLink && message.videoURL != "https://example.com/library/video/video%20%231" {
				t.Fatalf("invalid video URL: %s", message.videoURL)
			}
			if tc.state == catalog.RemoteUploadFailed && !strings.Contains(plain, job.ErrorMessage) {
				t.Fatalf("failure reason lost: %s", plain)
			}
		})
	}
}

func TestMessagesBoundAndEscapeDynamicContent(t *testing.T) {
	input := strings.Repeat("🎬<&>\n", 2000)
	job := &catalog.RemoteUploadJob{State: catalog.RemoteUploadFailed, ID: input, ResolvedTitle: input, ErrorMessage: input}
	message := renderImportMessage(job, "")
	plain := assertValidMessage(t, message.text)
	if strings.Count(plain, "…") != 3 || strings.Count(plain, "\n") > 8 {
		t.Fatalf("dynamic fields broke the compact layout: %s", plain)
	}
	plain = assertValidMessage(t, renderReceiptMessage(catalog.TelegramReceipt{Response: input}).text)
	if !strings.HasSuffix(plain, "…") {
		t.Fatal("long receipt text was not truncated")
	}
	message = renderReceiptMessage(catalog.TelegramReceipt{Response: `<b>不是标题</b> & "原文"`})
	if plain := assertValidMessage(t, message.text); !strings.Contains(plain, `<b>不是标题</b> & "原文"`) {
		t.Fatalf("stored plain text was interpreted as HTML: %s", plain)
	}
}

func TestImportMessagesHandleMissingDetails(t *testing.T) {
	job := &catalog.RemoteUploadJob{State: catalog.RemoteUploadCompleted, RequestedTitle: "  请求标题  ", CompletedVideoID: "video"}
	message := renderImportMessage(job, "")
	if !strings.Contains(message.text, "\n请求标题\n") || message.videoURL != "" {
		t.Fatalf("title fallback or absent site URL: %+v", message)
	}
	job.CompletedVideoID = ""
	if message := renderImportMessage(job, "https://example.com"); message.videoURL != "" {
		t.Fatal("created a link without a video ID")
	}
	job.RequestedTitle, job.State = "", catalog.RemoteUploadFailed
	message = renderImportMessage(job, "")
	if !strings.Contains(message.text, "未命名视频") || !strings.Contains(message.text, "查看详情") || strings.Contains(message.text, "<code>") {
		t.Fatalf("missing details produce an incomplete reply: %s", message.text)
	}
}

func TestCommandsAndMediaHintsRespectAccess(t *testing.T) {
	s, _ := testService(t)
	for _, tc := range []struct {
		name, command, chatType string
		userID                  int64
		want                    string
	}{
		{"start", "/start", "private", 42, "保存视频到91"},
		{"help", "/help", "private", 42, "视频附带的第一行文字"},
		{"addressed help", "/help@test_bot", "private", 42, "保存视频到91"},
		{"start argument", "/start welcome", "private", 42, "保存视频到91"},
		{"id", "/id", "private", 42, "<code>42</code>"},
		{"bootstrap id", "/id@test_bot", "private", 99, "<code>99</code>"},
		{"unauthorized start", "/start", "private", 99, "需要开通权限"},
		{"unauthorized help", "/help", "private", 99, "需要开通权限"},
		{"unauthorized status", "/status", "private", 99, "需要开通权限"},
		{"unsupported", "https://t.me/example/1", "private", 42, "请发送视频"},
		{"unknown command", "/unknown", "private", 42, "请发送视频"},
		{"unauthorized media", "", "private", 99, ""},
		{"group id", "/id", "group", 42, ""},
		{"group help", "/help", "group", 42, ""},
		{"group status", "/status", "group", 42, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := videoUpdate(1, tc.userID)
			u.Message.Video = nil
			u.Message.Text, u.Message.Chat.Type = tc.command, tc.chatType
			input, err := s.prepareImport(context.Background(), u)
			if err != nil {
				t.Fatal(err)
			}
			if input.Source != nil {
				t.Fatal("command or unsupported message created an import")
			}
			if tc.want == "" {
				if input.Receipt.Response != "__ignore__" {
					t.Fatalf("unexpected response: %+v", input.Receipt)
				}
				return
			}
			message := renderReceiptMessage(input.Receipt)
			assertValidMessage(t, message.text)
			if !strings.Contains(message.text, tc.want) {
				t.Fatalf("response missing %q: %s", tc.want, message.text)
			}
		})
	}
}
