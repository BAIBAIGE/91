package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/video-site/backend/internal/catalog"
)

func TestGetSettingsReturnsDatabasePreferencesOnly(t *testing.T) {
	server := &AdminServer{GetTheme: func() string { return "sky" }}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil)
	server.handleGetSettings(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
	var response settingsDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Theme != "sky" {
		t.Fatalf("theme = %q, want sky", response.Theme)
	}
	if !response.BuiltinTagsEnabled {
		t.Fatal("builtinTagsEnabled = false, want backwards-compatible true default")
	}
	if strings.Contains(recorder.Body.String(), "nightlyStartTime") {
		t.Fatalf("config.yaml fields leaked into settings endpoint: %s", recorder.Body.String())
	}
}

func TestPutSettingsTogglesBuiltinTagPackAndStartsRetagOnRestore(t *testing.T) {
	cat, err := catalog.Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	retagStarts := 0
	tagChanges := 0
	server := &AdminServer{
		Catalog: cat,
		OnStartTagRetag: func() bool {
			retagStarts++
			return true
		},
		OnTagsChanged: func() { tagChanges++ },
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/settings",
		strings.NewReader(`{"builtinTagsEnabled":false}`),
	)
	server.handlePutSettings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response settingsDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode disable response: %v", err)
	}
	if response.BuiltinTagsEnabled {
		t.Fatal("builtinTagsEnabled = true after disabling")
	}
	if retagStarts != 0 {
		t.Fatalf("retag starts after disabling = %d, want 0", retagStarts)
	}
	if tagChanges != 1 {
		t.Fatalf("tag cache invalidations after disabling = %d, want 1", tagChanges)
	}
	tags, err := cat.ListTags(request.Context())
	if err != nil {
		t.Fatalf("list tags after disabling: %v", err)
	}
	for _, tag := range tags {
		if tag.Source == "builtin" {
			t.Fatalf("builtin tag %q survived disable", tag.Label)
		}
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPut,
		"/admin/api/settings",
		strings.NewReader(`{"builtinTagsEnabled":true}`),
	)
	server.handlePutSettings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode enable response: %v", err)
	}
	if !response.BuiltinTagsEnabled {
		t.Fatal("builtinTagsEnabled = false after enabling")
	}
	if retagStarts != 1 {
		t.Fatalf("retag starts after enabling = %d, want 1", retagStarts)
	}
	if tagChanges != 2 {
		t.Fatalf("tag cache invalidations after enabling = %d, want 2", tagChanges)
	}
}

func TestPutSettingsRejectsNullBuiltinTagSettingWithoutDeletingTags(t *testing.T) {
	cat, err := catalog.Open(t.TempDir() + "/catalog.db")
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	server := &AdminServer{Catalog: cat}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/settings",
		strings.NewReader(`{"builtinTagsEnabled":null}`),
	)
	server.handlePutSettings(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s; want 400", recorder.Code, recorder.Body.String())
	}
	enabled, err := cat.BuiltinTagsEnabled(request.Context())
	if err != nil || !enabled {
		t.Fatalf("builtin setting after rejected request = %v, %v; want enabled", enabled, err)
	}
}

func TestPutSettingsStillSupportsPartialThemeUpdate(t *testing.T) {
	theme := "dark"
	server := &AdminServer{
		GetTheme: func() string { return theme },
		SetTheme: func(next string) error {
			theme = next
			return nil
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/api/settings",
		strings.NewReader(`{"theme":"pink"}`),
	)
	server.handlePutSettings(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if theme != "pink" {
		t.Fatalf("theme = %q, want pink", theme)
	}
}
