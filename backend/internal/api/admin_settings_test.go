package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetSettingsReturnsDatabasePreferencesOnly(t *testing.T) {
	server := &AdminServer{GetTheme: func() string { return "sky" }}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil)
	server.handleGetSettings(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response settingsDTO
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Theme != "sky" {
		t.Fatalf("theme = %q, want sky", response.Theme)
	}
	if strings.Contains(recorder.Body.String(), "nightlyStartTime") {
		t.Fatalf("config.yaml fields leaked into settings endpoint: %s", recorder.Body.String())
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
