package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/mediaimport"
	"gopkg.in/yaml.v3"
)

func integrationFixture(t *testing.T) (*Integration, config.Telegram) {
	t.Helper()
	service, cat := testService(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	loadIntegrationDeployment(t, manager, service.cfg.LocalFilesRoot, "http://127.0.0.1:7878")
	i := NewIntegration(cat, manager, service.uploadDir, 1)
	cfg := service.cfg
	cfg.APIFilesRoot = manager.TelegramSettings().APIFilesRoot
	cfg.BotToken = "123:private_token"
	return i, cfg
}

func loadIntegrationDeployment(t *testing.T, manager *config.Manager, storage, endpoint string) {
	t.Helper()
	address, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	compose, err := yaml.Marshal(map[string]any{"services": map[string]any{
		"telegram-bot-api": map[string]any{
			"environment": map[string]string{"TELEGRAM_HTTP_PORT": "7878"},
			"ports": []any{map[string]any{
				"host_ip": address.Hostname(), "published": address.Port(), "target": 7878,
			}},
			"volumes": []any{map[string]any{
				"type": "bind", "source": storage, "target": "/var/lib/telegram-bot-api",
			}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "telegram.yml")
	if err := os.WriteFile(filename, compose, 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.LoadTelegramCompose(filename); err != nil {
		t.Fatal(err)
	}
}

func saveTelegramYAML(t *testing.T, i *Integration, cfg config.Telegram) {
	t.Helper()
	raw, err := yaml.Marshal(map[string]any{"telegram": cfg})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := i.configManager.ReplaceYAML(raw, ""); err != nil || result.RestartRequired {
		t.Fatalf("save live YAML: restart=%v error=%v", result.RestartRequired, err)
	}
}
func TestIntegrationConnectsToStandardAPIAndAppliesChanges(t *testing.T) {
	i, input := integrationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer i.stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"panel_bot"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			io.WriteString(w, `{"ok":true,"result":[]}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer server.Close()
	loadIntegrationDeployment(t, i.configManager, input.LocalFilesRoot, server.URL)
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	old := i.session()
	if old == nil {
		t.Fatal("receiver not started")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !i.Available() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !i.Available() {
		t.Fatalf("not connected: %+v", i.Status())
	}
	raw, version, err := i.configManager.ReadYAML()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = i.configManager.ReplaceYAML(append(raw, []byte("preview:\n  enabled: false\n")...), version); err != nil {
		t.Fatal(err)
	}
	i.reconcile(ctx)
	if i.session() != old {
		t.Fatal("unrelated YAML edit rebuilt Telegram session")
	}
	input.UploadDriveID = "cloud"
	input.UploadDirectory = "Telegram/videos"
	for _, proxy := range []string{"http://user:private_proxy_password@proxy.example:7890", "socks5h://proxy.example:1080", ""} {
		input.UploadProxy = proxy
		saveTelegramYAML(t, i, input)
		i.reconcile(ctx)
		if i.session() != old || old.runCtx.Err() != nil {
			t.Fatal("transfer settings interrupted Telegram receiver")
		}
		status := i.Status()
		if status.Config.UploadDriveID != input.UploadDriveID || status.Config.UploadDirectory != input.UploadDirectory || status.Config.UploadProxy != proxy {
			t.Fatal("transfer status retained stale configuration")
		}
		raw, err := json.Marshal(status)
		if err != nil || strings.Contains(string(raw), "proxy") || strings.Contains(string(raw), "private_proxy_password") {
			t.Fatal("status exposed proxy credentials", err)
		}
	}
	input.AllowedUserIDs = []int64{77}
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	if i.session() == old || old.runCtx.Err() == nil || !i.Allowed(77) || i.Allowed(42) {
		t.Fatal("old receiver or whitelist survived configuration change")
	}
	input.Enabled = false
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	if i.Status().State != "disabled" || i.session() != nil {
		t.Fatal("disable did not stop receiver")
	}
	if name, err := i.Test(ctx); err != nil || name != "panel_bot" {
		t.Fatalf("disabled integration could not test the independent API: %s %v", name, err)
	}
}
func TestConfigurationChangeRetriesActiveDownload(t *testing.T) {
	i, input := integrationFixture(t)
	ctx := context.Background()
	called := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(called) })
		<-r.Context().Done()
	}))
	defer server.Close()
	input.APIBaseURL = server.URL
	s := New(input, input.BotToken, i.cat, i.uploadDir, 1)
	s.runCtx, s.cancel = context.WithCancel(ctx)
	s.botID = 123
	s.status.State = "connected"
	s.client = &client{base: server.URL, token: input.BotToken, http: server.Client()}
	i.active = s
	if err := s.accept(ctx, videoUpdate(1, 42)); err != nil {
		t.Fatal(err)
	}
	jobs, err := i.cat.ListImportJobs(ctx, "telegram", "", 0, 30)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := i.Fetch(ctx, jobs[0], func(string, int64, int64) error { return nil })
		result <- err
	}()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	i.stop()
	select {
	case err := <-result:
		var retry *mediaimport.SourceError
		if !errors.As(err, &retry) || !retry.WaitForAvailability {
			t.Fatalf("configuration change did not retry download: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old download did not stop")
	}
}

func TestFreshSettingsHaveUsableDefaults(t *testing.T) {
	i, _ := integrationFixture(t)
	cfg := i.configManager.TelegramSettings()
	if cfg.AllowedUserIDs == nil || cfg.APIBaseURL == "" || cfg.MaxFileSizeBytes == 0 {
		t.Fatal("invalid default YAML settings")
	}
}

func TestIntegrationStartWithoutCredentialsAndShutdown(t *testing.T) {
	i, _ := integrationFixture(t)
	i.Start(context.Background())
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := i.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if i.Status().State != "disabled" || i.session() != nil {
		t.Fatal("unconfigured integration started receiving")
	}
}

func TestIntegrationReportsMissingComposeWithoutStartingReceiver(t *testing.T) {
	i, cfg := integrationFixture(t)
	if err := i.configManager.LoadTelegramCompose(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Fatal("missing Compose accepted")
	}
	saveTelegramYAML(t, i, cfg)
	i.reconcile(context.Background())
	defer i.stop()
	if status := i.Status(); status.State != "error" || !strings.Contains(status.Error, "Compose") || i.session() != nil {
		t.Fatalf("deployment error not reported: %+v", status)
	}
	if _, err := i.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "Compose") {
		t.Fatalf("probe ignored deployment error: %v", err)
	}
}

func waitForIntegration(t *testing.T, i *Integration, state string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if i.Status().State == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %s, got %+v", state, i.Status())
}

func TestIntegrationRecoversFromExternalAPIOutage(t *testing.T) {
	i, cfg := integrationFixture(t)
	if err := i.cat.AcceptTelegramUpdate(context.Background(), catalog.TelegramReceipt{
		BotID: 123, UpdateID: 100, ChatID: 42, MessageID: 100, Response: "__ignore__",
	}, nil, "", "", 100); err != nil {
		t.Fatal(err)
	}
	var offline atomic.Bool
	offline.Store(true)
	var polls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			var input struct{ Offset int64 }
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Offset != 101 {
				t.Errorf("lost persisted offset across outage: offset=%d error=%v", input.Offset, err)
			}
		} else {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		if offline.Load() {
			io.WriteString(w, `{"ok":false,"error_code":503}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"external_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			polls.Add(1)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			io.WriteString(w, `{"ok":true,"result":[]}`)
		default:
			io.WriteString(w, `{"ok":true,"result":{}}`)
		}
	}))
	defer server.Close()
	defer i.stop()
	loadIntegrationDeployment(t, i.configManager, cfg.LocalFilesRoot, server.URL)
	saveTelegramYAML(t, i, cfg)
	i.reconcile(context.Background())
	session := i.session()
	waitForIntegration(t, i, "error")
	offline.Store(false)
	waitForIntegration(t, i, "connected")
	deadline := time.Now().Add(time.Second)
	for polls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if polls.Load() == 0 {
		t.Fatal("receiver did not resume polling")
	}
	offline.Store(true)
	waitForIntegration(t, i, "error")
	offline.Store(false)
	waitForIntegration(t, i, "connected")
	if i.session() != session {
		t.Fatal("outage recovery required rebuilding the integration")
	}
}

func TestIntegrationProbeUsesSavedConfigWithoutStartingReceiver(t *testing.T) {
	i, cfg := integrationFixture(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/bot123:private_token/getMe":
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"external_bot"}}`)
		case "/bot123:private_token/getWebhookInfo":
			io.WriteString(w, `{"ok":true,"result":{"url":""}}`)
		default:
			t.Errorf("connection probe invoked %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	cfg.Enabled = false
	loadIntegrationDeployment(t, i.configManager, cfg.LocalFilesRoot, server.URL)
	saveTelegramYAML(t, i, cfg)
	name, err := i.Test(context.Background())
	if err != nil || name != "external_bot" || i.session() != nil || requests.Load() != 2 {
		t.Fatalf("probe unexpectedly requires or starts a receiver: name=%s error=%v requests=%d", name, err, requests.Load())
	}
}

func TestIntegrationSwitchesEndpointAndBotAfterCancelingOldPolling(t *testing.T) {
	i, cfg := integrationFixture(t)
	started, canceled := make(chan struct{}), make(chan struct{})
	var startOnce, cancelOnce sync.Once
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":123,"username":"old_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			startOnce.Do(func() { close(started) })
			<-r.Context().Done()
			cancelOnce.Do(func() { close(canceled) })
		default:
			io.WriteString(w, `{"ok":true,"result":{}}`)
		}
	}))
	defer oldServer.Close()
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if !strings.HasPrefix(r.URL.Path, "/bot456:new_token/") {
			t.Errorf("new endpoint received old bot credentials: %s", r.URL.Path)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			io.WriteString(w, `{"ok":true,"result":{"id":456,"username":"new_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			<-r.Context().Done()
		default:
			io.WriteString(w, `{"ok":true,"result":{}}`)
		}
	}))
	defer newServer.Close()
	defer i.stop()
	loadIntegrationDeployment(t, i.configManager, cfg.LocalFilesRoot, oldServer.URL)
	saveTelegramYAML(t, i, cfg)
	i.reconcile(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old receiver did not start")
	}
	old := i.session()
	loadIntegrationDeployment(t, i.configManager, cfg.LocalFilesRoot, newServer.URL)
	cfg.BotToken = "456:new_token"
	saveTelegramYAML(t, i, cfg)
	if name, err := i.Test(context.Background()); err != nil || name != "new_bot" {
		t.Fatalf("probe used the old session rather than saved settings: %s %v", name, err)
	}
	i.reconcile(context.Background())
	if old.runCtx.Err() == nil || i.session() == old {
		t.Fatal("old receiver survived endpoint change")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("old long poll was not canceled")
	}
	waitForIntegration(t, i, "connected")
	if i.BotID() != 456 {
		t.Fatal("new connection retained the old bot identity")
	}
}
