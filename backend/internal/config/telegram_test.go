package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTelegramConfigValidation(t *testing.T) {
	for _, input := range []string{
		"telegram:\n  bot_token: secret\n",
		"telegram:\n  enabled: true\n",
		"telegram:\n  max_pending_jobs: -1\n",
		"telegram:\n  api_base_url: https://api.telegram.org\n",
		"telegram:\n  api_base_url: http://user:secret@localhost\n",
		"telegram:\n  allowed_user_ids: [-1]\n",
		"telegram:\n  fetch_timeout_seconds: 1\n",
		"telegram:\n  upload_directory: ../outside\n",
		"telegram:\n  upload_directory: /absolute\n",
		"telegram:\n  upload_directory: 'Telegram\\videos'\n",
		"telegram:\n  upload_drive_id: local-upload\n",
		"telegram:\n  upload_proxy: ftp://proxy.example:21\n",
		"telegram:\n  upload_proxy: proxy.example:7890\n",
		"telegram:\n  upload_proxy: http://\n",
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	cfg, err := Parse([]byte("telegram:\n  enabled: true\n  bot_token: 123:test_token\n  allowed_user_ids: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.MaxFileSizeBytes != 4<<30 || cfg.Telegram.UploadDriveID != "" || cfg.Telegram.UploadDirectory != "Telegram" || cfg.Telegram.UploadProxy != "" {
		t.Fatalf("defaults: %+v", cfg.Telegram)
	}
}

func TestTelegramUploadProxyValidationAndRedaction(t *testing.T) {
	for _, proxy := range []string{"", "http://proxy.example:7890", "https://user:private_password@proxy.example:443", "socks5://proxy.example:1080", "socks5h://proxy.example:1080"} {
		cfg, err := Parse([]byte("telegram:\n  upload_proxy: '  " + proxy + "  '\n"))
		if err != nil || cfg.Telegram.UploadProxy != proxy {
			t.Fatalf("proxy did not normalize: %v", err)
		}
		data, err := json.Marshal(cfg.Telegram)
		if err != nil || strings.Contains(string(data), "proxy") || strings.Contains(string(data), "private_password") {
			t.Fatal("status JSON exposed upload proxy", err)
		}
	}
	if _, err := Parse([]byte("telegram:\n  upload_proxy: 'http://user:private_password@proxy.example:invalid'\n")); err == nil || strings.Contains(err.Error(), "private_password") {
		t.Fatalf("expected sanitized validation error: %v", err)
	}
}
