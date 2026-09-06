package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/drives"
	"github.com/video-site/backend/internal/proxy"
	"github.com/video-site/backend/internal/scanjob"
)

func scanResultTestApp(t *testing.T) (*App, *serverTreeScanDrive) {
	t.Helper()
	cat, err := catalog.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	const id = "scan-result-drive"
	if err := cat.UpsertDrive(context.Background(), &catalog.Drive{ID: id, Kind: "fake", Name: "Scan test", RootID: "root"}); err != nil {
		t.Fatal(err)
	}
	drv := &serverTreeScanDrive{id: id, entries: map[string][]drives.Entry{"root": {}}}
	registry := proxy.NewRegistry()
	registry.Set(id, drv)
	return &App{cat: cat, registry: registry, cfg: &config.Config{
		Scanner: config.Scanner{VideoExtensions: []string{".mp4"}},
		Storage: config.Storage{LocalPreviewDir: t.TempDir()},
	}}, drv
}

func TestScanReplacementSurvivesFirstScanAndRetainsLiveDeduplication(t *testing.T) {
	for _, hash := range []string{"same-hash", ""} {
		t.Run("hash="+hash, func(t *testing.T) {
			app, drv := scanResultTestApp(t)
			ctx := context.Background()
			now := time.Now()
			oldName := "clip.mp4"
			if hash != "" {
				oldName = "original.mp4"
			}
			if err := app.cat.UpsertVideo(ctx, &catalog.Video{
				ID: "old-row", DriveID: drv.ID(), FileID: "old-file", FileName: oldName,
				ContentHash: hash, Size: 123, ParentID: "root", AncestorDirIDs: []string{"root"},
				Title: "Clip", CreatedAt: now, PublishedAt: now, UpdatedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			drv.entries["root"] = []drives.Entry{
				{ID: "replacement", Name: "clip.mp4", Hash: hash, Size: 123},
				{ID: "another-copy", Name: "clip.mp4", Hash: hash, Size: 123},
			}
			result := app.runScan(ctx, drv.ID())
			if result.State != scanjob.Succeeded || result.AddedCount != 1 || result.DuplicateCount != 1 {
				t.Fatalf("unexpected result: %+v", result)
			}
			items, err := app.cat.ListVideosByDrive(ctx, drv.ID())
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].FileID != "replacement" {
				t.Fatalf("first scan must retain the live replacement: %+v", items)
			}
			again := app.runScan(ctx, drv.ID())
			if again.AddedCount != 0 || again.State != scanjob.Succeeded {
				t.Fatalf("rescan = %+v", again)
			}
		})
	}
}

func TestScanReportsAndPersistsPartialDiscovery(t *testing.T) {
	app, drv := scanResultTestApp(t)
	drv.entries["root"] = []drives.Entry{
		{ID: "failed-dir", Name: "Unavailable", IsDir: true},
		{ID: "live-file", Name: "clip.mp4", Size: 123},
	}
	drv.listErrors = map[string]error{"failed-dir": errors.New("provider unavailable")}
	result := app.runScan(context.Background(), drv.ID())
	if result.State != scanjob.Partial || result.ErrorCount != 1 || result.ScannedCount != 1 || result.AddedCount != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Issues) != 1 || result.Issues[0].Stage != "discovery" {
		t.Fatalf("issues = %+v", result.Issues)
	}
	stored, err := app.cat.LatestScanResults(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored[drv.ID()].State != scanjob.Partial || stored[drv.ID()].AddedCount != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	if app.driveHasActiveWork(drv.ID()) {
		t.Fatal("finished results must not keep the drive busy")
	}
}

func TestScanReportsFatalFailureAndCancellation(t *testing.T) {
	t.Run("root failure", func(t *testing.T) {
		app, drv := scanResultTestApp(t)
		drv.listErrors = map[string]error{"root": errors.New("root unavailable")}
		result := app.runScan(context.Background(), drv.ID())
		if result.State != scanjob.Failed || result.ErrorCount != 1 || result.Message == "" {
			t.Fatalf("result = %+v", result)
		}
		stored, err := app.cat.LatestScanResults(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if stored[drv.ID()].State != scanjob.Failed {
			t.Fatalf("stored = %+v", stored)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		app, drv := scanResultTestApp(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := app.runScan(ctx, drv.ID())
		if result.State != scanjob.Canceled {
			t.Fatalf("result = %+v", result)
		}
		stored, err := app.cat.LatestScanResults(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if stored[drv.ID()].State != scanjob.Canceled {
			t.Fatalf("cancellation was not retained: %+v", stored)
		}
	})
}

func TestScanReturnsSkippedWhenAnotherScanOwnsTheDrive(t *testing.T) {
	app, drv := scanResultTestApp(t)
	if !app.beginDriveScanOrCrawl(drv.ID()) {
		t.Fatal("could not begin original scan")
	}
	defer app.endDriveScanOrCrawl(drv.ID())
	result := app.runScan(context.Background(), drv.ID())
	if result.State != scanjob.Skipped || result.Message == "" {
		t.Fatalf("result = %+v", result)
	}
	if !app.scanQueued[drv.ID()] {
		t.Fatal("rejected scan cleared the running scan")
	}
}
