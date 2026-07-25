package p115

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/video-site/backend/internal/drives"
)

func TestIsTransient115ListError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "blocked html status title", err: errors.New(`<!doctypehtml><html lang="zh-cn"><title>405</title>Sorry, your request has been blocked as it may cause potential threats to the server's security.`), want: true},
		{name: "blocked html status title with whitespace", err: errors.New(`<!doctype html><title class="waf"> 405 </title>blocked`), want: true},
		{name: "chinese waf", err: errors.New("很抱歉，由于您访问的URL有可能对网站造成安全威胁，您的访问被阻断。"), want: false},
		{name: "status 405", err: errors.New("request failed with status: 405"), want: true},
		{name: "rate limit", err: errors.New("429 too many requests"), want: true},
		{name: "regular auth error", err: errors.New("invalid credential"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransient115ListError(tc.err); got != tc.want {
				t.Fatalf("isTransient115ListError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestSelectHLSVariantChoosesHighestBandwidthAndResolvesRelativeURL(t *testing.T) {
	master := "#EXTM3U\r\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360\r\nlow/index.m3u8\r\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=3000000,RESOLUTION=1920x1080\r\nhigh/index.m3u8\r\n"
	got, err := selectHLSVariant("https://115.com/api/video/master.m3u8", master)
	if err != nil {
		t.Fatalf("select variant: %v", err)
	}
	if got != "https://115.com/api/video/high/index.m3u8" {
		t.Fatalf("variant = %q", got)
	}
}

func TestSelectHLSVariantRejectsExternalAndHTTPSDowngrade(t *testing.T) {
	for _, playlist := range []string{
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttps://evil.example/video.m3u8\n",
		"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttp://115.com/video.m3u8\n",
	} {
		if got, err := selectHLSVariant("https://115.com/master.m3u8", playlist); err == nil {
			t.Fatalf("variant = %q, want rejection", got)
		}
	}
}

func TestGenerationStreamURLKeepsCookieOutOfReturnedLinkAndCaches(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if got := r.Header.Get("Cookie"); got != "UID=u;CID=c;SEID=s" {
			t.Errorf("cookie = %q", got)
		}
		if got := r.Header.Get("Referer"); got != p115HLSReferer {
			t.Errorf("referer = %q", got)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=100\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=200\nhigh.m3u8\n")
	}))
	t.Cleanup(server.Close)

	driver := New(Config{ID: "115", Cookie: "UID=u;CID=c;SEID=s"})
	driver.hlsClient = server.Client()
	driver.hlsMasterBaseURL = server.URL + "/api/video/m3u8"
	driver.rememberPickCode("file-1", "pick-1")

	first, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
	if err != nil {
		t.Fatalf("first generation stream: %v", err)
	}
	if first.URL != server.URL+"/api/video/m3u8/high.m3u8" {
		t.Fatalf("url = %q", first.URL)
	}
	if first.Headers.Get("Cookie") != "" {
		t.Fatalf("returned cookie = %q", first.Headers.Get("Cookie"))
	}
	if first.Headers.Get("User-Agent") != p115HLSUserAgent || first.Headers.Get("Referer") != p115HLSReferer {
		t.Fatalf("returned headers = %#v", first.Headers)
	}

	second, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
	if err != nil || second.URL != first.URL {
		t.Fatalf("cached generation stream = %#v, %v", second, err)
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("master requests = %d, want 1 cached request", gotRequests)
	}

	if _, err := driver.GenerationStreamURL(context.Background(), "file-1", true); err != nil {
		t.Fatalf("force refresh: %v", err)
	}
	mu.Lock()
	gotRequests = requests
	mu.Unlock()
	if gotRequests != 2 {
		t.Fatalf("master requests after refresh = %d, want 2", gotRequests)
	}
}

func TestGenerationStreamURLCoalescesConcurrentResolution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		once.Do(func() { close(started) })
		<-release
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nstream.m3u8\n")
	}))
	t.Cleanup(server.Close)

	driver := New(Config{ID: "115", Cookie: "UID=u;CID=c;SEID=s"})
	driver.hlsClient = server.Client()
	driver.hlsMasterBaseURL = server.URL
	driver.rememberPickCode("file-1", "pick")

	type result struct {
		link *drives.StreamLink
		err  error
	}
	results := make(chan result, 2)
	go func() {
		link, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
		results <- result{link: link, err: err}
	}()
	<-started
	go func() {
		link, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
		results <- result{link: link, err: err}
	}()
	close(release)
	for range 2 {
		got := <-results
		if got.err != nil || got.link == nil {
			t.Fatalf("generation stream = %#v, %v", got.link, got.err)
		}
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("master requests = %d, want 1", gotRequests)
	}
}

func TestGenerationStreamURLForceRefreshSupersedesOrdinaryInflight(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once

	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		requestNumber := requests
		mu.Unlock()
		if requestNumber == 1 {
			close(firstStarted)
			<-releaseFirst
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nstale.m3u8\n")
			return
		}
		_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nfresh.m3u8\n")
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseFirst) })
		server.Close()
	})

	driver := New(Config{ID: "115", Cookie: "UID=u;CID=c;SEID=s"})
	driver.hlsClient = server.Client()
	driver.hlsMasterBaseURL = server.URL
	driver.rememberPickCode("file-1", "pick")

	type result struct {
		link *drives.StreamLink
		err  error
	}
	ordinaryResult := make(chan result, 1)
	go func() {
		link, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
		ordinaryResult <- result{link: link, err: err}
	}()
	<-firstStarted

	refreshResult := make(chan result, 1)
	go func() {
		link, err := driver.GenerationStreamURL(context.Background(), "file-1", true)
		refreshResult <- result{link: link, err: err}
	}()

	select {
	case got := <-refreshResult:
		if got.err != nil || got.link == nil || !strings.HasSuffix(got.link.URL, "/fresh.m3u8") {
			t.Fatalf("refreshed generation stream = %#v, %v", got.link, got.err)
		}
	case <-time.After(2 * time.Second):
		releaseOnce.Do(func() { close(releaseFirst) })
		t.Fatal("forced refresh joined the older in-flight resolution")
	}

	releaseOnce.Do(func() { close(releaseFirst) })
	if got := <-ordinaryResult; got.err != nil || got.link == nil || !strings.HasSuffix(got.link.URL, "/stale.m3u8") {
		t.Fatalf("ordinary generation stream = %#v, %v", got.link, got.err)
	}

	cached, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
	if err != nil || cached == nil || !strings.HasSuffix(cached.URL, "/fresh.m3u8") {
		t.Fatalf("cached generation stream = %#v, %v; want refreshed link", cached, err)
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 2 {
		t.Fatalf("master requests = %d, want 2", gotRequests)
	}
}

func TestGenerationCachePrunesExpiredEntriesAndEvictsAtCapacity(t *testing.T) {
	driver := New(Config{ID: "115"})
	now := time.Now()
	driver.generationCache["expired"] = cachedGenerationStream{expires: now.Add(-time.Second)}
	driver.generationCache["valid"] = cachedGenerationStream{expires: now.Add(time.Minute)}
	driver.pruneGenerationCacheLocked(now)
	if _, ok := driver.generationCache["expired"]; ok {
		t.Fatal("expired generation stream remained cached")
	}
	if _, ok := driver.generationCache["valid"]; !ok {
		t.Fatal("valid generation stream was pruned")
	}

	clear(driver.generationCache)
	for i := 0; i < p115GenerationCacheMaxEntries; i++ {
		driver.generationCache[fmt.Sprintf("file-%d", i)] = cachedGenerationStream{
			expires: now.Add(time.Duration(i+1) * time.Second),
		}
	}
	driver.makeGenerationCacheRoomLocked()
	if got := len(driver.generationCache); got != p115GenerationCacheMaxEntries-1 {
		t.Fatalf("generation cache entries = %d, want %d after eviction", got, p115GenerationCacheMaxEntries-1)
	}
	if _, ok := driver.generationCache["file-0"]; ok {
		t.Fatal("oldest generation cache entry was not evicted")
	}
}

func TestGenerationStreamURLClassifiesUnavailableAndRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          int
		body            string
		wantUnavailable bool
		wantRateLimit   bool
	}{
		{name: "missing", status: http.StatusNotFound, wantUnavailable: true},
		{name: "malformed", status: http.StatusOK, body: "not a playlist", wantUnavailable: true},
		{name: "waf", status: http.StatusMethodNotAllowed, body: "<title>405</title>", wantRateLimit: true},
		// Online playback being refused must leave the ordinary download URL
		// usable instead of cooling the drive down as if it were throttled.
		{name: "forbidden", status: http.StatusForbidden, wantUnavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			driver := New(Config{ID: "115", Cookie: "UID=u;CID=c;SEID=s"})
			driver.hlsClient = server.Client()
			driver.hlsMasterBaseURL = server.URL
			driver.rememberPickCode("file-1", "pick")
			_, err := driver.GenerationStreamURL(context.Background(), "file-1", false)
			if tc.wantUnavailable != errors.Is(err, drives.ErrGenerationStreamUnavailable) {
				t.Fatalf("error = %v, unavailable=%v", err, tc.wantUnavailable)
			}
			_, isRateLimit := drives.RateLimitRetryAfter(err)
			if isRateLimit != tc.wantRateLimit {
				t.Fatalf("error = %v, rate limit=%v", err, isRateLimit)
			}
		})
	}
}

func TestWrap115StreamTransientError(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		wantRateLimit bool
	}{
		{name: "unexpected", err: errors.New("unexpected error"), wantRateLimit: false},
		{name: "405 blocked", err: errors.New("405 request has been blocked"), wantRateLimit: true},
		{name: "405 waf html", err: errors.New(`<!doctypehtml><html><title>405</title><p>blocked</p>`), wantRateLimit: true},
		{name: "429", err: errors.New("429 too many requests"), wantRateLimit: true},
		{name: "403 authentication", err: errors.New("403 forbidden"), wantRateLimit: false},
		{name: "tls timeout", err: errors.New("net/http: TLS handshake timeout"), wantRateLimit: true},
		{name: "blocked", err: errors.New("blocked by waf"), wantRateLimit: false},
		{name: "auth", err: errors.New("invalid credential"), wantRateLimit: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wrap115StreamTransientError("115 get file", tc.err)
			var rateLimit *drives.RateLimitError
			isRateLimit := errors.As(got, &rateLimit)
			if isRateLimit != tc.wantRateLimit {
				t.Fatalf("rate limit = %v, want %v; err=%v", isRateLimit, tc.wantRateLimit, got)
			}
			if !strings.Contains(got.Error(), "115 get file") {
				t.Fatalf("err = %v, want operation prefix", got)
			}
			if tc.wantRateLimit {
				if rateLimit.Provider != "p115" {
					t.Fatalf("provider = %q, want p115", rateLimit.Provider)
				}
				if rateLimit.RetryAfter != 10*time.Minute {
					t.Fatalf("retry after = %s, want 10m", rateLimit.RetryAfter)
				}
			}
		})
	}
}

func TestRememberPickCodeStaysBounded(t *testing.T) {
	driver := New(Config{ID: "115"})
	for i := 0; i <= p115PickCodeCacheMaxEntries; i++ {
		fileID := fmt.Sprintf("file-%d", i)
		driver.rememberPickCode(fileID, "pick-"+fileID)
	}
	if got := len(driver.pickCodes); got > p115PickCodeCacheMaxEntries {
		t.Fatalf("pick-code cache entries = %d, max = %d", got, p115PickCodeCacheMaxEntries)
	}
	if got := driver.rememberedPickCode(fmt.Sprintf("file-%d", p115PickCodeCacheMaxEntries)); got == "" {
		t.Fatal("most recently remembered pick code was evicted")
	}
}

// TestBufferAndHashSha1 验证 bufferAndHashSha1：
//
//   - 把 reader 的全部字节落到 tmp 文件
//   - SHA1 与标准库一致（HEX 大写）
//   - declaredSize=0 时不校验，>0 时严格校验
//   - 调用方拿到的 *os.File 可以 Seek 回 0 重新读出原文（OSS SDK 上传需要）
func TestBufferAndHashSha1(t *testing.T) {
	body := []byte("hello-115-upload-test")
	want := sha1.Sum(body)
	wantHex := strings.ToUpper(hex.EncodeToString(want[:]))

	t.Run("declared size matches", func(t *testing.T) {
		tmp, gotHex, n, err := bufferAndHashSha1("", bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("bufferAndHashSha1 returned error: %v", err)
		}
		defer cleanup(tmp)
		if gotHex != wantHex {
			t.Errorf("sha1 = %s, want %s", gotHex, wantHex)
		}
		if n != int64(len(body)) {
			t.Errorf("written = %d, want %d", n, len(body))
		}
		// Seek 回 0，应能读出原文
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("seek: %v", err)
		}
		got, err := io.ReadAll(tmp)
		if err != nil {
			t.Fatalf("read tmp: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("tmp content mismatch: got %q want %q", string(got), string(body))
		}
	})

	t.Run("declared size mismatch returns error", func(t *testing.T) {
		_, _, _, err := bufferAndHashSha1("", bytes.NewReader(body), int64(len(body))+1)
		if err == nil {
			t.Fatal("expected size mismatch error, got nil")
		}
	})

	t.Run("declared size zero is unchecked", func(t *testing.T) {
		tmp, gotHex, n, err := bufferAndHashSha1("", bytes.NewReader(body), 0)
		if err != nil {
			t.Fatalf("bufferAndHashSha1 returned error: %v", err)
		}
		defer cleanup(tmp)
		if gotHex != wantHex {
			t.Errorf("sha1 = %s, want %s", gotHex, wantHex)
		}
		if n != int64(len(body)) {
			t.Errorf("written = %d, want %d", n, len(body))
		}
	})

	t.Run("uses configured temp dir", func(t *testing.T) {
		tempDir := filepath.Join(t.TempDir(), "upload-tmp")
		tmp, _, _, err := bufferAndHashSha1(tempDir, bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatalf("bufferAndHashSha1 returned error: %v", err)
		}
		defer cleanup(tmp)
		if gotDir := filepath.Dir(tmp.Name()); gotDir != tempDir {
			t.Fatalf("tmp dir = %q, want %q", gotDir, tempDir)
		}
	})
}

// TestUploadAndReportSha1RejectsInvalidArgs 检查空 reader / 空 name / 负 size 在
// 客户端未初始化前就被拒绝，避免下游 SDK 在错误参数下做异步初始化和真实网络调用。
func TestUploadAndReportSha1RejectsInvalidArgs(t *testing.T) {
	d := New(Config{ID: "p115-test"})
	// 注意：未调 Init，因此 d.client == nil，第一道防线就会拒绝。

	cases := []struct {
		name      string
		parentID  string
		fname     string
		body      io.Reader
		size      int64
		wantSubst string
	}{
		{name: "nil client", parentID: "0", fname: "x.mp4", body: bytes.NewReader([]byte("ok")), size: 2, wantSubst: "not initialized"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.UploadAndReportSha1(context.Background(), c.parentID, c.fname, c.body, c.size)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), c.wantSubst) {
				t.Fatalf("err = %v, want containing %q", err, c.wantSubst)
			}
		})
	}
}

func cleanup(f *os.File) {
	if f == nil {
		return
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
}
