package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startCommandMenuService(t *testing.T, s *Service, server *httptest.Server) {
	t.Helper()
	s.cfg.APIBaseURL = server.URL
	s.setState("connecting", nil)
	s.Start(context.Background())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("receiver did not stop menu synchronization: %v", err)
		}
	})
}

func TestServiceRegistersPrivateCommandMenu(t *testing.T) {
	s, _ := testService(t)
	type request struct {
		method string
		body   map[string]any
	}
	requests := make(chan request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch method := filepath.Base(r.URL.Path); method {
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"test_bot"}}`)
		case "getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		case "getUpdates":
			<-r.Context().Done()
		case "setMyCommands", "setChatMenuButton":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Error("menu configuration must use JSON POST requests")
			}
			select {
			case requests <- request{method, body}:
			default:
				t.Error("repeated successful menu registration")
			}
			io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			t.Errorf("unexpected method: %s", method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	startCommandMenuService(t, s, server)

	want := []request{
		{"setMyCommands", map[string]any{
			"commands": []any{
				map[string]any{"command": "start", "description": "开始"},
				map[string]any{"command": "help", "description": "使用说明"},
				map[string]any{"command": "status", "description": "转存统计"},
				map[string]any{"command": "id", "description": "我的 Telegram ID"},
			},
			"scope": map[string]any{"type": "all_private_chats"},
		}},
		{"setChatMenuButton", map[string]any{"menu_button": map[string]any{"type": "commands"}}},
	}
	for _, expected := range want {
		select {
		case got := <-requests:
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("menu request = %+v, want %+v", got, expected)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("missing %s request after connecting", expected.method)
		}
	}
}

func TestCommandMenuRetriesWithoutBlockingReception(t *testing.T) {
	for _, tc := range []struct {
		method     string
		failure    string
		retryAfter time.Duration
	}{
		{"setMyCommands", `{"ok":false,"error_code":503}`, 10 * time.Second},
		{"setChatMenuButton", `{"ok":false,"error_code":429,"parameters":{"retry_after":11}}`, 11 * time.Second},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			s, cat := testService(t)
			menuStarted, releaseFailure := make(chan struct{}), make(chan struct{})
			importAccepted := make(chan struct{})
			var acceptedOnce sync.Once
			s.SetWake(func() { acceptedOnce.Do(func() { close(importAccepted) }) })
			registered := make(chan time.Time, 1)
			var attempts, polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				method := filepath.Base(r.URL.Path)
				if method == tc.method && attempts.Add(1) == 1 {
					close(menuStarted)
					select {
					case <-releaseFailure:
						io.WriteString(w, tc.failure)
					case <-r.Context().Done():
					}
					return
				}
				switch method {
				case "getMe":
					io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"test_bot"}}`)
				case "getWebhookInfo":
					io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
				case "getUpdates":
					if polls.Add(1) == 1 {
						select {
						case <-menuStarted:
							_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []update{videoUpdate(1, 42)}})
						case <-r.Context().Done():
						}
					} else {
						<-r.Context().Done()
					}
				case "setMyCommands":
					io.WriteString(w, `{"ok":true,"result":true}`)
				case "setChatMenuButton":
					io.WriteString(w, `{"ok":true,"result":true}`)
					select {
					case registered <- time.Now():
					default:
						t.Error("repeated successful menu registration")
					}
				case "sendMessage", "editMessageText":
					io.WriteString(w, `{"ok":true,"result":{"message_id":55}}`)
				default:
					t.Errorf("unexpected method: %s", method)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			startCommandMenuService(t, s, server)

			select {
			case <-importAccepted:
			case <-time.After(3 * time.Second):
				t.Fatal("menu registration blocked video reception")
			}
			jobs, err := cat.ListImportJobs(context.Background(), "telegram", "", 0, 30)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("video was not queued during menu registration: jobs=%v error=%v", jobs, err)
			}
			failedAt := time.Now()
			close(releaseFailure)
			select {
			case completedAt := <-registered:
				if completedAt.Sub(failedAt) < tc.retryAfter {
					t.Fatal("menu retry ignored backoff or Telegram retry_after")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("menu registration did not recover without reconnecting")
			}
			if status := s.Status(); status.State != "connected" || status.Error != "" {
				t.Fatalf("menu failure changed receiver availability: %+v", status)
			}
			if attempts.Load() != 2 {
				t.Fatalf("menu attempts = %d, want 2", attempts.Load())
			}
		})
	}
}

func TestCommandMenuShutdownCancelsRequest(t *testing.T) {
	s, _ := testService(t)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch filepath.Base(r.URL.Path) {
		case "getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"test_bot"}}`)
		case "getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		case "getUpdates":
			<-r.Context().Done()
		case "setMyCommands":
			close(started)
			<-r.Context().Done()
		default:
			t.Errorf("unexpected method: %s", filepath.Base(r.URL.Path))
		}
	}))
	t.Cleanup(server.Close)
	startCommandMenuService(t, s, server)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("menu synchronization did not start")
	}
	// Cleanup must cancel the in-flight menu request before closing the server.
}
