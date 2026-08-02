package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/video-site/backend/internal/applog"
	"github.com/video-site/backend/internal/requestmeta"
)

const frontendHashedAssetCacheControl = "public, max-age=31536000, immutable"

type capturedLogFormatter struct {
	access      *middleware.DefaultLogFormatter
	panicLogger *log.Logger
	logs        *applog.Store
}

func (f *capturedLogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	remote := requestmeta.ClientIP(r)
	requestForAccessLog := r
	if remote != "" {
		requestCopy := new(http.Request)
		*requestCopy = *r
		requestCopy.RemoteAddr = remote
		requestForAccessLog = requestCopy
	}
	return &capturedLogEntry{
		LogEntry:    f.access.NewLogEntry(requestForAccessLog),
		panicLogger: f.panicLogger,
		logs:        f.logs,
		request:     r,
		remote:      remote,
	}
}

type capturedLogEntry struct {
	middleware.LogEntry
	panicLogger *log.Logger
	logs        *applog.Store
	request     *http.Request
	remote      string
}

func (e *capturedLogEntry) Write(status, bytes int, header http.Header, elapsed time.Duration, extra any) {
	// Keep the human-readable operational stream independently of file logging.
	e.LogEntry.Write(status, bytes, header, elapsed, extra)
	if e.logs == nil || e.request == nil {
		return
	}
	target := e.request.URL.RequestURI()
	if target == "" {
		target = "/"
	}
	scheme := "http"
	if e.request.TLS != nil {
		scheme = "https"
	}
	requestID := middleware.GetReqID(e.request.Context())
	prefix := ""
	if requestID != "" {
		prefix = "[" + requestID + "] "
	}
	remote := e.remote
	if remote == "" {
		remote = e.request.RemoteAddr
	}
	message := fmt.Sprintf("%s%q from %s - %d %dB in %s",
		prefix,
		e.request.Method+" "+scheme+"://"+e.request.Host+target+" "+e.request.Proto,
		remote,
		status,
		bytes,
		elapsed,
	)
	_ = e.logs.AppendEntry(applog.Entry{
		Timestamp: time.Now(),
		Source:    applog.SourceHTTP,
		Method:    applog.Method(e.request.Method),
		Status:    status,
		Path:      target,
		Remote:    remote,
		Bytes:     bytes,
		Elapsed:   elapsed.String(),
		RequestID: requestID,
		Message:   message,
	})
}

func (e *capturedLogEntry) Panic(value any, stack []byte) {
	if e.panicLogger != nil {
		e.panicLogger.Printf("[http] panic: %v\n%s", value, stack)
		return
	}
	fmt.Fprintf(os.Stderr, "[http] panic: %v\n%s", value, stack)
}

// requestLogMiddleware writes a human-readable access line to stdout and a
// structured durable entry for the admin viewer. The viewer endpoint itself is
// omitted so polling cannot generate self-referential log traffic.
func requestLogMiddleware(accessLogger, panicLogger *log.Logger, logs *applog.Store) func(http.Handler) http.Handler {
	requestLogger := middleware.RequestLogger(&capturedLogFormatter{
		access: &middleware.DefaultLogFormatter{
			Logger:  accessLogger,
			NoColor: true,
		},
		panicLogger: panicLogger,
		logs:        logs,
	})
	return func(next http.Handler) http.Handler {
		logged := requestLogger(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/admin/api/logs" {
				next.ServeHTTP(w, r)
				return
			}
			logged.ServeHTTP(w, r)
		})
	}
}

// corsMiddleware 返回一个 chi 中间件，按白名单匹配 Origin 决定是否回写
// CORS 响应头。
//
// 设计要点：
//   - 不再反射任意 Origin。Origin 必须出现在 allowedOrigins 中才会得到
//     Access-Control-Allow-Origin / Allow-Credentials 的"放行"响应头；
//     不在白名单的跨源请求拿不到这些头，浏览器会拒绝读响应内容。
//   - 同源请求（浏览器不发 Origin 头，或 Origin 等于自己）不需要 CORS 头，
//     直接放行。
//   - 始终带 Vary: Origin，避免反代缓存把 A Origin 的允许头喂给 B Origin。
//   - 对不在白名单的 OPTIONS 预检直接 403，避免被当成"放行"信号。
//
// allowedOrigins 由 config.Server.AllowedOrigins 注入；默认为空 = 完全
// 不允许跨源（最安全的默认值，同源部署不受影响）。
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allow := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		o = strings.TrimSpace(o)
		if o == "" || o == "*" {
			// 通配符在带 cookie 的 CORS 下没意义且危险，直接忽略
			continue
		}
		allow[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// 任何走过 CORS 检查的响应都要带 Vary: Origin，避免缓存污染。
			w.Header().Add("Vary", "Origin")

			isAllowedOrigin := false
			if origin != "" {
				_, isAllowedOrigin = allow[origin]
			}

			if isAllowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "600")
			}

			if r.Method == http.MethodOptions {
				// 预检请求：只对白名单 Origin 返回 204；否则 403 让浏览器把请求拦下来。
				// 同源场景一般不会触发预检（浏览器只在跨源 + 复杂请求时才发 OPTIONS）。
				if isAllowedOrigin {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if origin != "" {
					http.Error(w, "cors: origin not allowed", http.StatusForbidden)
					return
				}
				// 没带 Origin 的 OPTIONS 不是 CORS 预检（可能是健康检查工具），
				// 直接交给下游处理。
			}

			next.ServeHTTP(w, r)
		})
	}
}

func mountFrontend(r chi.Router) {
	dir, ok := resolveFrontendDir()
	if !ok {
		return
	}
	log.Printf("serving frontend from %s", dir)
	r.NotFound(frontendHandler(dir))
}

func resolveFrontendDir() (string, bool) {
	candidates := []string{}
	if dir := strings.TrimSpace(os.Getenv("VIDEO_FRONTEND_DIR")); dir != "" {
		candidates = append(candidates, dir)
	} else {
		candidates = append(candidates, "./dist", "../dist")
	}
	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		indexPath := filepath.Join(dir, "index.html")
		if st, err := os.Stat(indexPath); err == nil && !st.IsDir() {
			return dir, true
		}
	}
	return "", false
}

func frontendHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if isBackendRoute(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		cleanPath := path.Clean("/" + r.URL.Path)
		rel := strings.TrimPrefix(cleanPath, "/")
		if rel != "" && rel != "." {
			name := filepath.FromSlash(rel)
			f, err := os.Open(filepath.Join(dir, name))
			if err == nil {
				defer f.Close()
				if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
					if strings.HasPrefix(cleanPath, "/assets/") {
						w.Header().Set("Cache-Control", frontendHashedAssetCacheControl)
					}
					http.ServeContent(w, r, st.Name(), st.ModTime(), f)
					return
				}
			}
			if filepath.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}

		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}
}

func isBackendRoute(p string) bool {
	return p == "/api" ||
		strings.HasPrefix(p, "/api/") ||
		p == "/admin/api" ||
		strings.HasPrefix(p, "/admin/api/") ||
		p == "/p" ||
		strings.HasPrefix(p, "/p/")
}

func parseBoolDefault(raw string, def bool) bool {
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

func parseIntDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}
