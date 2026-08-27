package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRequiresAdminSetup(t *testing.T) {
	if !RequiresAdminSetup(&Config{Server: Server{Admin: Admin{Username: DefaultAdminUsername, Password: DefaultAdminPassword}}}) {
		t.Fatal("default admin credentials should require setup")
	}
	if RequiresAdminSetup(&Config{Server: Server{Admin: Admin{Username: "owner", Password: "secret123"}}}) {
		t.Fatal("custom admin credentials should not require setup")
	}
}

func TestWriteAdminCredentialsUpdatesConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
server:
  listen: "127.0.0.1:9192"
  admin:
    username: "admin"
    password: "admin123"
storage:
  db_path: "./data/video-site.db"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if err := WriteAdminCredentials(path, "owner", "new-secret"); err != nil {
		t.Fatalf("write admin credentials: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Server.Admin.Username != "owner" {
		t.Fatalf("username = %q, want owner", cfg.Server.Admin.Username)
	}
	if cfg.Server.Admin.Password != "new-secret" {
		t.Fatalf("password = %q, want new-secret", cfg.Server.Admin.Password)
	}
	if cfg.Server.Listen != "127.0.0.1:9192" {
		t.Fatalf("listen = %q, want preserved value", cfg.Server.Listen)
	}
	if cfg.Storage.DBPath != "./data/video-site.db" {
		t.Fatalf("db path = %q, want preserved value", cfg.Storage.DBPath)
	}
}

func TestRedactAdminCredentialsPreservesOtherConfig(t *testing.T) {
	source := []byte(`# retained comment
server:
  listen: "127.0.0.1:9192"
  admin:
    username: "source-owner"
    password: "source-secret"
  future_option: "keep-me"
storage:
  db_path: "./data/video-site.db"
`)
	redacted, err := RedactAdminCredentials(source)
	if err != nil {
		t.Fatalf("redact admin credentials: %v", err)
	}
	if strings.Contains(string(redacted), "source-owner") ||
		strings.Contains(string(redacted), "source-secret") {
		t.Fatalf("redacted config still contains administrator credentials:\n%s", redacted)
	}
	if !strings.Contains(string(redacted), "# retained comment") {
		t.Fatalf("redaction discarded unrelated config:\n%s", redacted)
	}
	var document map[string]any
	if err := yaml.Unmarshal(redacted, &document); err != nil {
		t.Fatalf("parse redacted config: %v", err)
	}
	server, ok := document["server"].(map[string]any)
	if !ok {
		t.Fatalf("redacted server config = %#v", document["server"])
	}
	admin, ok := server["admin"].(map[string]any)
	if !ok || admin["username"] != "" || admin["password"] != "" {
		t.Fatalf("redacted administrator config = %#v", server["admin"])
	}
	if server["future_option"] != "keep-me" {
		t.Fatalf("redacted future server option = %#v", server["future_option"])
	}
}

func TestLoadDefaultScannerVideoExtensionsIncludeSTRM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !hasVideoExtension(cfg.Scanner.VideoExtensions, ".strm") {
		t.Fatalf("video extensions = %#v, want .strm", cfg.Scanner.VideoExtensions)
	}
}

func TestExampleConfigOmitsDatabaseManagedDriveDefinitions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	text := string(data)
	for _, obsolete := range []string{"drives:", "# 盘列表", "my-onedrive", "my-webdav"} {
		if strings.Contains(text, obsolete) {
			t.Fatalf("example config still exposes database-managed drive definition %q", obsolete)
		}
	}
}

func TestResolveStoragePathsUsesStartupDirectoryWithoutMutatingConfig(t *testing.T) {
	baseDir := t.TempDir()
	storage := Storage{
		DBPath:          "./data/video-site.db",
		LocalPreviewDir: "./data/previews",
	}

	resolved, err := ResolveStoragePaths(storage, baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DBPath != filepath.Join(baseDir, "data", "video-site.db") {
		t.Fatalf("resolved db path = %q", resolved.DBPath)
	}
	if resolved.LocalPreviewDir != filepath.Join(baseDir, "data", "previews") {
		t.Fatalf("resolved preview path = %q", resolved.LocalPreviewDir)
	}
	if storage.DBPath != "./data/video-site.db" ||
		storage.LocalPreviewDir != "./data/previews" {
		t.Fatalf("source storage config was mutated: %+v", storage)
	}
}

func TestLoggingDefaultsAndCanBeDisabled(t *testing.T) {
	defaults, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.Logging.IsFileEnabled() || defaults.Logging.Directory != "./data/logs" ||
		defaults.Logging.MaxFileSizeMB != 10 || defaults.Logging.MaxTotalSizeMB != 50 {
		t.Fatalf("logging defaults = %+v", defaults.Logging)
	}

	disabled, err := Parse([]byte("logging:\n  file_enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Logging.IsFileEnabled() {
		t.Fatal("file logging should be disabled explicitly")
	}
}

func TestResolveLoggingPathsUsesStartupDirectoryWithoutMutatingConfig(t *testing.T) {
	baseDir := t.TempDir()
	logging := Logging{Directory: "./data/logs", MaxFileSizeMB: 10, MaxTotalSizeMB: 200}

	resolved, err := ResolveLoggingPaths(logging, baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Directory != filepath.Join(baseDir, "data", "logs") {
		t.Fatalf("resolved logging path = %q", resolved.Directory)
	}
	if logging.Directory != "./data/logs" {
		t.Fatalf("source logging config was mutated: %+v", logging)
	}
}

func TestParseRejectsInvalidLoggingSizeLimits(t *testing.T) {
	tests := []string{
		"logging:\n  max_file_size_mb: -1\n",
		"logging:\n  max_file_size_mb: 20\n  max_total_size_mb: 10\n",
		"logging:\n  max_total_size_mb: 10241\n",
	}
	for _, data := range tests {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("Parse(%q) succeeded, want validation error", data)
		}
	}
}

func TestLoadLegacyDefaultScannerVideoExtensionsIncludeSTRM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
scanner:
  video_extensions: [".mp4", ".mkv", ".mov", ".webm", ".avi"]
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !hasVideoExtension(cfg.Scanner.VideoExtensions, ".strm") {
		t.Fatalf("video extensions = %#v, want .strm appended for legacy default list", cfg.Scanner.VideoExtensions)
	}
}

func TestLoadCustomScannerVideoExtensionsArePreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
scanner:
  video_extensions: [".mp4"]
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Scanner.VideoExtensions) != 1 || cfg.Scanner.VideoExtensions[0] != ".mp4" {
		t.Fatalf("video extensions = %#v, want custom list preserved", cfg.Scanner.VideoExtensions)
	}
}

func TestPreviewConcurrency(t *testing.T) {
	cfg, err := Parse([]byte(`
preview:
  concurrency: 3
`))
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if got := cfg.Preview.Concurrency; got != 3 {
		t.Fatalf("preview concurrency = %d, want 3", got)
	}
	maximum, err := Parse([]byte("preview:\n  concurrency: 5\n"))
	if err != nil {
		t.Fatalf("parse maximum preview concurrency: %v", err)
	}
	if got := maximum.Preview.Concurrency; got != MaxPreviewConcurrency {
		t.Fatalf("maximum preview concurrency = %d, want %d", got, MaxPreviewConcurrency)
	}

	defaults, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if got := defaults.Preview.Concurrency; got != DefaultPreviewConcurrency {
		t.Fatalf("default preview concurrency = %d, want %d", got, DefaultPreviewConcurrency)
	}
}

func TestPreviewConcurrencyRejectsInvalidValue(t *testing.T) {
	_, err := Parse([]byte(`
preview:
  concurrency: 6
`))
	if err == nil || !strings.Contains(err.Error(), "must be between 1 and 5") {
		t.Fatalf("parse error = %v, want concurrency validation error", err)
	}
}

func TestLoadDefaultNightlyCronHour(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Nightly.CronHour != 1 {
		t.Fatalf("nightly cron hour = %d, want 1", cfg.Nightly.CronHour)
	}
	if cfg.Nightly.StartTime != DefaultNightlyStartTime {
		t.Fatalf("nightly start time = %q, want %q", cfg.Nightly.StartTime, DefaultNightlyStartTime)
	}
	if cfg.Nightly.Timezone != DefaultNightlyTimezone {
		t.Fatalf("nightly timezone = %q, want %q", cfg.Nightly.Timezone, DefaultNightlyTimezone)
	}
	if cfg.Nightly.Disabled != DefaultNightlyDisabled {
		t.Fatalf("nightly disabled = %v, want %v", cfg.Nightly.Disabled, DefaultNightlyDisabled)
	}
}

func TestParseNightlyStartTime(t *testing.T) {
	cfg, err := Parse([]byte("nightly:\n  start_time: \"00:15\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nightly.StartTime != "00:15" {
		t.Fatalf("start time = %q", cfg.Nightly.StartTime)
	}
	if cfg.Nightly.Timezone != DefaultNightlyTimezone {
		t.Fatalf("default timezone = %q", cfg.Nightly.Timezone)
	}
}

func TestParseNightlyTimezone(t *testing.T) {
	cfg, err := Parse([]byte("nightly:\n  timezone: Asia/Shanghai\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Nightly.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q", cfg.Nightly.Timezone)
	}
}

func TestParseNightlyDisabled(t *testing.T) {
	disabled, err := Parse([]byte("nightly:\n  disabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !disabled.Nightly.Disabled {
		t.Fatal("explicitly disabled nightly schedule was enabled")
	}

	enabled, err := Parse([]byte("nightly:\n  disabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Nightly.Disabled {
		t.Fatal("explicitly enabled nightly schedule was disabled")
	}

	if _, err := Parse([]byte("nightly:\n  disabled: not-a-boolean\n")); err == nil {
		t.Fatal("non-boolean nightly.disabled was accepted")
	}
}

func TestParseBuiltinTagConfiguration(t *testing.T) {
	defaults, err := Parse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.Tags.IsBuiltinPackEnabled() {
		t.Fatal("built-in tags should default to enabled")
	}

	disabled, err := Parse([]byte("tags:\n  builtin_pack_enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Tags.IsBuiltinPackEnabled() {
		t.Fatal("explicitly disabled built-in tags were enabled")
	}

	if _, err := Parse([]byte("tags:\n  builtin_pack_enabled: not-a-boolean\n")); err == nil {
		t.Fatal("non-boolean built-in tag configuration was accepted")
	}
}

func TestParseRejectsInvalidNightlyStartTime(t *testing.T) {
	_, err := Parse([]byte("nightly:\n  start_time: \"24:00\"\n"))
	if !errors.Is(err, ErrInvalidNightlyStartTime) {
		t.Fatalf("error = %v, want ErrInvalidNightlyStartTime", err)
	}
}

func TestParseRejectsInvalidNightlyTimezone(t *testing.T) {
	for _, timezone := range []string{"Local", "Mars/Olympus"} {
		t.Run(timezone, func(t *testing.T) {
			_, err := Parse([]byte("nightly:\n  timezone: \"" + timezone + "\"\n"))
			if !errors.Is(err, ErrInvalidNightlyTimezone) {
				t.Fatalf("error = %v, want ErrInvalidNightlyTimezone", err)
			}
		})
	}
}

func TestLoadForcedRelayDefaultsEnabledAndCanBeDisabled(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if !cfg.Proxy.AllowsForcedRelay() {
			t.Fatal("forced relay should remain enabled by default")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("proxy:\n  allow_forced_relay: false\n"), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.Proxy.AllowsForcedRelay() {
			t.Fatal("forced relay should honor an explicit false value")
		}
	})
}

func TestLoadInvalidNightlyCronHourFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`
nightly:
  cron_hour: 25
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Nightly.CronHour != 1 {
		t.Fatalf("nightly cron hour = %d, want fallback 1", cfg.Nightly.CronHour)
	}
}

func TestLoadRemoteUploadDefaultsAndOverrides(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.RemoteUpload.DiskReserveBytes != 1073741824 {
			t.Fatalf("disk reserve = %d", cfg.RemoteUpload.DiskReserveBytes)
		}
		if cfg.RemoteUpload.IdleTimeoutSeconds != 120 {
			t.Fatalf("idle timeout = %d", cfg.RemoteUpload.IdleTimeoutSeconds)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(`
remote_upload:
  disk_reserve_bytes: 2147483648
  idle_timeout_seconds: 240
`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.RemoteUpload.DiskReserveBytes != 2147483648 ||
			cfg.RemoteUpload.IdleTimeoutSeconds != 240 {
			t.Fatalf("remote upload config = %#v", cfg.RemoteUpload)
		}
	})
}

func hasVideoExtension(exts []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, ext := range exts {
		if strings.ToLower(strings.TrimSpace(ext)) == want {
			return true
		}
	}
	return false
}
