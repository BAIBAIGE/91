package drives

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Drive 是多家网盘统一抽象。上层不区分盘，只区分 Kind。
type Drive interface {
	// Kind 返回驱动代号："quark" / "p115" / "p123" / "pikpak" / "wopan" / "guangyapan" / "onedrive" / "googledrive" / "webdav" / "localstorage"
	Kind() string

	// ID 返回该盘在 catalog 中的唯一标识
	ID() string

	// Init 完成登录态校验；登录态由 Authenticator 另行获取后注入
	Init(ctx context.Context) error

	// List 列指定目录下的直接子项
	List(ctx context.Context, dirID string) ([]Entry, error)

	// Stat 拿到单个文件的元数据
	Stat(ctx context.Context, fileID string) (*Entry, error)

	// StreamURL 返回一次性直链 + 必须的请求头
	// 代理层据此回源，透传 Range
	StreamURL(ctx context.Context, fileID string) (*StreamLink, error)

	// Upload 把本地流写入指定目录，返回新文件 fileID。
	// 当前预览视频和封面只保存在本地，不再通过该方法写回网盘。
	Upload(ctx context.Context, parentID, name string, r io.Reader, size int64) (string, error)

	// EnsureDir 保证指定路径存在（相对根目录），返回最终目录 fileID。
	EnsureDir(ctx context.Context, pathFromRoot string) (string, error)

	// RootID 返回根目录 fileID
	RootID() string
}

// GenerationStreamProvider is an optional drive capability for a provider-
// generated playback stream that is cheaper to seek than the original file.
// Background thumbnail/preview workers prefer this stream, while ordinary
// playback and fingerprinting continue to use StreamURL.
//
// forceRefresh invalidates any short-lived provider cache after a signed
// playlist rejection. Implementations must never expose account credentials in
// the returned StreamLink.
type GenerationStreamProvider interface {
	GenerationStreamURL(ctx context.Context, fileID string, forceRefresh bool) (*StreamLink, error)
}

// Remover is an optional drive capability. It mirrors OpenList's optional
// Remove interface: callers must type-assert before deleting a source file.
type Remover interface {
	Remove(ctx context.Context, fileID string) error
}

// SourceFile carries the catalog metadata available when an administrator
// requests deletion of the original source file.
type SourceFile struct {
	FileID   string
	ParentID string
	Name     string
	Size     int64
}

// SourceRemover is an optional, richer removal capability for providers whose
// playback ID is not the same ID required by their delete API.
type SourceRemover interface {
	RemoveSource(ctx context.Context, source SourceFile) error
}

type Entry struct {
	ID       string
	Name     string
	Size     int64
	Hash     string
	IsDir    bool
	ParentID string
	MimeType string
	ModTime  time.Time

	// 部分网盘额外信息
	Category     int    // 1=视频 (quark)
	ThumbnailURL string // 网盘侧已提供的快速缩略图
}

type StreamLink struct {
	URL     string
	Headers http.Header
	// Expires is the deadline through which callers may reuse this link. It can
	// be a conservative local deadline when a provider does not expose the
	// signed URL's exact expiry.
	Expires time.Time

	// PassThroughRedirects tells the online playback proxy to make the first
	// authenticated request itself, but relay an upstream 3xx Location to the
	// browser instead of following it on the server. Background consumers such
	// as fingerprinting and transcoding still follow redirects to read bytes.
	PassThroughRedirects bool
}

// ErrNotSupported 代表某家盘不支持某操作
var ErrNotSupported = errors.New("operation not supported by this drive")

// ErrGenerationStreamUnavailable means the optional optimized generation
// stream does not exist for this file. Callers may safely fall back to the
// original StreamURL without treating the drive as unhealthy.
var ErrGenerationStreamUnavailable = errors.New("generation stream unavailable")

// RateLimitError 表示上游服务正在限流。RetryAfter 为 0 时由调用方选择默认冷却时间。
type RateLimitError struct {
	Provider   string
	RetryAfter time.Duration
	Err        error
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return "rate limited"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Provider != "" {
		return e.Provider + " rate limited"
	}
	return "rate limited"
}

func (e *RateLimitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func RateLimitRetryAfter(err error) (time.Duration, bool) {
	var rateLimit *RateLimitError
	if errors.As(err, &rateLimit) {
		return rateLimit.RetryAfter, true
	}
	return 0, false
}

// TextMentionsHTTPStatus only looks for explicit numeric HTTP status contexts
// in errors from tools that do not expose structured response metadata.
func TextMentionsHTTPStatus(text string, statuses ...int) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, status := range statuses {
		if status <= 0 {
			continue
		}
		code := strconv.Itoa(status)
		if strings.HasPrefix(text, code+" ") ||
			strings.Contains(text, "status="+code) ||
			strings.Contains(text, "status: "+code) ||
			strings.Contains(text, "status "+code) ||
			strings.Contains(text, "status code "+code) ||
			strings.Contains(text, "http "+code) ||
			strings.Contains(text, "http status="+code) ||
			strings.Contains(text, "http status: "+code) ||
			strings.Contains(text, "http status "+code) ||
			strings.Contains(text, "server returned "+code) ||
			strings.Contains(text, "code="+code) ||
			strings.Contains(text, "code: "+code) ||
			strings.Contains(text, "error_code="+code) ||
			strings.Contains(text, "error_code: "+code) {
			return true
		}
	}
	return false
}

func ErrorMentionsHTTPStatus(err error, statuses ...int) bool {
	if err == nil {
		return false
	}
	return TextMentionsHTTPStatus(err.Error(), statuses...)
}
