package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
	"github.com/video-site/backend/internal/mediaasset"
	"gopkg.in/yaml.v3"
)

type testBackupEnv struct {
	root       string
	configPath string
	cfg        *config.Config
	cat        *catalog.Catalog
	manager    *Manager
}

func newTestBackupEnv(t *testing.T) *testBackupEnv {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Server: config.Server{
			Listen: "127.0.0.1:9192",
			Admin: config.Admin{
				Username: "source-admin",
				Password: "source-password",
			},
			AllowedOrigins: []string{"https://source.example"},
		},
		Storage: config.Storage{
			DBPath:          filepath.Join(root, "video-site.db"),
			LocalPreviewDir: filepath.Join(root, "previews"),
		},
		Preview: config.Preview{
			Enabled:     true,
			FFmpegPath:  "/source/bin/ffmpeg",
			FFprobePath: "/source/bin/ffprobe",
		},
	}
	configPath := filepath.Join(root, "config.yaml")
	writeTestConfig(t, configPath, cfg)
	if err := os.MkdirAll(cfg.Storage.LocalPreviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Config{
		Catalog:        cat,
		AppConfig:      cfg,
		ConfigPath:     configPath,
		AppVersion:     "v1.2.3",
		RestartManaged: true,
	})
	if err != nil {
		_ = cat.Close()
		t.Fatal(err)
	}
	env := &testBackupEnv{
		root:       root,
		configPath: configPath,
		cfg:        cfg,
		cat:        cat,
		manager:    manager,
	}
	t.Cleanup(func() {
		manager.Close()
		_ = cat.Close()
	})
	return env
}

func writeTestConfig(t *testing.T, path string, cfg *config.Config) {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func createAndWaitForBackup(t *testing.T, manager *Manager) BackupRecord {
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
				t.Fatal("backup task completed without an archive")
			}
			return result.Backups[0]
		}
		if status != nil && (status.State == "failed" || status.State == "canceled") {
			t.Fatalf("backup task ended as %s: %s", status.State, status.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for backup")
	return BackupRecord{}
}

func TestCatalogBackupToIncludesUncheckpointedWALData(t *testing.T) {
	env := newTestBackupEnv(t)
	if _, err := env.cat.CreateUser(context.Background(), "wal-user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(env.cfg.Storage.DBPath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("expected non-empty WAL before snapshot: info=%v err=%v", walInfo, err)
	}
	snapshot := filepath.Join(env.root, "snapshot.sqlite")
	if err := env.cat.BackupTo(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snapshot+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'wal-user'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot user count = %d, want 1", count)
	}
}

func TestFullBackupContainsPersistentFilesAndExcludesTemporaryData(t *testing.T) {
	env := newTestBackupEnv(t)
	ctx := context.Background()
	adminID, err := env.cat.CreateUser(ctx, "backup-admin", "admin-password-hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.cat.CreateUser(ctx, "backup-user", "user-password-hash", "user"); err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateSession(ctx, "backup-session", time.Hour, adminID); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(env.root, "previews", "cover.jpg"), []byte("cover"))
	writeTestFile(t, filepath.Join(env.root, "previews", "teaser.mp4"), []byte("teaser"))
	writeTestFile(t, filepath.Join(env.root, "previews", "framesigs", "video.fsig"), []byte("framesig"))
	writeTestFile(t, filepath.Join(env.root, "uploads", "upload.mp4"), []byte("upload"))
	writeTestFile(t, filepath.Join(env.root, "crawler-scripts", "crawler.py"), []byte("print('ok')"))
	writeTestFile(t, filepath.Join(env.root, "scriptcrawlers", "demo", "videos", "crawl.mp4"), []byte("crawl"))
	writeTestFile(t, filepath.Join(env.root, "spider91", "legacy.mp4"), []byte("legacy"))
	writeTestFile(t, filepath.Join(env.root, "previews", "ignored.part"), []byte("partial"))
	writeTestFile(t, filepath.Join(env.root, "crawler-scripts", "__pycache__", "ignored.pyc"), []byte("cache"))
	writeTestFile(t, filepath.Join(env.root, "upload-tmp", "ignored.mp4"), []byte("temp"))
	outside := filepath.Join(env.root, "outside-secret")
	writeTestFile(t, outside, []byte("secret"))
	if err := os.Symlink(outside, filepath.Join(env.root, "previews", "linked-secret")); err != nil {
		t.Fatal(err)
	}

	record := createAndWaitForBackup(t, env.manager)
	archivePath, _, err := env.manager.resolveBackup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files := make(map[string]*zip.File)
	for _, file := range reader.File {
		files[file.Name] = file
	}
	for _, expected := range []string{
		"manifest.json",
		"payload/database.sqlite",
		"payload/config.yaml",
		"payload/previews/cover.jpg",
		"payload/previews/teaser.mp4",
		"payload/previews/framesigs/video.fsig",
		"payload/uploads/upload.mp4",
		"payload/crawler-scripts/crawler.py",
		"payload/scriptcrawlers/demo/videos/crawl.mp4",
		"payload/spider91/legacy.mp4",
	} {
		if files[expected] == nil {
			t.Errorf("archive is missing %s", expected)
		}
	}
	for _, excluded := range []string{
		"payload/previews/ignored.part",
		"payload/crawler-scripts/__pycache__/ignored.pyc",
		"payload/previews/linked-secret",
	} {
		if files[excluded] != nil {
			t.Errorf("archive unexpectedly contains %s", excluded)
		}
	}
	if record.VerificationStatus != "verified" || !validSHA256(record.SHA256) {
		t.Fatalf("record verification = %q sha=%q", record.VerificationStatus, record.SHA256)
	}

	databasePath := filepath.Join(t.TempDir(), "database.sqlite")
	if err := os.WriteFile(databasePath, readZipFile(t, files["payload/database.sqlite"]), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var adminCount, userCount, sessionCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&adminCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'backup-user' AND role = 'user'`).Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM admin_sessions`).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if adminCount != 0 || userCount != 1 || sessionCount != 0 {
		t.Fatalf(
			"redacted snapshot has admins=%d users=%d sessions=%d, want 0/1/0",
			adminCount,
			userCount,
			sessionCount,
		)
	}

	var backupConfig config.Config
	if err := yaml.Unmarshal(readZipFile(t, files["payload/config.yaml"]), &backupConfig); err != nil {
		t.Fatal(err)
	}
	if backupConfig.Server.Admin.Username != "" || backupConfig.Server.Admin.Password != "" {
		t.Fatalf("backup administrator config was not redacted: %+v", backupConfig.Server.Admin)
	}
	liveConfig, err := config.Load(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if liveConfig.Server.Admin.Username != "source-admin" ||
		liveConfig.Server.Admin.Password != "source-password" {
		t.Fatalf("creating a backup changed the live administrator config: %+v", liveConfig.Server.Admin)
	}
}

func TestBackupTaskIsExclusiveCancelableAndRejectsLowDisk(t *testing.T) {
	env := newTestBackupEnv(t)
	writeTestFile(t, filepath.Join(env.root, "uploads", "data.mp4"), bytes.Repeat([]byte("x"), 8<<20))
	if _, err := env.manager.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.Create(context.Background()); !errors.Is(err, ErrTaskRunning) {
		t.Fatalf("second Create error = %v, want ErrTaskRunning", err)
	}
	if err := env.manager.Cancel(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := env.manager.Current()
		if status != nil && status.State == "canceled" {
			break
		}
		if status != nil && status.State == "completed" {
			t.Fatal("backup completed despite immediate cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status := env.manager.Current(); status == nil || status.State != "canceled" {
		t.Fatalf("status after cancel = %+v", status)
	}

	env.manager.mu.Lock()
	env.manager.estimateUntil = time.Time{}
	env.manager.mu.Unlock()
	env.manager.availableBytes = func(string) (int64, error) { return 1, nil }
	if _, err := env.manager.Create(context.Background()); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("low-disk Create error = %v, want ErrInsufficientSpace", err)
	}
}

func TestStartupCleanupAndUploadExpiryPreserveCompletedBackups(t *testing.T) {
	env := newTestBackupEnv(t)
	completedPath := filepath.Join(env.root, "backups", "video-site-91-full-kept.zip")
	writeTestFile(t, completedPath, []byte("completed"))
	interruptedPath := filepath.Join(env.root, "backups", "video-site-91-full-interrupted.zip.part")
	writeTestFile(t, interruptedPath, []byte("partial"))
	orphanSnapshot := filepath.Join(env.root, ".backup-snapshots", "orphan", "payload", "file")
	writeTestFile(t, orphanSnapshot, []byte("snapshot"))

	env.manager.Close()
	restarted, err := NewManager(Config{
		Catalog:    env.cat,
		AppConfig:  env.cfg,
		ConfigPath: env.configPath,
		AppVersion: "v1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err := os.Stat(interruptedPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted backup part still exists: %v", err)
	}
	if _, err := os.Stat(orphanSnapshot); !os.IsNotExist(err) {
		t.Fatalf("orphan snapshot still exists: %v", err)
	}
	if _, err := os.Stat(completedPath); err != nil {
		t.Fatalf("completed backup was removed during cleanup: %v", err)
	}

	now := time.Unix(1_800_000_000, 0).UTC()
	restarted.now = func() time.Time { return now }
	session, err := restarted.BeginUpload(context.Background(), BeginUploadInput{
		FileName: "unfinished.zip",
		Size:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.FinalizeUpload(context.Background(), session.ID); !errors.Is(err, ErrUploadIncomplete) {
		t.Fatalf("incomplete finalize error = %v, want ErrUploadIncomplete", err)
	}
	now = now.Add(UploadTTL + time.Second)
	if err := restarted.cleanupExpiredUploads(); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.UploadStatus(session.ID); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("expired upload status error = %v, want ErrUploadNotFound", err)
	}
	if _, err := os.Stat(completedPath); err != nil {
		t.Fatalf("completed backup was removed with expired upload: %v", err)
	}
}

func TestVerifyArchiveRejectsUnsafeDuplicateTamperedAndNewerArchives(t *testing.T) {
	env := newTestBackupEnv(t)
	writeTestFile(t, filepath.Join(env.root, "uploads", "safe.mp4"), []byte("safe"))
	record := createAndWaitForBackup(t, env.manager)
	validPath, _, err := env.manager.resolveBackup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	var progressEvents []OperationProgress
	verified, err := VerifyArchive(context.Background(), validPath, VerifyOptions{
		CurrentVersion: "v1.2.3",
		Progress: func(progress OperationProgress) {
			progressEvents = append(progressEvents, progress)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(progressEvents) == 0 {
		t.Fatal("archive verification did not report progress")
	}
	lastProgress := progressEvents[len(progressEvents)-1]
	if lastProgress.Phase != "database" ||
		lastProgress.ProcessedBytes != verified.Manifest.TotalSize ||
		lastProgress.ProcessedFiles != verified.Manifest.FileCount {
		t.Fatalf("final archive progress = %+v, manifest = %+v", lastProgress, verified.Manifest)
	}

	traversal := filepath.Join(env.root, "traversal.zip")
	writeRawZip(t, traversal, []rawZipEntry{
		{name: "manifest.json", body: []byte(`{"formatVersion":1,"fileCount":1,"totalSize":1}`)},
		{name: "../escape", body: []byte("x")},
	})
	if _, err := VerifyArchive(context.Background(), traversal, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil {
		t.Fatal("path traversal archive was accepted")
	}

	duplicate := filepath.Join(env.root, "duplicate.zip")
	writeRawZip(t, duplicate, []rawZipEntry{
		{name: "manifest.json", body: []byte(`{"formatVersion":1,"fileCount":1,"totalSize":1}`)},
		{name: "payload/config.yaml", body: []byte("a")},
		{name: "payload/config.yaml", body: []byte("b")},
	})
	if _, err := VerifyArchive(context.Background(), duplicate, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil {
		t.Fatal("duplicate-entry archive was accepted")
	}

	symlink := filepath.Join(env.root, "symlink.zip")
	writeRawZip(t, symlink, []rawZipEntry{
		{name: "manifest.json", body: []byte(`{"formatVersion":1,"fileCount":1,"totalSize":6}`)},
		{name: "payload/previews/link", body: []byte("target"), mode: os.ModeSymlink | 0o777},
	})
	if _, err := VerifyArchive(context.Background(), symlink, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil ||
		!strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("symlink archive error = %v", err)
	}

	corruptDatabase := filepath.Join(env.root, "corrupt-database.zip")
	writePayloadArchive(t, corruptDatabase, []rawZipEntry{
		{name: "payload/database.sqlite", body: []byte("not a sqlite database")},
		{name: "payload/config.yaml", body: []byte("server: {}\n")},
	})
	if _, err := VerifyArchive(context.Background(), corruptDatabase, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil ||
		!strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("corrupt database archive error = %v", err)
	}

	tampered := filepath.Join(env.root, "tampered.zip")
	rewriteZip(t, validPath, tampered, func(name string, body []byte) []byte {
		if name == "payload/config.yaml" {
			return append(body, []byte("\n# tampered")...)
		}
		return body
	})
	if _, err := VerifyArchive(context.Background(), tampered, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil ||
		!strings.Contains(err.Error(), "SHA-256") && !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("tampered archive error = %v", err)
	}

	newer := filepath.Join(env.root, "newer.zip")
	rewriteZip(t, validPath, newer, func(name string, body []byte) []byte {
		if name != manifestName {
			return body
		}
		var manifest Manifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.AppVersion = "v99.0.0"
		updated, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		return updated
	})
	if _, err := VerifyArchive(context.Background(), newer, VerifyOptions{CurrentVersion: "v1.2.3"}); err == nil ||
		!strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer archive error = %v", err)
	}
}

func TestChunkUploadSupportsOutOfOrderRepeatAndRestartResume(t *testing.T) {
	env := newTestBackupEnv(t)
	large := bytes.Repeat([]byte{0x5a}, int(ChunkSize)+4096)
	writeTestFile(t, filepath.Join(env.root, "uploads", "large.mp4"), large)
	record := createAndWaitForBackup(t, env.manager)
	sourcePath, _, err := env.manager.resolveBackup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := env.manager.BeginUpload(context.Background(), BeginUploadInput{
		FileName: "migration.zip",
		Size:     int64(len(archiveBytes)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.TotalChunks < 2 {
		t.Fatalf("total chunks = %d, want at least 2", session.TotalChunks)
	}
	partInfo, err := os.Stat(env.manager.uploadPartPath(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	if partInfo.Size() != session.Size {
		t.Fatalf("part size = %d, want %d", partInfo.Size(), session.Size)
	}
	put := func(index int, hash string) error {
		start := int64(index) * session.ChunkSize
		end := min(start+session.ChunkSize, int64(len(archiveBytes)))
		_, err := env.manager.PutChunk(
			context.Background(),
			session.ID,
			index,
			hash,
			bytes.NewReader(archiveBytes[start:end]),
		)
		return err
	}
	lastIndex := session.TotalChunks - 1
	lastStart := int64(lastIndex) * session.ChunkSize
	lastHash := sha256Hex(archiveBytes[lastStart:])
	if err := put(lastIndex, strings.Repeat("0", 64)); err == nil {
		t.Fatal("bad chunk hash was accepted")
	}
	firstHash := sha256Hex(archiveBytes[:session.ChunkSize])
	putErrors := make(chan error, 2)
	go func() { putErrors <- put(lastIndex, lastHash) }()
	go func() { putErrors <- put(0, firstHash) }()
	for range 2 {
		if err := <-putErrors; err != nil {
			t.Fatal(err)
		}
	}
	if err := put(0, firstHash); err != nil {
		t.Fatalf("idempotent repeated chunk failed: %v", err)
	}
	uploadEntries, err := os.ReadDir(env.manager.uploadDir(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range uploadEntries {
		if strings.HasSuffix(entry.Name(), ".chunk") {
			t.Fatalf("new upload wrote legacy chunk file %q", entry.Name())
		}
	}
	part, err := os.Open(env.manager.uploadPartPath(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	lastOnDisk := make([]byte, len(archiveBytes[lastStart:]))
	if _, err := part.ReadAt(lastOnDisk, lastStart); err != nil {
		_ = part.Close()
		t.Fatal(err)
	}
	if err := part.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lastOnDisk, archiveBytes[lastStart:]) {
		t.Fatal("out-of-order chunk was not written at its fixed offset")
	}

	env.manager.Close()
	resumed, err := NewManager(Config{
		Catalog:        env.cat,
		AppConfig:      env.cfg,
		ConfigPath:     env.configPath,
		AppVersion:     "v1.2.3",
		RestartManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	status, err := resumed.UploadStatus(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Received) != 2 {
		t.Fatalf("received after restart = %d, want 2", len(status.Received))
	}
	resumed.setUploadProgress(status.ID, OperationProgress{
		Phase:          "hashing",
		ProcessedBytes: 1024,
		TotalBytes:     status.Size,
	})
	status, err = resumed.UploadStatus(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Progress == nil || status.Progress.Phase != "hashing" ||
		status.Progress.ProcessedBytes != 1024 {
		t.Fatalf("upload progress snapshot = %+v", status.Progress)
	}
	resumed.clearUploadProgress(status.ID)
	for index := 1; index < lastIndex; index++ {
		start := int64(index) * status.ChunkSize
		end := min(start+status.ChunkSize, int64(len(archiveBytes)))
		chunk := archiveBytes[start:end]
		if _, err := resumed.PutChunk(
			context.Background(),
			status.ID,
			index,
			sha256Hex(chunk),
			bytes.NewReader(chunk),
		); err != nil {
			t.Fatal(err)
		}
	}
	imported, err := resumed.FinalizeUpload(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !imported.Imported || imported.VerificationStatus != "verified" {
		t.Fatalf("imported record = %+v", imported)
	}
}

func TestChunkUploadMigratesLegacySessionToSinglePart(t *testing.T) {
	env := newTestBackupEnv(t)
	writeTestFile(t, filepath.Join(env.root, "uploads", "legacy.mp4"), []byte("legacy-upload"))
	record := createAndWaitForBackup(t, env.manager)
	sourcePath, _, err := env.manager.resolveBackup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := env.manager.BeginUpload(context.Background(), BeginUploadInput{
		FileName: "legacy.zip",
		Size:     int64(len(archiveBytes)),
		SHA256:   sha256Hex(archiveBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := env.manager.loadUpload(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(env.manager.uploadPartPath(session.ID)); err != nil {
		t.Fatal(err)
	}
	stored.StorageFormat = ""
	for index := 0; index < stored.TotalChunks; index++ {
		start := int64(index) * stored.ChunkSize
		end := min(start+stored.ChunkSize, int64(len(archiveBytes)))
		chunk := archiveBytes[start:end]
		writeTestFile(t, env.manager.uploadChunkPath(session.ID, index), chunk)
		stored.Received[index] = UploadChunk{
			Index:  index,
			Size:   int64(len(chunk)),
			SHA256: sha256Hex(chunk),
		}
	}
	if err := writeJSONAtomic(env.manager.uploadSidecar(session.ID), stored, 0o600); err != nil {
		t.Fatal(err)
	}

	imported, err := env.manager.FinalizeUpload(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	importedPath, _, err := env.manager.resolveBackup(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	importedBytes, err := os.ReadFile(importedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(importedBytes, archiveBytes) {
		t.Fatal("legacy chunks changed while migrating to the part file")
	}
	if _, err := os.Stat(env.manager.uploadDir(session.ID)); !os.IsNotExist(err) {
		t.Fatalf("completed legacy upload session still exists: %v", err)
	}
}

func TestChunkUploadCorruptPartDropsOnlyFailedChunkForRetry(t *testing.T) {
	env := newTestBackupEnv(t)
	writeTestFile(t, filepath.Join(env.root, "uploads", "retry.mp4"), []byte("retry-upload"))
	record := createAndWaitForBackup(t, env.manager)
	sourcePath, _, err := env.manager.resolveBackup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := env.manager.BeginUpload(context.Background(), BeginUploadInput{
		FileName: "retry.zip",
		Size:     int64(len(archiveBytes)),
		SHA256:   sha256Hex(archiveBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.TotalChunks != 1 {
		t.Fatalf("test archive chunks = %d, want 1", session.TotalChunks)
	}
	chunkHash := sha256Hex(archiveBytes)
	if _, err := env.manager.PutChunk(
		context.Background(), session.ID, 0, chunkHash, bytes.NewReader(archiveBytes),
	); err != nil {
		t.Fatal(err)
	}
	part, err := os.OpenFile(env.manager.uploadPartPath(session.ID), os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.WriteAt([]byte{archiveBytes[0] ^ 0xff}, 0); err != nil {
		_ = part.Close()
		t.Fatal(err)
	}
	if err := part.Sync(); err != nil {
		_ = part.Close()
		t.Fatal(err)
	}
	if err := part.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.FinalizeUpload(context.Background(), session.ID); err == nil ||
		!strings.Contains(err.Error(), "暂存文件中校验失败") {
		t.Fatalf("corrupt part finalize error = %v", err)
	}
	status, err := env.manager.UploadStatus(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "uploading" || len(status.Received) != 0 {
		t.Fatalf("status after corrupt chunk = %+v", status)
	}
	if _, err := env.manager.PutChunk(
		context.Background(), session.ID, 0, chunkHash, bytes.NewReader(archiveBytes),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.FinalizeUpload(context.Background(), session.ID); err != nil {
		t.Fatalf("finalize after retransmitting corrupt chunk: %v", err)
	}
}

func TestRestoreSwitchesAllDataPreservesTargetRuntimeConfigAndClearsSessions(t *testing.T) {
	env := newTestBackupEnv(t)
	ctx := context.Background()
	previewPath := mediaasset.PreviewPath(env.cfg.Storage.LocalPreviewDir, "video-1")
	thumbPath := mediaasset.ThumbnailPath(env.cfg.Storage.LocalPreviewDir, "video-1")
	writeTestFile(t, previewPath, []byte("old-preview"))
	writeTestFile(t, thumbPath, []byte("old-thumb"))
	writeTestFile(t, filepath.Join(env.root, "uploads", "old.mp4"), []byte("old-upload"))
	scriptPath := filepath.Join(env.root, "crawler-scripts", "restore.py")
	writeTestFile(t, scriptPath, []byte("print('restore')"))
	now := time.Now()
	if err := env.cat.UpsertVideo(ctx, &catalog.Video{
		ID:                "video-1",
		DriveID:           "drive-1",
		FileID:            "remote-1",
		FileName:          "video.mp4",
		Title:             "Backup Video",
		ThumbnailURL:      "/p/thumb/video-1",
		PreviewLocal:      previewPath,
		PreviewStatus:     "ready",
		FingerprintStatus: "ready",
		PublishedAt:       now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	missingPreviewPath := mediaasset.PreviewPath(env.cfg.Storage.LocalPreviewDir, "video-missing-assets")
	if err := env.cat.UpsertVideo(ctx, &catalog.Video{
		ID:            "video-missing-assets",
		DriveID:       "drive-1",
		FileID:        "remote-missing-assets",
		FileName:      "missing.mp4",
		Title:         "Missing Assets",
		ThumbnailURL:  "/p/thumb/video-missing-assets",
		PreviewLocal:  missingPreviewPath,
		PreviewStatus: "ready",
		PublishedAt:   now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.cat.UpsertDrive(ctx, &catalog.Drive{
		ID:     "crawler-1",
		Kind:   "scriptcrawler",
		Name:   "Crawler",
		RootID: "/",
		Credentials: map[string]string{
			"script_path": scriptPath,
		},
	}); err != nil {
		t.Fatal(err)
	}
	missingLocal := filepath.Join(env.root, "missing-local-disk")
	if err := env.cat.UpsertDrive(ctx, &catalog.Drive{
		ID:     "local-missing",
		Kind:   "localstorage",
		Name:   "Missing Local",
		RootID: "/",
		Credentials: map[string]string{
			"path": missingLocal,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateSession(ctx, "old-session", time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateVideoShare(ctx, "old-share", "old-share-token", "video-1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := env.cat.CreateRemoteUploadJob(
		ctx,
		"old-remote-job",
		"https://source.example/private.mp4",
		"private.mp4",
		"Remote Job",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	record := createAndWaitForBackup(t, env.manager)

	if _, err := env.cat.CreateUser(ctx, "post-backup-user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, previewPath, []byte("new-preview"))
	writeTestFile(t, filepath.Join(env.root, "uploads", "new.mp4"), []byte("new-upload"))
	env.cfg.Server.Listen = "0.0.0.0:7777"
	env.cfg.Server.AllowedOrigins = []string{"https://target.example"}
	env.cfg.Preview.FFmpegPath = "/target/bin/ffmpeg"
	env.cfg.Preview.FFprobePath = "/target/bin/ffprobe"
	env.cfg.Server.Admin.Username = "target-admin"
	env.cfg.Server.Admin.Password = "target-password"
	writeTestConfig(t, env.configPath, env.cfg)

	report, err := env.manager.PrepareRestore(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	restoreProgress := env.manager.restoreProgressSnapshot()
	if restoreProgress == nil || restoreProgress.Phase != "ready" ||
		restoreProgress.ProcessedBytes != report.Manifest.TotalSize ||
		restoreProgress.ProcessedFiles != report.Manifest.FileCount {
		t.Fatalf("restore progress after prepare = %+v", restoreProgress)
	}
	if report.VerificationStatus != "verified" || len(report.LocalStorageWarnings) == 0 ||
		len(report.MissingAssets) < 2 {
		t.Fatalf("restore report = %+v", report)
	}
	env.manager.Close()
	if err := env.cat.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPendingRestore(env.root)
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("pending restore was not applied")
	}
	restoredCatalog, err := catalog.Open(env.cfg.Storage.DBPath)
	if err != nil {
		_ = RollbackAppliedRestore(applied, err)
		t.Fatal(err)
	}
	defer restoredCatalog.Close()
	if err := CommitAppliedRestore(applied); err != nil {
		t.Fatal(err)
	}
	if _, err := restoredCatalog.GetUserByUsername(ctx, "post-backup-user"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("post-backup user survived restore: %v", err)
	}
	if valid, _, err := restoredCatalog.ValidateSession(ctx, "old-session"); err != nil || valid {
		t.Fatalf("restored session valid=%v err=%v, want cleared", valid, err)
	}
	video, err := restoredCatalog.GetVideo(ctx, "video-1")
	if err != nil {
		t.Fatal(err)
	}
	if video.PreviewStatus != "ready" || video.PreviewLocal != previewPath ||
		video.ThumbnailURL != "/p/thumb/video-1" {
		t.Fatalf("restored video asset state = %+v", video)
	}
	missingVideo, err := restoredCatalog.GetVideo(ctx, "video-missing-assets")
	if err != nil {
		t.Fatal(err)
	}
	if missingVideo.PreviewStatus != "pending" || missingVideo.PreviewLocal != "" ||
		missingVideo.ThumbnailURL != "" {
		t.Fatalf("missing restored assets were not marked pending: %+v", missingVideo)
	}
	pendingThumbnails, err := restoredCatalog.ListVideosByThumbnailStatus(ctx, "drive-1", "pending", 100)
	if err != nil {
		t.Fatal(err)
	}
	missingThumbnailPending := false
	for _, candidate := range pendingThumbnails {
		if candidate.ID == missingVideo.ID {
			missingThumbnailPending = true
			break
		}
	}
	if !missingThumbnailPending {
		t.Fatal("missing restored thumbnail was not marked pending")
	}
	remoteJob, err := restoredCatalog.GetRemoteUploadJob(ctx, "old-remote-job")
	if err != nil {
		t.Fatal(err)
	}
	if remoteJob.State != catalog.RemoteUploadCanceled || remoteJob.SourceURL != "" ||
		!remoteJob.CancelRequested {
		t.Fatalf("restored remote upload was not canceled and scrubbed: %+v", remoteJob)
	}
	if err := restoredCatalog.CreateVideoShare(
		ctx,
		"old-share",
		"old-share-token",
		"video-1",
		now.Add(time.Hour),
	); err != nil {
		t.Fatalf("restored one-time shares were not cleared: %v", err)
	}
	if body, err := os.ReadFile(previewPath); err != nil || string(body) != "old-preview" {
		t.Fatalf("restored preview = %q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(env.root, "uploads", "new.mp4")); !os.IsNotExist(err) {
		t.Fatalf("post-backup upload still exists: %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(env.root, "uploads", "old.mp4")); err != nil ||
		string(body) != "old-upload" {
		t.Fatalf("restored upload = %q err=%v", body, err)
	}
	restoredConfig, err := config.Load(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if restoredConfig.Server.Listen != "0.0.0.0:7777" ||
		len(restoredConfig.Server.AllowedOrigins) != 1 ||
		restoredConfig.Server.AllowedOrigins[0] != "https://target.example" {
		t.Fatalf("target network config was not preserved: %+v", restoredConfig.Server)
	}
	if restoredConfig.Preview.FFmpegPath != "/target/bin/ffmpeg" ||
		restoredConfig.Preview.FFprobePath != "/target/bin/ffprobe" {
		t.Fatalf("target executable paths were not preserved: %+v", restoredConfig.Preview)
	}
	if restoredConfig.Server.Admin.Username != "target-admin" ||
		restoredConfig.Server.Admin.Password != "target-password" {
		t.Fatalf("target administrator config was not preserved: %+v", restoredConfig.Server.Admin)
	}
	localDrive, err := restoredCatalog.GetDrive(ctx, "local-missing")
	if err != nil {
		t.Fatal(err)
	}
	if localDrive.Status != "disconnected" || !strings.Contains(localDrive.LastError, missingLocal) {
		t.Fatalf("missing local drive state = %+v", localDrive)
	}
}

func TestRestoreLegacyBackupPreservesAllTargetAdministrators(t *testing.T) {
	env := newTestBackupEnv(t)
	ctx := context.Background()

	sourceAdminID, err := env.cat.CreateUser(
		ctx,
		"legacy-source-admin",
		"legacy-source-admin-hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	sourceUserID, err := env.cat.CreateUser(ctx, "restored-user", "restored-user-hash", "user")
	if err != nil {
		t.Fatal(err)
	}
	conflictingUserID, err := env.cat.CreateUser(
		ctx,
		"TARGET-OWNER",
		"source-conflicting-user-hash",
		"user",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateSession(ctx, "legacy-source-session", time.Hour, sourceAdminID); err != nil {
		t.Fatal(err)
	}

	legacyDatabasePath := filepath.Join(env.root, "legacy-source.sqlite")
	if err := env.cat.BackupTo(ctx, legacyDatabasePath); err != nil {
		t.Fatal(err)
	}
	legacyDatabase, err := os.ReadFile(legacyDatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacyConfig, err := os.ReadFile(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	const legacyID = "video-site-91-full-legacy-administrator-test"
	writePayloadArchive(
		t,
		filepath.Join(env.manager.backupDir, legacyID+".zip"),
		[]rawZipEntry{
			{name: "payload/database.sqlite", body: legacyDatabase},
			{name: "payload/config.yaml", body: legacyConfig},
		},
	)

	if err := env.cat.DeleteSession(ctx, "legacy-source-session"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{sourceAdminID, sourceUserID, conflictingUserID} {
		if err := env.cat.DeleteUser(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	targetOwnerID, err := env.cat.CreateUser(
		ctx,
		"target-owner",
		"target-owner-password-hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	targetAuditorID, err := env.cat.CreateUser(
		ctx,
		"target-auditor",
		"target-auditor-password-hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.SetUserBanned(ctx, targetAuditorID, true); err != nil {
		t.Fatal(err)
	}
	targetOwner, err := env.cat.GetUserByID(ctx, targetOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	targetAuditor, err := env.cat.GetUserByID(ctx, targetAuditorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateSession(ctx, "target-session", time.Hour, targetOwnerID); err != nil {
		t.Fatal(err)
	}
	env.cfg.Server.Admin.Username = "target-config-admin"
	env.cfg.Server.Admin.Password = "target-config-password"
	writeTestConfig(t, env.configPath, env.cfg)

	if _, err := env.manager.PrepareRestore(ctx, legacyID); err != nil {
		t.Fatal(err)
	}
	env.manager.Close()
	if err := env.cat.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPendingRestore(env.root)
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("pending legacy restore was not applied")
	}
	restoredCatalog, err := catalog.Open(env.cfg.Storage.DBPath)
	if err != nil {
		_ = RollbackAppliedRestore(applied, err)
		t.Fatal(err)
	}
	defer restoredCatalog.Close()
	if err := CommitAppliedRestore(applied); err != nil {
		t.Fatal(err)
	}

	if _, err := restoredCatalog.GetUserByUsername(ctx, "legacy-source-admin"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("legacy source administrator survived restore: %v", err)
	}
	restoredUser, err := restoredCatalog.GetUserByUsername(ctx, "restored-user")
	if err != nil {
		t.Fatal(err)
	}
	if restoredUser.Role != "user" || restoredUser.Password != "restored-user-hash" {
		t.Fatalf("source regular user was not restored: %+v", restoredUser)
	}
	restoredOwner, err := restoredCatalog.GetUserByUsername(ctx, "TARGET-OWNER")
	if err != nil {
		t.Fatal(err)
	}
	if restoredOwner.Username != targetOwner.Username ||
		restoredOwner.Password != targetOwner.Password ||
		restoredOwner.Role != "admin" ||
		restoredOwner.Banned != targetOwner.Banned ||
		restoredOwner.CreatedAt != targetOwner.CreatedAt {
		t.Fatalf("target owner was not preserved over conflicting source user: %+v", restoredOwner)
	}
	restoredAuditor, err := restoredCatalog.GetUserByUsername(ctx, targetAuditor.Username)
	if err != nil {
		t.Fatal(err)
	}
	if restoredAuditor.Password != targetAuditor.Password ||
		restoredAuditor.Role != "admin" ||
		restoredAuditor.Banned != targetAuditor.Banned ||
		restoredAuditor.CreatedAt != targetAuditor.CreatedAt {
		t.Fatalf("target auditor was not preserved: %+v", restoredAuditor)
	}
	admins, err := restoredCatalog.ListAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 2 {
		t.Fatalf("restored administrator count = %d, want 2", len(admins))
	}
	for _, token := range []string{"legacy-source-session", "target-session"} {
		if valid, _, err := restoredCatalog.ValidateSession(ctx, token); err != nil || valid {
			t.Fatalf("restored session %q valid=%v err=%v, want cleared", token, valid, err)
		}
	}
	restoredConfig, err := config.Load(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if restoredConfig.Server.Admin.Username != "target-config-admin" ||
		restoredConfig.Server.Admin.Password != "target-config-password" {
		t.Fatalf("target administrator config was not preserved: %+v", restoredConfig.Server.Admin)
	}
}

func TestAppliedRestoreCanRollBackToOldData(t *testing.T) {
	env := newTestBackupEnv(t)
	writeTestFile(t, filepath.Join(env.root, "uploads", "state.txt"), []byte("backup-state"))
	record := createAndWaitForBackup(t, env.manager)
	writeTestFile(t, filepath.Join(env.root, "uploads", "state.txt"), []byte("current-state"))
	if _, err := env.cat.CreateUser(context.Background(), "current-user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	currentOwnerID, err := env.cat.CreateUser(
		context.Background(),
		"current-owner",
		"current-owner-password-hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	currentAuditorID, err := env.cat.CreateUser(
		context.Background(),
		"current-auditor",
		"current-auditor-password-hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.SetUserBanned(context.Background(), currentAuditorID, true); err != nil {
		t.Fatal(err)
	}
	currentOwner, err := env.cat.GetUserByID(context.Background(), currentOwnerID)
	if err != nil {
		t.Fatal(err)
	}
	currentAuditor, err := env.cat.GetUserByID(context.Background(), currentAuditorID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.cat.CreateSession(
		context.Background(),
		"rollback-admin-session",
		time.Hour,
		currentOwnerID,
	); err != nil {
		t.Fatal(err)
	}
	env.cfg.Server.Admin.Username = "rollback-config-owner"
	env.cfg.Server.Admin.Password = "rollback-config-password"
	writeTestConfig(t, env.configPath, env.cfg)
	if _, err := env.manager.PrepareRestore(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	env.manager.Close()
	if err := env.cat.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPendingRestore(env.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := RollbackAppliedRestore(applied, errors.New("injected migration failure")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(env.root, "uploads", "state.txt"))
	if err != nil || string(body) != "current-state" {
		t.Fatalf("rolled back file = %q err=%v", body, err)
	}
	oldCatalog, err := catalog.Open(env.cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer oldCatalog.Close()
	if _, err := oldCatalog.GetUserByUsername(context.Background(), "current-user"); err != nil {
		t.Fatalf("current database was not restored after rollback: %v", err)
	}
	for _, expected := range []*catalog.User{currentOwner, currentAuditor} {
		actual, err := oldCatalog.GetUserByUsername(context.Background(), expected.Username)
		if err != nil {
			t.Fatalf("rolled back administrator %q is missing: %v", expected.Username, err)
		}
		if actual.Password != expected.Password ||
			actual.Role != expected.Role ||
			actual.Banned != expected.Banned ||
			actual.CreatedAt != expected.CreatedAt {
			t.Fatalf("rolled back administrator %q = %+v, want %+v", expected.Username, actual, expected)
		}
	}
	if valid, userID, err := oldCatalog.ValidateSession(
		context.Background(),
		"rollback-admin-session",
	); err != nil || !valid || userID != currentOwnerID {
		t.Fatalf("rolled back administrator session valid=%v userID=%d err=%v", valid, userID, err)
	}
	rolledBackConfig, err := config.Load(env.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBackConfig.Server.Admin.Username != "rollback-config-owner" ||
		rolledBackConfig.Server.Admin.Password != "rollback-config-password" {
		t.Fatalf("administrator config was not restored after rollback: %+v", rolledBackConfig.Server.Admin)
	}
}

func TestInterruptedRestoreRollbackResumesWithoutMixingData(t *testing.T) {
	env := newTestBackupEnv(t)
	statePath := filepath.Join(env.root, "uploads", "state.txt")
	writeTestFile(t, statePath, []byte("backup-state"))
	record := createAndWaitForBackup(t, env.manager)
	writeTestFile(t, statePath, []byte("current-state"))
	if _, err := env.cat.CreateUser(context.Background(), "current-user", "hash", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.manager.PrepareRestore(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	env.manager.Close()
	if err := env.cat.Close(); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyPendingRestore(env.root)
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("pending restore was not applied")
	}

	// Simulate a crash after one operation has already put its old target back,
	// but before that operation's rolledback state reached the marker.
	interrupted := applied.marker
	var uploadOperation *restoreSwitch
	for index := range interrupted.Operations {
		if interrupted.Operations[index].Name == "uploads" {
			uploadOperation = &interrupted.Operations[index]
			break
		}
	}
	if uploadOperation == nil || !uploadOperation.HadTarget {
		t.Fatalf("uploads restore operation = %+v", uploadOperation)
	}
	if err := os.Rename(uploadOperation.Target, uploadOperation.Staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(uploadOperation.Rollback, uploadOperation.Target); err != nil {
		t.Fatal(err)
	}
	interrupted.State = "rolling-back"
	interrupted.LastError = "injected crash during rollback"
	if err := writeJSONAtomic(applied.markerPath, interrupted, 0o600); err != nil {
		t.Fatal(err)
	}

	reapplied, err := ApplyPendingRestore(env.root)
	if err != nil {
		t.Fatal(err)
	}
	if reapplied != nil {
		t.Fatal("interrupted rollback unexpectedly reapplied restored data")
	}
	body, err := os.ReadFile(statePath)
	if err != nil || string(body) != "current-state" {
		t.Fatalf("resumed rollback file = %q err=%v", body, err)
	}
	currentCatalog, err := catalog.Open(env.cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer currentCatalog.Close()
	if _, err := currentCatalog.GetUserByUsername(context.Background(), "current-user"); err != nil {
		t.Fatalf("resumed rollback left the restored database active: %v", err)
	}
}

type rawZipEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func readZipFile(t *testing.T, file *zip.File) []byte {
	t.Helper()
	if file == nil {
		t.Fatal("ZIP entry is missing")
	}
	input, err := file.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	body, err := io.ReadAll(input)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeRawZip(t *testing.T, path string, entries []rawZipEntry) {
	t.Helper()
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func writePayloadArchive(t *testing.T, archivePath string, payload []rawZipEntry) {
	t.Helper()
	manifest := Manifest{
		FormatVersion:  FormatVersion,
		AppVersion:     "v1.2.3",
		CreatedAt:      time.Now().UTC(),
		SourceDataRoot: "/source/data",
		Included:       []string{"database", "config"},
	}
	for _, entry := range payload {
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:   entry.name,
			Size:   int64(len(entry.body)),
			SHA256: sha256Hex(entry.body),
			Mode:   0o600,
		})
		manifest.TotalSize += int64(len(entry.body))
	}
	manifest.FileCount = len(manifest.Files)
	manifestBody, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entries := append([]rawZipEntry{{name: manifestName, body: manifestBody}}, payload...)
	writeRawZip(t, archivePath, entries)
}

func rewriteZip(t *testing.T, source, destination string, mutate func(string, []byte) []byte) {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	output, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for _, sourceFile := range reader.File {
		header := sourceFile.FileHeader
		target, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if sourceFile.FileInfo().IsDir() {
			continue
		}
		input, err := sourceFile.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(input)
		_ = input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(mutate(sourceFile.Name, body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
