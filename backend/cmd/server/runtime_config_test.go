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

func TestConfigSavePersistsAndHotUpdatesScheduler(t *testing.T) {
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
	app := &App{cat: cat, configManager: manager}
	app.nightlyRunner = nightly.New(nightly.Config{Settings: cat, StartTime: manager.LiveSettings().NightlyStartTime})
	manager.SetApply(app.applyLiveConfig)

	_, version, err := manager.ReadYAML()
	if err != nil {
		t.Fatal(err)
	}
	next, err := manager.ReplaceYAML([]byte("nightly:\n  start_time: \"00:45\"\n"), version)
	if err != nil {
		t.Fatalf("replace config: %v", err)
	}
	if next.Settings.NightlyStartTime != "00:45" {
		t.Fatalf("updated settings = %#v", next.Settings)
	}
	if got := app.nightlyRunner.StartTime(); got != "00:45" {
		t.Fatalf("live scheduler start time = %q, want 00:45", got)
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
	if err := cat.DeleteSettings(ctx, legacyNightlyStartTimeSetting, obsoleteDuplicateReviewEnabledSetting); err != nil {
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
