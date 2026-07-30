package config

import (
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
