package streamhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HasSensitiveHeaders reports whether a StreamLink contains credentials that
// must not be placed in an ffmpeg/ffprobe command line or forwarded by those
// processes across redirects.
func HasSensitiveHeaders(headers http.Header) bool {
	if len(headers) == 0 {
		return false
	}
	for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
		if strings.TrimSpace(headers.Get(key)) != "" {
			return true
		}
	}
	return false
}

// StartLoopbackRelay exposes one fixed HTTP(S) source on a random loopback
// address. Provider credentials remain inside this process; redirect handling
// uses CheckRedirect, which strips them when the origin changes.
func StartLoopbackRelay(ctx context.Context, rawURL string, headers http.Header) (string, func(), error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", nil, errors.New("stream relay: source must be an HTTP(S) URL")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("stream relay: listen: %w", err)
	}
	sourceHeaders := headers.Clone()
	client := NewClient(0)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/stream" {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), nil)
			if err != nil {
				http.Error(w, "bad upstream request", http.StatusBadGateway)
				return
			}
			req.Header = sourceHeaders.Clone()
			for _, key := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
				if value := r.Header.Get(key); value != "" {
					req.Header.Set(key, value)
				}
			}
			resp, err := client.Do(req)
			if err != nil {
				http.Error(w, "upstream request failed", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			for _, key := range []string{
				"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
				"Last-Modified", "Etag", "Cache-Control",
			} {
				if value := resp.Header.Get(key); value != "" {
					w.Header().Set(key, value)
				}
			}
			w.WriteHeader(resp.StatusCode)
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, resp.Body)
			}
		}),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			_ = srv.Close()
			_ = ln.Close()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			cleanup()
		case <-done:
		}
	}()
	return "http://" + ln.Addr().String() + "/stream", cleanup, nil
}
