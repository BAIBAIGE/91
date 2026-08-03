package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/video-site/backend/internal/mediaasset"
)

const thumbnailJPEGNormalizationSetting = "media.thumbnails.jpeg_normalized.v1"

// normalizeLegacyThumbnailFiles upgrades the old cache contract where a .jpg
// path could contain WebP bytes. The catalog marker makes the directory scan a
// one-time migration while still allowing a restored pre-migration database to
// trigger it again.
func (a *App) normalizeLegacyThumbnailFiles(ctx context.Context) (mediaasset.ThumbnailNormalizationStats, error) {
	stats := mediaasset.ThumbnailNormalizationStats{}
	if a == nil || a.cfg == nil || a.cat == nil {
		return stats, errors.New("thumbnail normalization dependencies are unavailable")
	}
	marker, err := a.cat.GetSetting(ctx, thumbnailJPEGNormalizationSetting, "")
	if err != nil {
		return stats, fmt.Errorf("read migration marker: %w", err)
	}
	if strings.TrimSpace(marker) == "1" {
		return stats, nil
	}

	directory := filepath.Join(a.cfg.Storage.LocalPreviewDir, "thumbs")
	stats, err = mediaasset.NormalizeThumbnailDirectoryJPEG(directory)
	if err != nil {
		return stats, fmt.Errorf(
			"normalize legacy thumbnails scanned=%d normalized=%d failed=%d: %w",
			stats.Scanned,
			stats.Normalized,
			stats.Failed,
			err,
		)
	}
	if err := a.cat.SetSetting(ctx, thumbnailJPEGNormalizationSetting, "1"); err != nil {
		return stats, fmt.Errorf("write migration marker: %w", err)
	}
	log.Printf(
		"[thumbnail-maintenance] scanned=%d normalized=%d",
		stats.Scanned,
		stats.Normalized,
	)
	return stats, nil
}
