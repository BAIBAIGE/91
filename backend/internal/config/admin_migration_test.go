package config

import (
	"errors"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const legacyAdminConfig = `# retained comment
server:
  listen: "127.0.0.1:9192"
  admin:
    username: "source-owner"
    password: "source-secret"
  future_option: "keep-me"
storage:
  db_path: "./data/video-site.db"
`

func TestMigrateLegacyAdminCredentials(t *testing.T) {
	manager, path := newManagerForTest(t, legacyAdminConfig)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	migrate := func(username, password string) error {
		called++
		if username != "source-owner" || password != "source-secret" {
			t.Fatal("wrong legacy credentials")
		}
		return nil
	}
	changed, err := manager.MigrateLegacyAdminCredentials(migrate)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	written, _, err := manager.ReadYAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"source-owner", "source-secret", "admin:"} {
		if strings.Contains(string(written), removed) {
			t.Fatalf("legacy field %q remains", removed)
		}
	}
	if !strings.Contains(string(written), "# retained comment") {
		t.Fatal("comment was lost")
	}
	var document map[string]any
	if err := yaml.Unmarshal(written, &document); err != nil {
		t.Fatal(err)
	}
	server := document["server"].(map[string]any)
	if server["future_option"] != "keep-me" || server["listen"] != "127.0.0.1:9192" {
		t.Fatal("unrelated config changed")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatal("config permissions changed")
	}
	changed, err = manager.MigrateLegacyAdminCredentials(migrate)
	if err != nil || changed || called != 1 {
		t.Fatalf("second migration changed=%v calls=%d err=%v", changed, called, err)
	}
}

func TestMigrateLegacyAdminCredentialsPreservesFileOnImportFailure(t *testing.T) {
	manager, path := newManagerForTest(t, legacyAdminConfig)
	failure := errors.New("database unavailable")
	changed, err := manager.MigrateLegacyAdminCredentials(func(string, string) error { return failure })
	if changed || !errors.Is(err, failure) {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != legacyAdminConfig {
		t.Fatal("failed migration changed config")
	}
}

func TestManagerRejectsRetiredAdminConfig(t *testing.T) {
	manager, path := newManagerForTest(t, "server:\n  listen: ':8080'\n")
	if _, err := manager.ReplaceYAML([]byte(legacyAdminConfig), ""); !errors.Is(err, ErrAdminConfigRemoved) {
		t.Fatalf("replace error=%v", err)
	}
	if err := os.WriteFile(path, []byte(legacyAdminConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Reload(); !errors.Is(err, ErrAdminConfigRemoved) {
		t.Fatalf("reload error=%v", err)
	}
	if _, _, err := manager.ReadYAML(); !errors.Is(err, ErrAdminConfigRemoved) {
		t.Fatalf("read error=%v", err)
	}
}

func TestAdminConfigIsNotMarshaled(t *testing.T) {
	cfg, err := Parse([]byte(legacyAdminConfig))
	if err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAdminConfigRemoved(data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "source-secret") {
		t.Fatal("config retained plaintext password")
	}
}
