package main

import (
	"context"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

const (
	legacyNightlyStartTimeSetting         = "automation.nightly_start_time"
	obsoleteDuplicateReviewEnabledSetting = "dedupe.duplicate_review_enabled"
	legacySettingMissing                  = "\x00video-site-config-setting-missing\x00"
)

func (a *App) liveConfigSettings() config.LiveSettings {
	if a == nil || a.configManager == nil {
		return config.DefaultLiveSettings()
	}
	return a.configManager.LiveSettings()
}

func (a *App) applyLiveConfig(settings config.LiveSettings) {
	if a == nil || a.nightlyRunner == nil {
		return
	}
	// The configuration parser already validated and normalized this value.
	// Runner validates again at its own boundary as a defensive invariant.
	if err := a.nightlyRunner.UpdateStartTime(settings.NightlyStartTime); err != nil {
		return
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
	return legacy, nil
}
