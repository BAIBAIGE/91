package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeTelegramCompose(t *testing.T, directory, mounts string, website string) string {
	t.Helper()
	filename := filepath.Join(directory, "telegram.yml")
	body := "services:\n  telegram-bot-api:\n    environment:\n      TELEGRAM_API_HASH: '0123456789abcdef0123456789abcdef'\n      TELEGRAM_HTTP_PORT: '7878'\n    ports:\n      - '127.0.0.1:7878:7878'\n    volumes:\n" + mounts + website
	if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestTelegramComposeResolvesNativeAndContainerStorage(t *testing.T) {
	for _, tc := range []struct {
		name, mounts, website, want string
	}{
		{"native bind", "      - /nzb/tg:/var/lib/telegram-bot-api\n", "", "/nzb/tg"},
		{"native long bind", "      - type: bind\n        source: /nzb/tg\n        target: /var/lib/telegram-bot-api\n", "", "/nzb/tg"},
		{"relative bind", "      - ./tg:/var/lib/telegram-bot-api\n", "", "relative"},
		{"container volume", "      - telegram-cache:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - telegram-cache:/site/tg\n      - ./telegram.yml:/opt/site/telegram.yml:ro\n", "/site/tg"},
		{"container bind", "      - /nzb/tg:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - /nzb/tg:/site/tg\n", "/site/tg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			filename := writeTelegramCompose(t, directory, tc.mounts, tc.website)
			got, err := readTelegramCompose(filename)
			want := tc.want
			if want == "relative" {
				want = filepath.Join(directory, "tg")
			}
			if err != nil || got.apiRoot != telegramDataMount || got.localRoot != want {
				t.Fatalf("storage=%+v err=%v, want local=%q", got, err, want)
			}
		})
	}
}

func TestTelegramComposeAcceptsShippedDeploymentFiles(t *testing.T) {
	directory, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]struct{ endpoint, localRoot string }{
		"tg-docker-compose.yml": {"http://telegram-bot-api:7878", telegramDataMount},
		"telegram.example.yml":  {"http://127.0.0.1:7878", filepath.Join(directory, "data", "telegram")},
	} {
		got, err := readTelegramCompose(filepath.Join(directory, name))
		if err != nil || got.apiBaseURL != want.endpoint || got.apiRoot != telegramDataMount || got.localRoot != want.localRoot {
			t.Fatalf("%s: storage=%+v error=%v", name, got, err)
		}
	}
}

func TestTelegramComposeRejectsMissingAmbiguousAndUnsupportedMounts(t *testing.T) {
	for _, tc := range []struct{ name, mounts, website string }{
		{"missing data mount", "      - /nzb/tg:/unrelated\n", ""},
		{"native named volume", "      - telegram-cache:/var/lib/telegram-bot-api\n", ""},
		{"anonymous volume", "      - /var/lib/telegram-bot-api\n", ""},
		{"read only", "      - /nzb/tg:/var/lib/telegram-bot-api:ro\n", ""},
		{"long read only", "      - type: bind\n        source: /nzb/tg\n        target: /var/lib/telegram-bot-api\n        read_only: true\n", ""},
		{"duplicate target", "      - /nzb/tg:/var/lib/telegram-bot-api\n      - /other:/var/lib/telegram-bot-api\n", ""},
		{"nested data", "      - /nzb/tg:/var/lib/telegram-bot-api\n      - /other:/var/lib/telegram-bot-api/library\n", ""},
		{"unresolved variable", "      - ${TG_DATA_DIR}:/var/lib/telegram-bot-api\n", ""},
		{"home expansion", "      - type: bind\n        source: ~/tg\n        target: /var/lib/telegram-bot-api\n", ""},
		{"wrong peer", "      - telegram-cache:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - other-cache:/site/tg\n"},
		{"ambiguous peer", "      - telegram-cache:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - telegram-cache:/site/tg\n      - telegram-cache:/other\n"},
		{"read only peer", "      - telegram-cache:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - telegram-cache:/site/tg:ro\n"},
		{"nested peer", "      - telegram-cache:/var/lib/telegram-bot-api\n", "  video-site-91:\n    volumes:\n      - telegram-cache:/site/tg\n      - other:/site/tg/library\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filename := writeTelegramCompose(t, t.TempDir(), tc.mounts, tc.website)
			if got, err := readTelegramCompose(filename); err == nil || got != (telegramDeployment{}) {
				t.Fatalf("accepted invalid deployment: %+v %v", got, err)
			}
		})
	}
	for _, data := range []string{"services: [", "services: {}"} {
		filename := filepath.Join(t.TempDir(), "telegram.yml")
		if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTelegramCompose(filename); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestTelegramDeploymentFollowsComposeAfterRestartAndStaysOutOfPanelSettings(t *testing.T) {
	directory := t.TempDir()
	compose := writeTelegramCompose(t, directory, "      - /old/tg:/var/lib/telegram-bot-api\n", "")
	configPath := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(configPath, []byte("telegram: {bot_token: '123:test', api_base_url: 'http://old-api:9999'}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(configPath)
	if err != nil || m.TelegramDeploymentError() != nil {
		t.Fatalf("initial deployment: %v", err)
	}
	initial := m.TelegramSettings()
	if initial.LocalFilesRoot != "/old/tg" || initial.APIBaseURL != "http://127.0.0.1:7878" {
		t.Fatalf("initial deployment = %+v", initial)
	}
	if changed, err := m.SyncTemplate(); err != nil || !changed {
		t.Fatalf("migrate old endpoint: changed=%v err=%v", changed, err)
	}
	if data, _, err := m.ReadYAML(); err != nil || strings.Contains(string(data), "api_base_url") {
		t.Fatalf("old endpoint remains in panel YAML: %v", err)
	}
	writeTelegramCompose(t, directory, "      - /nzb/tg:/var/lib/telegram-bot-api\n", "")
	data, err := os.ReadFile(compose)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compose, []byte(strings.ReplaceAll(string(data), "127.0.0.1:7878:7878", "127.0.0.1:8888:7878")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReplaceYAML([]byte("telegram: {bot_token: '123:new'}\n"), ""); err != nil {
		t.Fatal(err)
	}
	if got := m.TelegramSettings(); got.LocalFilesRoot != initial.LocalFilesRoot || got.APIBaseURL != initial.APIBaseURL {
		t.Fatal("panel edit reloaded deployment while services were running")
	}
	restarted, err := NewManager(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg := restarted.TelegramSettings()
	if cfg.APIFilesRoot != telegramDataMount || cfg.LocalFilesRoot != "/nzb/tg" || cfg.APIBaseURL != "http://127.0.0.1:8888" || cfg.BotToken != "123:new" {
		t.Fatalf("restarted settings = %+v", cfg)
	}
	for _, encode := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		data, err := encode(cfg)
		if err != nil || strings.Contains(string(data), "/nzb/tg") || strings.Contains(string(data), telegramDataMount) {
			t.Fatalf("runtime storage exposed: %s %v", data, err)
		}
	}
	if data, err := yaml.Marshal(cfg); err != nil || strings.Contains(string(data), cfg.APIBaseURL) {
		t.Fatalf("runtime endpoint exposed in YAML: %s %v", data, err)
	}
	for _, key := range []string{"api_base_url", "api_files_root", "local_files_root"} {
		if _, err := restarted.ReplaceYAML([]byte("telegram: {"+key+": /override}"), ""); err == nil {
			t.Fatalf("accepted panel override %s", key)
		}
	}
	if err := os.Remove(compose); err != nil {
		t.Fatal(err)
	}
	missing, err := NewManager(configPath)
	if err != nil || missing.TelegramDeploymentError() == nil || missing.TelegramSettings().LocalFilesRoot != "" || missing.TelegramSettings().APIBaseURL != "" {
		t.Fatalf("missing deployment silently fell back: %v", err)
	}
}
