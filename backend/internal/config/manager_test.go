package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newManagerForTest(t *testing.T, source string) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(source), 0o640); err != nil {
		t.Fatalf("write config: %v", err)
	}
	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return manager, path
}

func TestManagerMigratesLegacyValuesIntoYAMLWithoutDiscardingUnknownNodes(t *testing.T) {
	manager, path := newManagerForTest(t, `# retained root comment
nightly:
  # legacy schedule comment
  cron_hour: 3
dedupe:
  duplicate_review_enabled: false
future_section:
  keep_me: true
`)
	start := "04:25"
	builtinTagsEnabled := false
	changed, err := manager.MigrateLegacyRuntimeSettings(LegacyRuntimeSettings{
		NightlyStartTime:   &start,
		BuiltinTagsEnabled: &builtinTagsEnabled,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !changed {
		t.Fatal("legacy document was not migrated")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, retained := range []string{"# retained root comment", "future_section:", "keep_me: true"} {
		if !strings.Contains(text, retained) {
			t.Fatalf("migration discarded %q:\n%s", retained, text)
		}
	}
	if strings.Contains(text, "cron_hour:") || !strings.Contains(text, "start_time: 04:25") {
		t.Fatalf("nightly schema was not migrated:\n%s", text)
	}
	if strings.Contains(text, "duplicate_review_enabled") || strings.Contains(text, "dedupe:") {
		t.Fatalf("retired duplicate-review setting remains:\n%s", text)
	}
	if !strings.Contains(text, "builtin_pack_enabled: false") {
		t.Fatalf("built-in tag setting was not migrated:\n%s", text)
	}
	want := LiveSettings{NightlyStartTime: "04:25", BuiltinTagsEnabled: false}
	if got := manager.LiveSettings(); got != want {
		t.Fatalf("live settings = %#v, want %#v", got, want)
	}
}

func TestManagerYAMLValuesWinOverLegacySQLiteValues(t *testing.T) {
	manager, path := newManagerForTest(t, "nightly:\n  start_time: \"02:10\"\n  cron_hour: 7\ntags:\n  builtin_pack_enabled: true\ndedupe:\n  duplicate_review_enabled: true\n")
	start := "22:45"
	builtinTagsEnabled := false
	_, err := manager.MigrateLegacyRuntimeSettings(LegacyRuntimeSettings{
		NightlyStartTime:   &start,
		BuiltinTagsEnabled: &builtinTagsEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := LiveSettings{NightlyStartTime: "02:10", BuiltinTagsEnabled: true}
	if got := manager.LiveSettings(); got != want {
		t.Fatalf("live settings = %#v, want YAML %#v", got, want)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "cron_hour:") || strings.Contains(string(data), "duplicate_review_enabled") {
		t.Fatalf("retired fields remain:\n%s", data)
	}
}

func TestManagerRejectsStaleVersionWithoutChangingFile(t *testing.T) {
	manager, path := newManagerForTest(t, "nightly:\n  start_time: \"01:00\"\n")
	original, _ := os.ReadFile(path)
	_, err := manager.ReplaceYAML([]byte("nightly:\n  start_time: \"02:00\"\n"), "stale")
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, want ErrVersionConflict", err)
	}
	written, _ := os.ReadFile(path)
	if string(written) != string(original) {
		t.Fatalf("stale write changed file:\n%s", written)
	}
}

func TestManagerReloadPublishesExternalValidChangeAndKeepsLastGoodOnError(t *testing.T) {
	manager, path := newManagerForTest(t, "nightly:\n  start_time: \"01:00\"\n")
	var applied []LiveSettings
	if err := manager.SetApply(func(settings LiveSettings) error {
		applied = append(applied, settings)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("nightly:\n  start_time: \"06:30\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	changed, err := manager.Reload()
	if err != nil || !changed {
		t.Fatalf("reload changed=%v err=%v", changed, err)
	}
	want := LiveSettings{NightlyStartTime: "06:30", BuiltinTagsEnabled: true}
	if got := manager.LiveSettings(); got != want {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
	if err := os.WriteFile(path, []byte("nightly:\n  start_time: \"99:00\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.Reload(); err == nil || changed {
		t.Fatalf("invalid reload changed=%v err=%v", changed, err)
	}
	if got := manager.LiveSettings(); got != want {
		t.Fatalf("invalid reload replaced last good settings: %#v", got)
	}
	if len(applied) != 2 {
		t.Fatalf("apply callbacks = %d, want initial + valid reload", len(applied))
	}
}

func TestRestartRequiredComparisonIgnoresOnlyLivePaths(t *testing.T) {
	before := []byte("nightly:\n  start_time: \"01:00\"\nfuture:\n  value: one\n")
	liveOnly := []byte("nightly:\n  start_time: \"03:15\"\ntags:\n  builtin_pack_enabled: false\nfuture:\n  value: one\n")
	if hasRestartRequiredChange(before, liveOnly) {
		t.Fatal("live-only values were reported as restart-required")
	}
	nonLive := []byte("nightly:\n  start_time: \"03:15\"\nfuture:\n  value: two\n")
	if !hasRestartRequiredChange(before, nonLive) {
		t.Fatal("unknown non-live value should require restart")
	}
}

func TestManagerRestoresYAMLAndLiveSnapshotWhenLiveApplyFails(t *testing.T) {
	manager, path := newManagerForTest(t, "nightly:\n  start_time: \"01:00\"\ntags:\n  builtin_pack_enabled: true\n")
	var applied []LiveSettings
	if err := manager.SetApply(func(settings LiveSettings) error {
		applied = append(applied, settings)
		if !settings.BuiltinTagsEnabled {
			return errors.New("catalog unavailable")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	original, version, err := manager.ReadYAML()
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.ReplaceYAML([]byte("nightly:\n  start_time: \"01:00\"\ntags:\n  builtin_pack_enabled: false\n"), version)
	if err == nil || !strings.Contains(err.Error(), "catalog unavailable") {
		t.Fatalf("replace error = %v, want live apply failure", err)
	}
	written, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(written) != string(original) {
		t.Fatalf("failed live apply changed YAML:\n%s", written)
	}
	if got := manager.LiveSettings(); !got.BuiltinTagsEnabled {
		t.Fatalf("failed live apply changed snapshot: %#v", got)
	}
	if len(applied) != 3 || !applied[0].BuiltinTagsEnabled || applied[1].BuiltinTagsEnabled || !applied[2].BuiltinTagsEnabled {
		t.Fatalf("apply sequence = %#v, want current, rejected candidate, rollback", applied)
	}
}
