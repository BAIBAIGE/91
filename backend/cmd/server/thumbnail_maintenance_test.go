package main

import (
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

func TestNormalizeLegacyThumbnailFilesConvertsAndMarksMigration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	previewDir := filepath.Join(root, "previews")
	thumbDir := filepath.Join(previewDir, "thumbs")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatalf("mkdir thumbnails: %v", err)
	}
	legacyPath := filepath.Join(thumbDir, "legacy.jpg")
	legacy, err := os.Create(legacyPath)
	if err != nil {
		t.Fatalf("create legacy thumbnail: %v", err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 24, 16))
	for y := 0; y < frame.Bounds().Dy(); y++ {
		for x := 0; x < frame.Bounds().Dx(); x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 20, G: 140, B: 220, A: 255})
		}
	}
	if err := png.Encode(legacy, frame); err != nil {
		_ = legacy.Close()
		t.Fatalf("encode legacy PNG: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy thumbnail: %v", err)
	}

	cat, err := catalog.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer cat.Close()
	app := &App{cat: cat, cfg: &config.Config{Storage: config.Storage{LocalPreviewDir: previewDir}}}
	stats, err := app.normalizeLegacyThumbnailFiles(ctx)
	if err != nil {
		t.Fatalf("normalize legacy thumbnails: %v", err)
	}
	if stats.Scanned != 1 || stats.Normalized != 1 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want scanned=1 normalized=1 failed=0", stats)
	}
	normalized, err := os.Open(legacyPath)
	if err != nil {
		t.Fatalf("open normalized thumbnail: %v", err)
	}
	if _, err := jpeg.Decode(normalized); err != nil {
		_ = normalized.Close()
		t.Fatalf("decode normalized JPEG: %v", err)
	}
	_ = normalized.Close()
	marker, err := cat.GetSetting(ctx, thumbnailJPEGNormalizationSetting, "")
	if err != nil || marker != "1" {
		t.Fatalf("migration marker = %q err=%v, want 1", marker, err)
	}

	if err := os.WriteFile(legacyPath, []byte("marker prevents a second pass"), 0o644); err != nil {
		t.Fatalf("replace normalized thumbnail: %v", err)
	}
	second, err := app.normalizeLegacyThumbnailFiles(ctx)
	if err != nil {
		t.Fatalf("second normalization: %v", err)
	}
	if second.Scanned != 0 || second.Normalized != 0 || second.Failed != 0 {
		t.Fatalf("second stats = %+v, want zero-value marker skip", second)
	}
}

func TestNormalizeLegacyThumbnailFilesDoesNotMarkFailedMigration(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	previewDir := filepath.Join(root, "previews")
	thumbDir := filepath.Join(previewDir, "thumbs")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatalf("mkdir thumbnails: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thumbDir, "invalid.jpg"), []byte("invalid"), 0o644); err != nil {
		t.Fatalf("write invalid thumbnail: %v", err)
	}
	cat, err := catalog.Open(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	defer cat.Close()
	app := &App{cat: cat, cfg: &config.Config{Storage: config.Storage{LocalPreviewDir: previewDir}}}
	stats, err := app.normalizeLegacyThumbnailFiles(ctx)
	if err == nil {
		t.Fatal("normalization succeeded despite invalid thumbnail")
	}
	if stats.Failed != 1 {
		t.Fatalf("stats = %+v, want one failure", stats)
	}
	marker, markerErr := cat.GetSetting(ctx, thumbnailJPEGNormalizationSetting, "")
	if markerErr != nil || marker != "" {
		t.Fatalf("migration marker = %q err=%v, want unset", marker, markerErr)
	}
}
