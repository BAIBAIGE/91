package main

import (
	"context"
	"fmt"
	"log"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

const (
	legacyNightlyStartTimeSetting   = "automation.nightly_start_time"
	legacyBuiltinTagsEnabledSetting = "tags.builtin_pack_enabled"
	legacySettingMissing            = "\x00video-site-config-setting-missing\x00"
)

func (a *App) liveConfigSettings() config.LiveSettings {
	if a == nil || a.configManager == nil {
		return config.DefaultLiveSettings()
	}
	return a.configManager.LiveSettings()
}

func (a *App) applyLiveConfig(ctx context.Context, settings config.LiveSettings) error {
	if a == nil {
		return nil
	}
	if a.nightlyRunner != nil {
		// The configuration parser already validated and normalized these values.
		// Runner validates again and swaps the schedule state atomically at its boundary.
		if err := a.nightlyRunner.UpdateSchedule(
			settings.NightlyStartTime,
			settings.NightlyTimezone,
			settings.NightlyDisabled,
		); err != nil {
			return fmt.Errorf("update nightly schedule: %w", err)
		}
	}
	a.applyPreviewConcurrency(settings.PreviewConcurrency)
	if a.cat == nil {
		return nil
	}
	changed, err := a.cat.SetBuiltinTagsEnabled(ctx, settings.BuiltinTagsEnabled)
	if err != nil {
		return fmt.Errorf("apply built-in tag configuration: %w", err)
	}
	if !changed {
		return nil
	}
	if a.onTagsChanged != nil {
		a.onTagsChanged()
	}
	if settings.BuiltinTagsEnabled {
		a.startTagRetag(ctx)
	}
	return nil
}

func (a *App) previewConcurrencyLocked() int {
	if a.previewConcurrency < 1 {
		return config.DefaultPreviewConcurrency
	}
	return a.previewConcurrency
}

// applyPreviewConcurrency copies one configuration value to every drive's
// independent preview worker. Holding a.mu across both the stored value and
// worker updates makes concurrent drive attachment observe either the old or
// the new complete configuration, never a missed transition.
func (a *App) applyPreviewConcurrency(concurrency int) {
	if a == nil {
		return
	}
	if concurrency < 1 {
		concurrency = config.DefaultPreviewConcurrency
	}
	a.mu.Lock()
	previous := a.previewConcurrencyLocked()
	a.previewConcurrency = concurrency
	for _, worker := range a.workers {
		worker.SetConcurrency(concurrency)
	}
	a.mu.Unlock()
	if previous != concurrency {
		log.Printf("[preview] per-drive concurrency updated from=%d to=%d", previous, concurrency)
	}
}

func loadLegacyRuntimeSettings(ctx context.Context, cat *catalog.Catalog) (config.LegacyRuntimeSettings, error) {
	var legacy config.LegacyRuntimeSettings
	startTime, err := cat.GetSetting(ctx, legacyNightlyStartTimeSetting, legacySettingMissing)
	if err != nil {
		return legacy, err
	}
	if startTime != legacySettingMissing {
		if normalized, normalizeErr := config.NormalizeNightlyStartTime(startTime); normalizeErr == nil {
			legacy.NightlyStartTime = &normalized
		}
	}
	builtinTagsEnabled, err := cat.BuiltinTagsEnabled(ctx)
	if err != nil {
		return legacy, err
	}
	legacy.BuiltinTagsEnabled = &builtinTagsEnabled
	return legacy, nil
}
