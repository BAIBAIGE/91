package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/mediaimport"
	"gopkg.in/yaml.v3"
)

const testAPIHash = "0123456789abcdef0123456789abcdef"

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
	i := NewIntegration(cat, manager, service.uploadDir, 1, t.TempDir())
	cfg := service.cfg
	cfg.APIID, cfg.APIHash, cfg.BotToken = 1234, testAPIHash, "123:private_token"
	return i, cfg
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
func acknowledgeSupervisor(t *testing.T, i *Integration, cfgRoot string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(i.controlDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Revision string `json:"revision"`
	}
	if err = json.Unmarshal(data, &projection); err != nil {
		t.Fatal(err)
	}
	status, _ := json.Marshal(map[string]any{"revision": projection.Revision, "state": "running", "updatedAt": time.Now().Unix()})
	if err = os.WriteFile(filepath.Join(cfgRoot, "panel-status.json"), status, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestIntegrationWaitsForSupervisorAndAppliesChanges(t *testing.T) {
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
	input.APIBaseURL = server.URL
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	if i.Status().State != "waiting_service" || i.Available() {
		t.Fatal("receiver started without supervisor acknowledgement")
	}
	projection, _ := os.ReadFile(filepath.Join(i.controlDir, "config.json"))
	if strings.Contains(string(projection), input.BotToken) {
		t.Fatal("Bot Token leaked to supervisor")
	}
	acknowledgeSupervisor(t, i, input.LocalFilesRoot)
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
	input.AllowedUserIDs = []int64{77}
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	sameProjection, _ := os.ReadFile(filepath.Join(i.controlDir, "config.json"))
	if string(sameProjection) != string(projection) {
		t.Fatal("whitelist edit unnecessarily restarted Bot API")
	}
	if i.session() == old || old.runCtx.Err() == nil || !i.Allowed(77) || i.Allowed(42) {
		t.Fatal("old receiver or whitelist survived configuration change")
	}
	input.APIHash = strings.Repeat("a", 32)
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	if i.session() != nil || i.Status().State != "waiting_service" {
		t.Fatal("accepted acknowledgement from previous API credentials")
	}
	input.Enabled = false
	saveTelegramYAML(t, i, input)
	i.reconcile(ctx)
	data, _ := os.ReadFile(filepath.Join(i.controlDir, "config.json"))
	if i.Status().State != "disabled" || !strings.Contains(string(data), `"enabled":false`) || strings.Contains(string(data), input.APIHash) {
		t.Fatal("disable did not stop receiver and clear published credentials")
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
	defer i.Shutdown(context.Background())
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(i.controlDir, "config.json")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if i.Status().State != "disabled" || i.session() != nil {
		t.Fatal("unconfigured integration started receiving")
	}
	data, err := os.ReadFile(filepath.Join(i.controlDir, "config.json"))
	if err != nil || !strings.Contains(string(data), `"enabled":false`) {
		t.Fatal("startup did not clear stale supervisor credentials")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = i.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
}
