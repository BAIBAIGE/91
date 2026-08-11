package p115

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/video-site/backend/internal/drives"
	"github.com/video-site/backend/internal/scopedproxy"
	"github.com/video-site/backend/internal/streamhttp"
)

const (
	p115HLSMasterBaseURL          = "https://115.com/api/video/m3u8/"
	p115HLSReferer                = "https://115.com/"
	p115HLSUserAgent              = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36"
	p115HLSCacheTTL               = 10 * time.Minute
	p115HLSMaxPlaylist            = 1 << 20
	p115PickCodeCacheMaxEntries   = 4096
	p115GenerationCacheMaxEntries = 1024
	p115ListRequestTimeout        = 30 * time.Second
)

type cachedGenerationStream struct {
	link    *drives.StreamLink
	expires time.Time
}

type generationStreamCall struct {
	done    chan struct{}
	refresh bool
	link    *drives.StreamLink
	err     error
}

type Driver struct {
	id            string
	cookie        string
	rootID        string
	client        *sdk.Pan115Client
	ua            string
	uploadTempDir string
	uploadGate    chan struct{}
	// uploadAppVersion is resolved lazily from 115's public version endpoint.
	// Uploads are serialized by uploadGate, so these fields need no second lock.
	uploadAppVersion         string
	uploadAppVersionResolved bool

	listGate     chan struct{}
	lastListAt   time.Time
	listInterval time.Duration
	listTimeout  time.Duration

	generationMu       sync.Mutex
	pickCodes          map[string]string
	generationCache    map[string]cachedGenerationStream
	generationInflight map[string]*generationStreamCall
	hlsClient          *http.Client
	hlsMasterBaseURL   string
}

type Config struct {
	ID            string
	Cookie        string // 形如 "UID=xxx; CID=xxx; SEID=xxx; KID=xxx"
	RootID        string // 默认 "0"
	UA            string // 默认 UA115Browser
	UploadTempDir string
}

func New(c Config) *Driver {
	rootID := c.RootID
	if rootID == "" {
		rootID = "0"
	}
	ua := c.UA
	if ua == "" {
		ua = sdk.UA115Browser
	}
	return &Driver{
		id:                 c.ID,
		cookie:             c.Cookie,
		rootID:             rootID,
		ua:                 ua,
		uploadTempDir:      strings.TrimSpace(c.UploadTempDir),
		uploadGate:         make(chan struct{}, 1),
		uploadAppVersion:   p115UploadFallbackAppVersion,
		listGate:           make(chan struct{}, 1),
		listInterval:       2 * time.Second,
		listTimeout:        p115ListRequestTimeout,
		pickCodes:          make(map[string]string),
		generationCache:    make(map[string]cachedGenerationStream),
		generationInflight: make(map[string]*generationStreamCall),
		hlsClient:          streamhttp.NewClient(20 * time.Second),
		hlsMasterBaseURL:   p115HLSMasterBaseURL,
	}
}

func (d *Driver) Kind() string   { return "p115" }
func (d *Driver) ID() string     { return d.id }
func (d *Driver) RootID() string { return d.rootID }

func (d *Driver) Init(ctx context.Context) error {
	cr := &sdk.Credential{}
	if err := cr.FromCookie(d.cookie); err != nil {
		return fmt.Errorf("parse cookie: %w", err)
	}
	d.client = sdk.New(
		sdk.WithClient(scopedproxy.NewHTTPClient(0)),
		sdk.UA(d.ua),
	).ImportCredential(cr)
	return d.client.LoginCheck()
}

func (d *Driver) List(ctx context.Context, dirID string) ([]drives.Entry, error) {
	files, err := d.listWithRetry(ctx, dirID)
	if err != nil {
		return nil, fmt.Errorf("115 list: %w", err)
	}
	if files == nil {
		return nil, nil
	}
	out := make([]drives.Entry, 0, len(*files))
	for _, f := range *files {
		d.rememberPickCode(f.FileID, f.PickCode)
		out = append(out, fileToEntry(&f, dirID))
	}
	return out, nil
}

// p115ListCooldown 是列目录触发疑似风控错误时的冷却时长。
//
// 历史上是 [30min × 3]，3 次都失败就放弃；新策略改为 10 分钟无限重试 ——
// 只要错误仍属明确 HTTP transient 状态（429 / 405），
// 就持续等 10 分钟再发一次列目录请求，直到成功或 ctx 取消。这样即使 115
// 风控持续较长时间，扫描会自然延后到风控结束，不再丢半棵子树。
const p115ListCooldown = 10 * time.Minute

func (d *Driver) listWithRetry(ctx context.Context, dirID string) (*[]sdk.File, error) {
	release, err := d.acquireListGate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	for attempt := 0; ; attempt++ {
		if err := d.waitForListSlot(ctx); err != nil {
			return nil, err
		}

		files, err := d.listWithLimitContext(ctx, dirID, sdk.MaxDirPageLimit)
		if err == nil {
			return files, nil
		}
		// 非 transient 错误（如 cookie 失效）直接返回；继续重试也只会反复失败。
		if !isTransient115ListError(err) {
			return nil, err
		}
		log.Printf("[p115] list cooling down drive=%s dir=%s cooldown=%s attempt=%d err=%v",
			d.id, dirID, p115ListCooldown, attempt+1, err)
		if err := sleepContext(ctx, p115ListCooldown); err != nil {
			return nil, err
		}
	}
}

// listWithLimitContext mirrors the SDK's ListWithLimit pagination while binding
// every HTTP request to ctx. The upstream ListWithLimit API does not accept a
// context, so calling it directly leaves an in-flight 115 request alive after an
// administrator stops a scan.
func (d *Driver) listWithLimitContext(ctx context.Context, dirID string, limit int64) (*[]sdk.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > sdk.MaxDirPageLimit {
		limit = sdk.MaxDirPageLimit
	}

	listOptions := sdk.DefaultListOptions()
	if len(listOptions.ApiURLs) == 0 {
		return nil, errors.New("115 list API URL is not configured")
	}

	files := make([]sdk.File, 0)
	offset := int64(0)
	for page := 0; ; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		result, err := d.getFilesContext(
			ctx,
			dirID,
			sdk.WithApiURL(listOptions.ApiURLs[page%len(listOptions.ApiURLs)]),
			sdk.WithLimit(limit),
			sdk.WithOffset(offset),
		)
		if err != nil {
			return nil, err
		}

		for i := range result.Files {
			file := (&sdk.File{}).From(&result.Files[i])
			files = append(files, *file)
		}
		offset = int64(result.Offset) + limit
		if offset >= int64(result.Count) {
			return &files, nil
		}
	}
}

func (d *Driver) getFilesContext(ctx context.Context, dirID string, options ...sdk.GetFileOptions) (*sdk.FileListResp, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.client == nil || d.client.Client == nil {
		return nil, errors.New("115 client not initialized")
	}

	requestCtx := ctx
	cancel := func() {}
	if d.listTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, d.listTimeout)
	}
	defer cancel()

	request := d.client.Client.R().
		SetContext(requestCtx).
		ForceContentType("application/json;charset=UTF-8")
	result, err := sdk.GetFiles(request, dirID, options...)
	if err != nil {
		// Prefer the parent cancellation reason. Some SDK error paths flatten the
		// request error, which would otherwise hide context.Canceled from callers.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("115 list request timed out after %s: %v", d.listTimeout, err)
		}
		return nil, err
	}
	return result, nil
}

func (d *Driver) acquireListGate(ctx context.Context) (func(), error) {
	select {
	case d.listGate <- struct{}{}:
		return func() { <-d.listGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *Driver) waitForListSlot(ctx context.Context) error {
	if d.listInterval <= 0 || d.lastListAt.IsZero() {
		d.lastListAt = time.Now()
		return ctx.Err()
	}

	next := d.lastListAt.Add(d.listInterval)
	now := time.Now()
	if now.Before(next) {
		if err := sleepContext(ctx, next.Sub(now)); err != nil {
			return err
		}
	}
	d.lastListAt = time.Now()
	return ctx.Err()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isTransient115ListError(err error) bool {
	return isTransient115UpstreamError(err)
}

func isTransient115UpstreamError(err error) bool {
	if err == nil {
		return false
	}
	return drives.ErrorMentionsHTTPStatus(err, http.StatusMethodNotAllowed, http.StatusTooManyRequests) ||
		p115HTMLTitleMentionsStatus(err.Error(), http.StatusMethodNotAllowed, http.StatusTooManyRequests)
}

// p115HTMLTitleMentionsStatus handles the Aliyun WAF response returned by 115.
// The SDK exposes that response as an error containing the raw HTML document,
// for example "<title>405</title>", rather than a structured HTTP status.
// Keep this provider-specific and require the title to contain only the numeric
// code so unrelated prose containing "405" is not mistaken for throttling.
func p115HTMLTitleMentionsStatus(text string, statuses ...int) bool {
	text = strings.ToLower(text)
	for searchFrom := 0; searchFrom < len(text); {
		start := strings.Index(text[searchFrom:], "<title")
		if start < 0 {
			return false
		}
		start += searchFrom
		openEnd := strings.IndexByte(text[start:], '>')
		if openEnd < 0 {
			return false
		}
		openEnd += start
		closeStart := strings.Index(text[openEnd+1:], "</title>")
		if closeStart < 0 {
			return false
		}
		closeStart += openEnd + 1
		title := strings.TrimSpace(text[openEnd+1 : closeStart])
		for _, status := range statuses {
			if status > 0 && title == fmt.Sprintf("%d", status) {
				return true
			}
		}
		searchFrom = closeStart + len("</title>")
	}
	return false
}

func isTransient115StreamError(err error) bool {
	if isTransient115UpstreamError(err) {
		return true
	}
	if drives.ErrorMentionsHTTPStatus(err,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	) {
		return true
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return true
	}
	// Some SDK layers flatten *url.Error into plain text. Match only concrete
	// transport failures here; authentication and ordinary API errors remain
	// permanent failures.
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "tls handshake timeout") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "connection timed out") ||
		strings.Contains(text, "connection reset by peer")
}

// ListDirsOnly 只列指定目录的直接**子目录**，不返回文件条目。专为 admin 后台
// 的"设置跳过目录"树形浏览器优化 —— 那里只显示目录节点，文件无意义。
//
// 性能差异：默认 List 按 SDK 的 ListWithLimit 语义分页拉到 offset>=count，会把
// 全部文件 + 目录一起拉下来。某个 115 根目录可能累积了几万个视频，叠加 driver
// 自己的 2 秒间隔限频，单次根列出会卡几十秒。这里用 `WithOrder(FileOrderByType)`
// 让目录排在最前，只读第一页（最多 1150 条），命中第一个非目录条目就停止 ——
// 几乎所有网盘的"单层目录数"都远小于 1150，1 次 API 调用就能拿全。
//
// 仍然与 listWithRetry 共享串行门闩、2s 间隔和 10 分钟冷却语义，避免和扫描
// 走的列目录请求并发触发风控。
func (d *Driver) ListDirsOnly(ctx context.Context, dirID string) ([]drives.Entry, error) {
	release, err := d.acquireListGate(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	for attempt := 0; ; attempt++ {
		if err := d.waitForListSlot(ctx); err != nil {
			return nil, err
		}

		// 单页拉 MaxDirPageLimit=1150 条，按"file_type asc"排序让目录排前 ——
		// 这样即使某个目录条目数量超过 1150，目录通常仍能落在第一页。但我们不
		// 依赖这个顺序保证：扫完整页所有 entry，只挑目录（FileID=="" 即为目录），
		// 文件直接忽略。1150 个 entry 解析是微秒级开销，无需提前 break。
		resp, err := d.getFilesContext(ctx, dirID,
			sdk.WithOrder(sdk.FileOrderByType),
			sdk.WithAsc(true),
			sdk.WithLimit(sdk.MaxDirPageLimit),
			sdk.WithOffset(0),
		)
		if err == nil && resp != nil {
			out := make([]drives.Entry, 0, 32)
			for _, fi := range resp.Files {
				if fi.FileID != "" {
					continue // 文件，跳过
				}
				f := (&sdk.File{}).From(&fi)
				out = append(out, fileToEntry(f, dirID))
			}
			// 极端兜底：如果该目录的总条目数 > 1150 且目录条目不在首页，记一行
			// warning 让运维知道可能漏目录；正常网盘单层目录数远小于 1150。
			if int64(resp.Count) > sdk.MaxDirPageLimit {
				log.Printf("[p115] list-dirs warning drive=%s dir=%s total=%d page=%d dirs_in_page=%d (sub-dirs beyond first page may be missed)",
					d.id, dirID, resp.Count, sdk.MaxDirPageLimit, len(out))
			}
			return out, nil
		}
		if !isTransient115ListError(err) {
			return nil, fmt.Errorf("115 list dirs: %w", err)
		}
		log.Printf("[p115] list-dirs cooling down drive=%s dir=%s cooldown=%s attempt=%d err=%v",
			d.id, dirID, p115ListCooldown, attempt+1, err)
		if err := sleepContext(ctx, p115ListCooldown); err != nil {
			return nil, err
		}
	}
}

func (d *Driver) Stat(ctx context.Context, fileID string) (*drives.Entry, error) {
	f, err := d.client.GetFile(fileID)
	if err != nil {
		return nil, fmt.Errorf("115 stat: %w", err)
	}
	if f == nil {
		return nil, errors.New("115 stat: not found")
	}
	d.rememberPickCode(f.FileID, f.PickCode)
	e := fileToEntry(f, f.ParentID)
	return &e, nil
}

func (d *Driver) StreamURL(ctx context.Context, fileID string) (*drives.StreamLink, error) {
	return d.streamURLWithUA(ctx, fileID, d.ua)
}

func (d *Driver) StreamURLWithHeader(ctx context.Context, fileID string, header http.Header) (*drives.StreamLink, error) {
	return d.streamURLWithUA(ctx, fileID, header.Get("User-Agent"))
}

func (d *Driver) streamURLWithUA(ctx context.Context, fileID string, ua string) (*drives.StreamLink, error) {
	// 需要先拿到 pickCode
	f, err := d.client.GetFile(fileID)
	if err != nil {
		return nil, wrap115StreamTransientError("115 get file", err)
	}
	d.rememberPickCode(fileID, f.PickCode)
	info, ua, err := d.downloadInfo(f.PickCode, ua)
	if err != nil {
		return nil, wrap115StreamTransientError("115 download url", err)
	}
	if info == nil || info.Url.Url == "" {
		return nil, errors.New("115 download url: empty")
	}

	headers := http.Header{}
	// 115 直链会返回一组 Cookie / Referer，info.Header 里带了
	for k, vs := range info.Header {
		for _, v := range vs {
			headers.Add(k, v)
		}
	}
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", ua)
	}

	return &drives.StreamLink{
		URL:                info.Url.Url,
		Headers:            headers,
		Expires:            time.Now().Add(25 * time.Minute), // 115 直链 30 分钟过期，留余量
		ClientRedirectSafe: true,
	}, nil
}

// GenerationStreamURL resolves 115's online-playback HLS master playlist to a
// signed media playlist. The account cookie is used only by this Go request;
// FFmpeg receives the signed child URL plus ordinary UA/Referer headers.
func (d *Driver) GenerationStreamURL(ctx context.Context, fileID string, forceRefresh bool) (*drives.StreamLink, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return nil, fmt.Errorf("115 hls: %w: empty file id", drives.ErrGenerationStreamUnavailable)
	}

	d.generationMu.Lock()
	now := time.Now()
	d.pruneGenerationCacheLocked(now)
	if forceRefresh {
		delete(d.generationCache, fileID)
	}
	if cached, ok := d.generationCache[fileID]; ok && now.Before(cached.expires) {
		link := cloneStreamLink(cached.link)
		d.generationMu.Unlock()
		return link, nil
	}
	if call, ok := d.generationInflight[fileID]; ok && (!forceRefresh || call.refresh) {
		d.generationMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			return cloneStreamLink(call.link), call.err
		}
	}
	// A forced refresh must not join an older non-refresh resolution: that
	// resolution may be the source of the signed URL that was just rejected.
	// Replacing the active call also makes subsequent ordinary callers join the
	// refresh instead of receiving the stale result.
	call := &generationStreamCall{done: make(chan struct{}), refresh: forceRefresh}
	d.generationInflight[fileID] = call
	d.generationMu.Unlock()

	link, err := d.resolveGenerationStream(ctx, fileID)

	d.generationMu.Lock()
	if active := d.generationInflight[fileID]; active == call {
		if err == nil && link != nil {
			now := time.Now()
			d.pruneGenerationCacheLocked(now)
			d.makeGenerationCacheRoomLocked()
			d.generationCache[fileID] = cachedGenerationStream{
				link:    cloneStreamLink(link),
				expires: now.Add(p115HLSCacheTTL),
			}
		}
		delete(d.generationInflight, fileID)
	}
	call.link = cloneStreamLink(link)
	call.err = err
	close(call.done)
	d.generationMu.Unlock()
	return link, err
}

func (d *Driver) resolveGenerationStream(ctx context.Context, fileID string) (*drives.StreamLink, error) {
	pickCode := d.rememberedPickCode(fileID)
	if pickCode == "" {
		if d.client == nil {
			return nil, fmt.Errorf("115 hls get file: %w", drives.ErrGenerationStreamUnavailable)
		}
		f, err := d.client.GetFile(fileID)
		if err != nil {
			return nil, wrap115StreamTransientError("115 hls get file", err)
		}
		if f != nil {
			pickCode = strings.TrimSpace(f.PickCode)
			d.rememberPickCode(fileID, pickCode)
		}
	}
	if pickCode == "" {
		return nil, fmt.Errorf("115 hls: %w: missing pick code", drives.ErrGenerationStreamUnavailable)
	}

	base := strings.TrimSpace(d.hlsMasterBaseURL)
	if base == "" {
		base = p115HLSMasterBaseURL
	}
	masterURL := strings.TrimRight(base, "/") + "/" + url.PathEscape(pickCode) + ".m3u8"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, masterURL, nil)
	if err != nil {
		return nil, fmt.Errorf("115 hls request: %w", err)
	}
	req.Header.Set("User-Agent", p115HLSUserAgent)
	req.Header.Set("Referer", p115HLSReferer)
	req.Header.Set("Cookie", d.cookie)
	client := d.hlsClient
	if client == nil {
		client = streamhttp.NewClient(20 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, wrap115StreamTransientError("115 hls master", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, p115HLSMaxPlaylist+1))
	if readErr != nil {
		return nil, wrap115StreamTransientError("115 hls master read", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("115 hls master status=%d", resp.StatusCode)
		if resp.StatusCode == http.StatusMethodNotAllowed ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode >= http.StatusInternalServerError {
			return nil, wrap115StreamTransientError("115 hls master", err)
		}
		// The generation stream is an optional optimization, so a rejection
		// here must not cool down the whole drive. Falling back to the ordinary
		// download URL keeps media generation working when only online playback
		// is refused, and lets a genuinely invalid cookie surface as the
		// permanent authentication failure it is.
		log.Printf("[p115] hls generation unavailable drive=%s file=%s status=%d; falling back to the download url",
			d.id, fileID, resp.StatusCode)
		return nil, fmt.Errorf("115 hls: %w: status=%d", drives.ErrGenerationStreamUnavailable, resp.StatusCode)
	}
	if len(body) > p115HLSMaxPlaylist {
		return nil, fmt.Errorf("115 hls: %w: playlist too large", drives.ErrGenerationStreamUnavailable)
	}
	variantURL, err := selectHLSVariant(masterURL, string(body))
	if err != nil {
		log.Printf("[p115] hls generation unavailable drive=%s file=%s master_host=%s: %v",
			d.id, fileID, req.URL.Hostname(), err)
		return nil, fmt.Errorf("115 hls: %w: %v", drives.ErrGenerationStreamUnavailable, err)
	}
	return &drives.StreamLink{
		URL: variantURL,
		Headers: http.Header{
			"User-Agent": {p115HLSUserAgent},
			"Referer":    {p115HLSReferer},
		},
		// Expires is a conservative local reuse deadline, not a claim about
		// the provider signature's exact expiry. A 403 still triggers the
		// caller's force-refresh path before this deadline.
		Expires: time.Now().Add(p115HLSCacheTTL),
	}, nil
}

func selectHLSVariant(masterURL, playlist string) (string, error) {
	base, err := url.Parse(masterURL)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(playlist, "\r\n", "\n"), "\n")
	bestBandwidth := int64(-1)
	bestURL := ""
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "#EXT-X-STREAM-INF:") {
			continue
		}
		bandwidth := hlsAttributeInt64(line, "BANDWIDTH")
		for j := i + 1; j < len(lines); j++ {
			candidate := strings.TrimSpace(lines[j])
			if candidate == "" {
				continue
			}
			if strings.HasPrefix(candidate, "#") {
				break
			}
			resolved, resolveErr := base.Parse(candidate)
			if resolveErr == nil && allowed115HLSURL(base, resolved) && bandwidth > bestBandwidth {
				bestBandwidth = bandwidth
				bestURL = resolved.String()
			}
			break
		}
	}
	if bestURL == "" {
		return "", errors.New("no allowed media variant")
	}
	return bestURL, nil
}

func hlsAttributeInt64(line, name string) int64 {
	if colon := strings.IndexByte(line, ':'); colon >= 0 {
		line = line[colon+1:]
	}
	prefix := strings.ToUpper(name) + "="
	for _, field := range strings.Split(line, ",") {
		field = strings.TrimSpace(field)
		upper := strings.ToUpper(field)
		if !strings.HasPrefix(upper, prefix) {
			continue
		}
		value := strings.TrimSpace(field[len(prefix):])
		n, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}

func allowed115HLSURL(master, candidate *url.URL) bool {
	if master == nil || candidate == nil {
		return false
	}
	if !strings.EqualFold(candidate.Scheme, "https") &&
		!(strings.EqualFold(master.Scheme, "http") && strings.EqualFold(candidate.Scheme, "http")) {
		return false
	}
	host := strings.ToLower(candidate.Hostname())
	masterHost := strings.ToLower(master.Hostname())
	return host != "" && (host == masterHost || host == "115.com" || strings.HasSuffix(host, ".115.com"))
}

func (d *Driver) rememberPickCode(fileID, pickCode string) {
	fileID = strings.TrimSpace(fileID)
	pickCode = strings.TrimSpace(pickCode)
	if fileID == "" || pickCode == "" {
		return
	}
	d.generationMu.Lock()
	if _, exists := d.pickCodes[fileID]; !exists && len(d.pickCodes) >= p115PickCodeCacheMaxEntries {
		// Losing a pick code only costs one GetFile call. Clearing at the hard
		// bound keeps a long-lived driver from retaining every file ever seen.
		clear(d.pickCodes)
	}
	d.pickCodes[fileID] = pickCode
	d.generationMu.Unlock()
}

func (d *Driver) rememberedPickCode(fileID string) string {
	d.generationMu.Lock()
	defer d.generationMu.Unlock()
	return d.pickCodes[fileID]
}

func (d *Driver) pruneGenerationCacheLocked(now time.Time) {
	for fileID, cached := range d.generationCache {
		if !now.Before(cached.expires) {
			delete(d.generationCache, fileID)
		}
	}
}

func (d *Driver) makeGenerationCacheRoomLocked() {
	if len(d.generationCache) < p115GenerationCacheMaxEntries {
		return
	}
	var (
		oldestID      string
		oldestExpires time.Time
	)
	for fileID, cached := range d.generationCache {
		if oldestID == "" || cached.expires.Before(oldestExpires) {
			oldestID = fileID
			oldestExpires = cached.expires
		}
	}
	if oldestID != "" {
		delete(d.generationCache, oldestID)
	}
}

func cloneStreamLink(link *drives.StreamLink) *drives.StreamLink {
	if link == nil {
		return nil
	}
	clone := *link
	clone.Headers = link.Headers.Clone()
	return &clone
}

func (d *Driver) downloadInfo(pickCode string, ua string) (*sdk.DownloadInfo, string, error) {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		ua = d.ua
	}
	info, err := d.client.DownloadWithUA(pickCode, ua)
	if err != nil {
		return nil, "", err
	}
	return info, ua, nil
}

func wrap115StreamTransientError(op string, err error) error {
	wrapped := fmt.Errorf("%s: %w", op, err)
	if !isTransient115StreamError(err) {
		return wrapped
	}
	return &drives.RateLimitError{
		Provider:   "p115",
		RetryAfter: p115ListCooldown,
		Err:        wrapped,
	}
}

// Rename 调用 115 SDK 把指定 fileID 重命名为 newName。
// 包装错误信息，方便日志定位是 115 端的失败。
func (d *Driver) Rename(ctx context.Context, fileID, newName string) error {
	if d.client == nil {
		return errors.New("p115 rename: driver not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fileID = strings.TrimSpace(fileID)
	newName = strings.TrimSpace(newName)
	if fileID == "" {
		return errors.New("p115 rename: empty fileID")
	}
	if newName == "" {
		return errors.New("p115 rename: empty newName")
	}
	if err := d.client.Rename(fileID, newName); err != nil {
		return fmt.Errorf("p115 rename: %w", err)
	}
	return nil
}

func (d *Driver) Remove(ctx context.Context, fileID string) error {
	if d.client == nil {
		return errors.New("p115 remove: driver not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return errors.New("p115 remove: empty fileID")
	}
	if err := d.client.Delete(fileID); err != nil {
		return fmt.Errorf("p115 remove: %w", err)
	}
	return nil
}

func (d *Driver) EnsureDir(ctx context.Context, pathFromRoot string) (string, error) {
	parts := splitPath(pathFromRoot)
	currentID := d.rootID
	for _, name := range parts {
		childID, err := d.findChildDir(ctx, currentID, name)
		if err != nil {
			return "", err
		}
		if childID == "" {
			id, err := d.mkdirContext(ctx, currentID, name)
			if err != nil {
				return "", fmt.Errorf("115 mkdir %s: %w", name, err)
			}
			childID = id
		}
		currentID = childID
	}
	return currentID, nil
}

// mkdirContext mirrors the SDK's Mkdir call while binding it to the caller's
// context. Besides cancellation, this is what keeps crawler upload proxy scope
// attached to the directory-creation request.
func (d *Driver) mkdirContext(ctx context.Context, parentID, name string) (string, error) {
	if d.client == nil || d.client.Client == nil {
		return "", errors.New("115 client not initialized")
	}
	result := sdk.MkdirResp{}
	resp, err := d.client.Client.R().
		SetContext(ctx).
		SetFormData(map[string]string{"pid": parentID, "cname": name}).
		SetResult(&result).
		ForceContentType("application/json;charset=UTF-8").
		Post(sdk.ApiDirAdd)
	if err = sdk.CheckErr(err, &result, resp); err != nil {
		return "", err
	}
	return string(result.CategoryID), nil
}

func (d *Driver) findChildDir(ctx context.Context, parent, name string) (string, error) {
	entries, err := d.List(ctx, parent)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir && e.Name == name {
			return e.ID, nil
		}
	}
	return "", nil
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func fileToEntry(f *sdk.File, parentID string) drives.Entry {
	return drives.Entry{
		ID:           f.FileID,
		Name:         f.Name,
		Size:         f.Size,
		Hash:         f.Sha1,
		IsDir:        f.IsDirectory,
		ParentID:     parentID,
		MimeType:     guessMime(f.Name),
		ModTime:      f.UpdateTime,
		ThumbnailURL: f.ThumbURL,
	}
}

func guessMime(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	}
	return "application/octet-stream"
}

var _ drives.Drive = (*Driver)(nil)
var _ drives.Remover = (*Driver)(nil)
