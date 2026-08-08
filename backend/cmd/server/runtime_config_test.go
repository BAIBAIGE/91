package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/nightly"
)

func TestConfigSavePersistsAndHotUpdatesRuntimeSettings(t *testing.T) {
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("nightly:\n  start_time: \"01:00\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	tagCacheInvalidations := 0
	app := &App{
		cat:           cat,
		configManager: manager,
		onTagsChanged: func() { tagCacheInvalidations++ },
	}
	app.nightlyRunner = nightly.New(nightly.Config{Settings: cat, StartTime: manager.LiveSettings().NightlyStartTime})
	if err := manager.SetApply(func(settings config.LiveSettings) error {
		return app.applyLiveConfig(context.Background(), settings)
	}); err != nil {
		t.Fatal(err)
	}

	_, version, err := manager.ReadYAML()
	if err != nil {
		t.Fatal(err)
	}
	next, err := manager.ReplaceYAML([]byte("nightly:\n  start_time: \"00:45\"\ntags:\n  builtin_pack_enabled: false\n"), version)
	if err != nil {
		t.Fatalf("replace config: %v", err)
	}
	if next.Settings.NightlyStartTime != "00:45" {
		t.Fatalf("updated settings = %#v", next.Settings)
	}
	if got := app.nightlyRunner.StartTime(); got != "00:45" {
		t.Fatalf("live scheduler start time = %q, want 00:45", got)
	}
	if next.Settings.BuiltinTagsEnabled {
		t.Fatalf("updated settings = %#v, want built-in tags disabled", next.Settings)
	}
	enabled, err := cat.BuiltinTagsEnabled(context.Background())
	if err != nil || enabled {
		t.Fatalf("catalog built-in setting = %v, %v; want disabled", enabled, err)
	}
	if tagCacheInvalidations != 1 {
		t.Fatalf("tag cache invalidations = %d, want 1", tagCacheInvalidations)
	}
	reloaded, err := config.NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.LiveSettings(); got != next.Settings {
		t.Fatalf("reloaded settings = %#v, want %#v", got, next.Settings)
	}
}

func TestLoadLegacyRuntimeSettingsIgnoresInvalidValues(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	if err := cat.SetSetting(ctx, legacyNightlyStartTimeSetting, "25:00"); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadLegacyRuntimeSettings(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.NightlyStartTime != nil {
		t.Fatalf("invalid legacy values should be ignored: %#v", legacy)
	}
}

func TestMigratedRuntimeSettingsCanBeRemovedFromSQLite(t *testing.T) {
	ctx := context.Background()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	if err := cat.SetSetting(ctx, legacyNightlyStartTimeSetting, "03:20"); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetSetting(ctx, obsoleteDuplicateReviewEnabledSetting, "false"); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetSetting(ctx, legacyBuiltinTagsEnabledSetting, "false"); err != nil {
		t.Fatal(err)
	}
	if err := cat.DeleteSettings(ctx, legacyNightlyStartTimeSetting, legacyBuiltinTagsEnabledSetting, obsoleteDuplicateReviewEnabledSetting); err != nil {
		t.Fatal(err)
	}
	legacy, err := loadLegacyRuntimeSettings(ctx, cat)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.NightlyStartTime != nil {
		t.Fatalf("deleted SQLite settings returned: %#v", legacy)
	}
}
