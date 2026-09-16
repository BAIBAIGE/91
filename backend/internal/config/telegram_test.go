package config

import "testing"

func TestTelegramConfigValidation(t *testing.T) {
	for _, input := range []string{
		"telegram:\n  bot_token: secret\n",
		"telegram:\n  max_pending_jobs: -1\n",
		"telegram:\n  api_base_url: https://api.telegram.org\n",
		"telegram:\n  api_base_url: http://user:secret@localhost\n",
		"telegram:\n  allowed_user_ids: [-1]\n",
		"telegram:\n  local_files_root: relative\n",
		"telegram:\n  fetch_timeout_seconds: 1\n",
		"telegram:\n  upload_directory: ../outside\n",
		"telegram:\n  upload_directory: /absolute\n",
		"telegram:\n  upload_directory: 'Telegram\\videos'\n",
		"telegram:\n  upload_drive_id: local-upload\n",
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	cfg, err := Parse([]byte("telegram:\n  enabled: true\n  bot_token: 123:test_token\n  api_id: 1234\n  api_hash: \"0123456789abcdef0123456789abcdef\"\n  allowed_user_ids: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Telegram.MaxFileSizeBytes != 4<<30 || cfg.Telegram.UploadDriveID != "" || cfg.Telegram.UploadDirectory != "Telegram" {
		t.Fatalf("defaults: %+v", cfg.Telegram)
	}
}
