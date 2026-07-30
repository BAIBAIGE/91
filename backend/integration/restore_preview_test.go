package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/video-site/backend/internal/api"
	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/backup"
	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/mediaasset"
	"github.com/video-site/backend/internal/proxy"
)

func TestRestoredPreviewServesWithRelativeTargetStorageConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	sourceRoot := filepath.Join(root, "source")
	sourceFileConfig := relativeStorageConfig()
	sourceRuntimeStorage := mustResolveStorage(t, sourceFileConfig.Storage, sourceRoot)
	mustWriteConfig(t, filepath.Join(sourceRoot, "config.yaml"), sourceFileConfig)
	if err := os.MkdirAll(sourceRuntimeStorage.LocalPreviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceCatalog, err := catalog.Open(sourceRuntimeStorage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	sourceManager, err := backup.NewManager(backup.Config{
		Catalog:        sourceCatalog,
		AppConfig:      sourceFileConfig,
		RuntimeStorage: sourceRuntimeStorage,
		ConfigPath:     filepath.Join(sourceRoot, "config.yaml"),
		AppVersion:     "integration-test",
		RestartManaged: true,
	})
	if err != nil {
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}

	previewPath := mediaasset.PreviewPath(sourceRuntimeStorage.LocalPreviewDir, "video-1")
	if err := os.WriteFile(previewPath, []byte("restored-preview"), 0o644); err != nil {
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}
	now := time.Now()
	if err := sourceCatalog.UpsertVideo(ctx, &catalog.Video{
		ID:            "video-1",
		DriveID:       "drive-1",
		FileID:        "file-1",
		Title:         "Restored video",
		PreviewLocal:  previewPath,
		PreviewStatus: "ready",
		PublishedAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}
	record := createBackup(t, sourceManager)
	archive, _, archiveName, err := sourceManager.OpenBackup(record.ID)
	if err != nil {
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}

	targetRoot := filepath.Join(root, "target")
	targetFileConfig := relativeStorageConfig()
	targetRuntimeStorage := mustResolveStorage(t, targetFileConfig.Storage, targetRoot)
	targetConfigPath := filepath.Join(targetRoot, "config.yaml")
	mustWriteConfig(t, targetConfigPath, targetFileConfig)
	if err := os.MkdirAll(targetRuntimeStorage.LocalPreviewDir, 0o755); err != nil {
		_ = archive.Close()
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}
	targetCatalog, err := catalog.Open(targetRuntimeStorage.DBPath)
	if err != nil {
		_ = archive.Close()
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}
	targetManager, err := backup.NewManager(backup.Config{
		Catalog:        targetCatalog,
		AppConfig:      targetFileConfig,
		RuntimeStorage: targetRuntimeStorage,
		ConfigPath:     targetConfigPath,
		AppVersion:     "integration-test",
		RestartManaged: true,
	})
	if err != nil {
		_ = archive.Close()
		_ = targetCatalog.Close()
		sourceManager.Close()
		_ = sourceCatalog.Close()
		t.Fatal(err)
	}
	targetArchivePath := filepath.Join(
		filepath.Dir(targetRuntimeStorage.DBPath),
		"backups",
		archiveName,
	)
	targetArchive, err := os.Create(targetArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(targetArchive, archive); err != nil {
		_ = targetArchive.Close()
		t.Fatal(err)
	}
	if err := targetArchive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	sourceManager.Close()
	if err := sourceCatalog.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := targetManager.PrepareRestore(ctx, record.ID); err != nil {
		t.Fatal(err)
	}
	targetManager.Close()
	if err := targetCatalog.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := backup.ApplyPendingRestore(filepath.Dir(targetRuntimeStorage.DBPath))
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("restore was not applied")
	}
	restoredCatalog, err := catalog.Open(targetRuntimeStorage.DBPath)
	if err != nil {
		_ = backup.RollbackAppliedRestore(applied, err)
		t.Fatal(err)
	}
	defer restoredCatalog.Close()
	if err := backup.CommitAppliedRestore(applied); err != nil {
		t.Fatal(err)
	}

	restoredVideo, err := restoredCatalog.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatal(err)
	}
	wantPreviewPath := mediaasset.PreviewPath(targetRuntimeStorage.LocalPreviewDir, "video-1")
	if restoredVideo.PreviewLocal != wantPreviewPath {
		t.Fatalf("restored preview path = %q, want %q", restoredVideo.PreviewLocal, wantPreviewPath)
	}
	restoredFileConfig, err := config.Load(targetConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if restoredFileConfig.Storage != targetFileConfig.Storage {
		t.Fatalf("restored file storage config = %+v, want %+v", restoredFileConfig.Storage, targetFileConfig.Storage)
	}

	const sessionToken = "integration-session"
	if err := restoredCatalog.CreateSession(ctx, sessionToken, time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	authenticator := &auth.Authenticator{Catalog: restoredCatalog}
	server := &api.Server{
		Catalog:  restoredCatalog,
		Proxy:    proxy.New(proxy.NewRegistry()),
		LocalDir: targetRuntimeStorage.LocalPreviewDir,
	}
	router := chi.NewRouter()
	server.RegisterRoutes(router, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/p/preview/video-1", nil)
	request.AddCookie(&http.Cookie{Name: "vs_admin", Value: sessionToken})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Body.String() != "restored-preview" {
		t.Fatalf("preview body = %q", response.Body.String())
	}
}

func relativeStorageConfig() *config.Config {
	return &config.Config{
		Server: config.Server{
			Listen: "127.0.0.1:9192",
			Admin: config.Admin{
				Username: "admin",
				Password: "admin-password",
			},
		},
		Storage: config.Storage{
			DBPath:          "./data/video-site.db",
			LocalPreviewDir: "./data/previews",
		},
	}
}

func mustResolveStorage(t *testing.T, storage config.Storage, root string) config.Storage {
	t.Helper()
	resolved, err := config.ResolveStoragePaths(storage, root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func mustWriteConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func createBackup(t *testing.T, manager *backup.Manager) backup.BackupRecord {
	t.Helper()
	if _, err := manager.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Current()
		if status != nil && status.State == "completed" {
			result, err := manager.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Backups) == 0 {
				t.Fatal("backup completed without archive")
			}
			return result.Backups[0]
		}
		if status != nil && (status.State == "failed" || status.State == "canceled") {
			t.Fatalf("backup ended as %s: %s", status.State, status.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for backup")
	return backup.BackupRecord{}
}
