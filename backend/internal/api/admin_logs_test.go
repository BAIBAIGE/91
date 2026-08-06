package api

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"

	"github.com/video-site/backend/internal/applog"
	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/catalog"
)

func TestHandleLogsReturnsFilteredBoundedSnapshot(t *testing.T) {
	store := newAdminLogsTestStore(t)
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "worker ready"})
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "worker failed: first"})
	appendAdminLog(t, store, applog.Entry{
		Source:  applog.SourceHTTP,
		Method:  applog.MethodGET,
		Status:  http.StatusInternalServerError,
		Path:    "/api",
		Message: `"GET http://example.test/api HTTP/1.1" from 127.0.0.1:1 - 500 9B in 1ms`,
	})
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "worker failed: latest"})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?source=application&level=error&q=worker&limit=1", nil)
	rr := httptest.NewRecorder()
	(&AdminServer{Logs: store}).handleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var got applog.Snapshot
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Matched != 1 || got.StorageBytes == 0 || got.MaxStorageBytes == 0 || got.NextCursor == "" {
		t.Fatalf("metadata = %#v", got)
	}
	if len(got.Entries) != 1 || got.Entries[0].Message != "worker failed: latest" {
		t.Fatalf("entries = %#v", got.Entries)
	}
}

func TestHandleLogsNegotiatesResponseCompression(t *testing.T) {
	store := newAdminLogsTestStore(t)
	for range 40 {
		appendAdminLog(t, store, applog.Entry{
			Source:  applog.SourceApplication,
			Message: strings.Repeat("repeated log payload ", 20),
		})
	}

	tests := []struct {
		name           string
		acceptEncoding string
		wantEncoding   string
	}{
		{name: "identity"},
		{name: "gzip", acceptEncoding: "gzip", wantEncoding: "gzip"},
		{name: "brotli preferred", acceptEncoding: "gzip, br", wantEncoding: "br"},
		{name: "quality honored", acceptEncoding: "br;q=0.2, gzip;q=0.8", wantEncoding: "gzip"},
		{name: "disabled", acceptEncoding: "br;q=0, gzip;q=0"},
		{name: "wildcard", acceptEncoding: "*;q=0.5", wantEncoding: "br"},
	}
	bodySizes := make(map[string]int, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=100", nil)
			if test.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", test.acceptEncoding)
			}
			rr := httptest.NewRecorder()
			(&AdminServer{Logs: store}).handleLogs(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			if got := rr.Header().Get("Content-Encoding"); got != test.wantEncoding {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.wantEncoding)
			}
			if got := strings.Join(rr.Header().Values("Vary"), ","); !strings.Contains(got, "Accept-Encoding") {
				t.Fatalf("Vary = %q, want Accept-Encoding", got)
			}
			bodySizes[test.name] = rr.Body.Len()

			var reader io.Reader = rr.Body
			switch test.wantEncoding {
			case "br":
				reader = brotli.NewReader(reader)
			case "gzip":
				gzipReader, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatalf("open gzip response: %v", err)
				}
				defer func() { _ = gzipReader.Close() }()
				reader = gzipReader
			}
			var snapshot applog.Snapshot
			if err := json.NewDecoder(reader).Decode(&snapshot); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if len(snapshot.Entries) != 40 {
				t.Fatalf("entries = %d, want 40", len(snapshot.Entries))
			}
		})
	}
	if bodySizes["gzip"] >= bodySizes["identity"] {
		t.Fatalf("gzip response = %d bytes, identity = %d", bodySizes["gzip"], bodySizes["identity"])
	}
	if bodySizes["brotli preferred"] >= bodySizes["identity"] {
		t.Fatalf("brotli response = %d bytes, identity = %d", bodySizes["brotli preferred"], bodySizes["identity"])
	}
}

func TestHandleLogsFiltersByHTTPMethod(t *testing.T) {
	store := newAdminLogsTestStore(t)
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceHTTP, Method: applog.MethodGET, Status: 200, Path: "/api", Message: "GET /api"})
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceHTTP, Method: applog.MethodPOST, Status: 201, Path: "/api", Message: "POST /api"})
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "POST appears in application text"})

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs?method=POST", nil)
	rr := httptest.NewRecorder()
	(&AdminServer{Logs: store}).handleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got applog.Snapshot
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Matched != 1 || len(got.Entries) != 1 || got.Entries[0].Method != applog.MethodPOST {
		t.Fatalf("POST entries = %#v", got)
	}
}

func TestHandleLogsValidatesQuery(t *testing.T) {
	store := newAdminLogsTestStore(t)
	maxLimitRR := httptest.NewRecorder()
	maxLimitReq := httptest.NewRequest(http.MethodGet, "/admin/api/logs?limit=10000", nil)
	(&AdminServer{Logs: store}).handleLogs(maxLimitRR, maxLimitReq)
	if maxLimitRR.Code != http.StatusOK {
		t.Fatalf("maximum frontend buffer limit status = %d body=%s", maxLimitRR.Code, maxLimitRR.Body.String())
	}

	tests := []string{
		"?limit=0",
		"?limit=10001",
		"?cursor=" + strings.Repeat("x", 4097),
		"?source=filesystem",
		"?level=debug",
		"?method=TRACE",
		"?q=" + strings.Repeat("x", 201),
	}
	for _, suffix := range tests {
		t.Run(suffix, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/admin/api/logs"+suffix, nil)
			(&AdminServer{Logs: store}).handleLogs(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleClearLogsRemovesPersistentHistoryAndInvalidatesCursor(t *testing.T) {
	store := newAdminLogsTestStore(t)
	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "before clear"})
	before, err := store.Query(applog.Query{Limit: 10})
	if err != nil {
		t.Fatalf("query before clear: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/logs", nil)
	(&AdminServer{Logs: store}).handleClearLogs(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}

	appendAdminLog(t, store, applog.Entry{Source: applog.SourceApplication, Message: "after clear"})
	after, err := store.Query(applog.Query{Limit: 10, Cursor: before.NextCursor})
	if err != nil {
		t.Fatalf("query after clear: %v", err)
	}
	if !after.Reset || len(after.Entries) != 1 || after.Entries[0].Message != "after clear" {
		t.Fatalf("snapshot after clear = %#v", after)
	}
}

func TestHandleLogsUnavailableWithoutStore(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	(&AdminServer{}).handleLogs(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRegisteredLogsRouteRequiresAdministratorSession(t *testing.T) {
	cat, err := catalog.Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	router := chi.NewRouter()
	(&AdminServer{
		Catalog: cat,
		Auth:    &auth.Authenticator{Catalog: cat},
		Logs:    newAdminLogsTestStore(t),
	}).Register(router)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	deleteRR := httptest.NewRecorder()
	router.ServeHTTP(deleteRR, httptest.NewRequest(http.MethodDelete, "/admin/api/logs", nil))
	if deleteRR.Code != http.StatusUnauthorized {
		t.Fatalf("delete status = %d body=%s", deleteRR.Code, deleteRR.Body.String())
	}

	viewerID, err := cat.CreateUser(context.Background(), "viewer", "unused-hash", "user")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := cat.CreateSession(context.Background(), "viewer-session", time.Hour, viewerID); err != nil {
		t.Fatalf("create viewer session: %v", err)
	}
	viewerReq := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	viewerReq.AddCookie(&http.Cookie{Name: "vs_admin", Value: "viewer-session"})
	viewerRR := httptest.NewRecorder()
	router.ServeHTTP(viewerRR, viewerReq)
	if viewerRR.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d body=%s", viewerRR.Code, viewerRR.Body.String())
	}

	adminID, err := cat.CreateUser(context.Background(), "operator", "unused-hash", "admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := cat.CreateSession(context.Background(), "admin-session", time.Hour, adminID); err != nil {
		t.Fatalf("create admin session: %v", err)
	}
	adminReq := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	adminReq.AddCookie(&http.Cookie{Name: "vs_admin", Value: "admin-session"})
	adminRR := httptest.NewRecorder()
	router.ServeHTTP(adminRR, adminReq)
	if adminRR.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", adminRR.Code, adminRR.Body.String())
	}
}

func newAdminLogsTestStore(t *testing.T) *applog.Store {
	t.Helper()
	store, err := applog.Open(applog.Config{
		Directory:         t.TempDir(),
		MaxLineBytes:      1024,
		MaxFileSizeBytes:  1 << 20,
		MaxTotalSizeBytes: 4 << 20,
	})
	if err != nil {
		t.Fatalf("open log store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func appendAdminLog(t *testing.T, store *applog.Store, entry applog.Entry) {
	t.Helper()
	if err := store.AppendEntry(entry); err != nil {
		t.Fatalf("append log entry: %v", err)
	}
}
