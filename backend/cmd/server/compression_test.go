package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestResponseCompressionMiddlewareCompressesLargeJSON(t *testing.T) {
	payload := strings.Repeat(`{"title":"benchmark"}`, 256)
	handler := responseCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, payload)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0.5, br;q=1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br", got)
	}
	if !headerContainsToken(rr.Header(), "Vary", "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", rr.Header().Values("Vary"))
	}
	decoded, err := io.ReadAll(brotli.NewReader(rr.Body))
	if err != nil {
		t.Fatalf("decode brotli response: %v", err)
	}
	if string(decoded) != payload {
		t.Fatal("decoded response does not match original payload")
	}
}

func TestResponseCompressionMiddlewareLeavesSmallAndMediaResponsesAlone(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "small", path: "/api/settings/theme", body: `{"theme":"dark"}`},
		{name: "media", path: "/p/preview/video", body: strings.Repeat("video", 1024)},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := responseCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			req.Header.Set("Accept-Encoding", "br, gzip")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if got := rr.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want empty", got)
			}
			if rr.Body.String() != test.body {
				t.Fatal("response body changed")
			}
		})
	}
}

func TestResponseCompressionMiddlewareHonorsEarlyFlush(t *testing.T) {
	payload := strings.Repeat("x", responseCompressionThreshold*2)
	handler := responseCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, payload)
	}))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/backups/id/restore", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q after early flush, want empty", got)
	}
	if rr.Body.String() != payload {
		t.Fatal("flushed response body changed")
	}
}

func TestResponseCompressionMiddlewarePreservesSpecialResponses(t *testing.T) {
	payload := strings.Repeat("response", responseCompressionThreshold)
	tests := []struct {
		name       string
		method     string
		rangeValue string
		status     int
	}{
		{name: "head", method: http.MethodHead, status: http.StatusOK},
		{name: "range", method: http.MethodGet, rangeValue: "bytes=0-99", status: http.StatusPartialContent},
		{name: "not-modified", method: http.MethodGet, status: http.StatusNotModified},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := responseCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if test.status == http.StatusPartialContent {
					w.Header().Set("Content-Range", "bytes 0-99/1000")
				}
				w.WriteHeader(test.status)
				if test.method != http.MethodHead && test.status != http.StatusNotModified {
					_, _ = io.WriteString(w, payload)
				}
			}))
			req := httptest.NewRequest(test.method, "/api/list", nil)
			req.Header.Set("Accept-Encoding", "br, gzip")
			if test.rangeValue != "" {
				req.Header.Set("Range", test.rangeValue)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if got := rr.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q, want empty", got)
			}
			if rr.Code != test.status {
				t.Fatalf("status = %d, want %d", rr.Code, test.status)
			}
		})
	}
}

func TestResponseCompressionMiddlewareCompressesLargeError(t *testing.T) {
	payload := strings.Repeat("upstream failure ", 256)
	handler := responseCompressionMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, payload)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/list", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadGateway)
	}
	if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != payload {
		t.Fatal("decoded error response does not match original")
	}
}

func TestNegotiateResponseEncodingHonorsQualityValues(t *testing.T) {
	for _, test := range []struct {
		header string
		want   string
	}{
		{header: "gzip, br", want: "br"},
		{header: "br;q=0, gzip;q=0.4", want: "gzip"},
		{header: "gzip;q=0, *;q=0.2", want: "br"},
		{header: "br;q=0, gzip;q=0", want: ""},
		{header: "identity", want: ""},
	} {
		if got := negotiateResponseEncoding([]string{test.header}); got != test.want {
			t.Errorf("negotiateResponseEncoding(%q) = %q, want %q", test.header, got, test.want)
		}
	}
}

func TestFrontendHandlerServesPrecompressedHashedAsset(t *testing.T) {
	dir := t.TempDir()
	assetDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := strings.Repeat("const optimized = true;\n", 128)
	assetPath := filepath.Join(assetDir, "app.js")
	if err := os.WriteFile(assetPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.Create(assetPath + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(compressed)
	if _, err := io.WriteString(gzipWriter, original); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	frontendHandler(dir).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") && !strings.HasPrefix(got, "application/javascript") {
		t.Fatalf("Content-Type = %q, want JavaScript", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != frontendHashedAssetCacheControl {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !headerContainsToken(rr.Header(), "Vary", "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", rr.Header().Values("Vary"))
	}
	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(decoded) != original {
		t.Fatal("precompressed asset does not decode to original")
	}
}

func headerContainsToken(header http.Header, name, want string) bool {
	for _, value := range header.Values(name) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				return true
			}
		}
	}
	return false
}
