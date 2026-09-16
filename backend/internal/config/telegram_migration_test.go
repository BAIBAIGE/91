package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTelegramMigrationImportsMissingYAMLAndPreservesExplicitValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# keep comment\npreview:\n  enabled: false\ntelegram:\n  enabled: false\n  upload_directory: Existing\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := Telegram{Enabled: true, BotToken: "123:private_token", APIID: 1234, APIHash: "0123456789abcdef0123456789abcdef", AllowedUserIDs: []int64{42}, UploadDirectory: "Legacy"}
	if err = m.MigrateTelegramSettings(legacy); err != nil {
		t.Fatal(err)
	}
	cfg := m.TelegramSettings()
	if cfg.Enabled || cfg.UploadDirectory != "Existing" || cfg.BotToken != legacy.BotToken || cfg.APIHash != legacy.APIHash || cfg.APIID != legacy.APIID || cfg.AllowedUserIDs[0] != 42 {
		t.Fatal("migration lost settings or overwrote explicit YAML")
	}
	data, _, err := m.ReadYAML()
	if err != nil || !strings.Contains(string(data), "# keep comment") {
		t.Fatal("lost YAML comments", err)
	}
	if err = m.MigrateTelegramSettings(legacy); err != nil {
		t.Fatal(err)
	}
	again, _, _ := m.ReadYAML()
	if !bytes.Equal(data, again) {
		t.Fatal("migration is not idempotent")
	}
	cfg.AllowedUserIDs[0] = 999
	if m.TelegramSettings().AllowedUserIDs[0] != 42 {
		t.Fatal("snapshot is not isolated")
	}
}

func TestTelegramYAMLHotReloadAndInvalidUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	data, version, _ := m.ReadYAML()
	valid := []byte("telegram:\n  enabled: true\n  bot_token: 123:private_token\n  api_id: 1234\n  api_hash: '0123456789abcdef0123456789abcdef'\n")
	result, err := m.ReplaceYAML(valid, version)
	if err != nil || result.RestartRequired || !m.TelegramSettings().Enabled {
		t.Fatal("YAML did not apply live", err)
	}
	if _, err = m.ReplaceYAML(data, version); !errors.Is(err, ErrVersionConflict) {
		t.Fatal("accepted stale write", err)
	}
	invalid := []byte("telegram:\n  enabled: true\n  bot_token: invalid\n")
	if _, err = m.ReplaceYAML(invalid, result.Version); err == nil {
		t.Fatal("accepted invalid credentials")
	}
	disk, _, _ := m.ReadYAML()
	if !bytes.Equal(disk, valid) {
		t.Fatal("invalid write replaced config")
	}
	if err = os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Reload(); err == nil || !m.TelegramSettings().Enabled {
		t.Fatal("invalid external edit changed runtime")
	}
	if err = os.WriteFile(path, []byte("telegram:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := m.Reload(); err != nil || !changed || m.TelegramSettings().Enabled {
		t.Fatal("external edit did not apply", err)
	}
}

func TestTelegramMigrationDoesNotWriteInvalidLegacyCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# keep\n{}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.MigrateTelegramSettings(Telegram{Enabled: true, BotToken: "invalid"}); err == nil {
		t.Fatal("accepted invalid migration")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("failed migration modified file")
	}
}
