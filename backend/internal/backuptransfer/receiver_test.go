package backuptransfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/video-site/backend/internal/backup"
	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

func newReceiverTestBackupManager(
	t *testing.T,
	now func() time.Time,
) (*backup.Manager, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Storage: config.Storage{
			DBPath:          filepath.Join(root, "video-site.db"),
			LocalPreviewDir: filepath.Join(root, "previews"),
		},
	}
	if err := os.MkdirAll(cfg.Storage.LocalPreviewDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, []byte("storage: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := backup.NewManager(backup.Config{
		Catalog:        cat,
		AppConfig:      cfg,
		ConfigPath:     configPath,
		AppVersion:     "v1.0.0",
		Now:            now,
		AvailableBytes: func(string) (int64, error) { return 1 << 40, nil },
	})
	if err != nil {
		_ = cat.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.Close()
		_ = cat.Close()
	})
	return manager, root
}

func TestBeginImportRecreatesExpiredStagingAfterTokenWasRefreshed(t *testing.T) {
	current := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	backups, root := newReceiverTestBackupManager(t, now)
	stateRoot := filepath.Join(root, "peer-transfer")
	receiver, err := New(Config{Backups: backups, RootDir: stateRoot, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	token, err := receiver.GenerateReceiveToken(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request := ImportRequest{
		TransferID:     strings.Repeat("1", 32),
		SourceServerID: strings.Repeat("2", 32),
		BackupID:       "video-site-91-full-source",
		FileName:       "video-site-91-full-source.zip",
		Size:           1,
		SHA256:         strings.Repeat("a", 64),
		FormatVersion:  backup.FormatVersion,
	}
	initial, err := receiver.BeginImport(context.Background(), token.Token, request)
	if err != nil {
		t.Fatal(err)
	}

	current = current.Add(70 * time.Hour)
	if _, err := receiver.ImportStatus(context.Background(), token.Token, request.TransferID); err != nil {
		t.Fatalf("refresh bound receive token: %v", err)
	}
	current = current.Add(3 * time.Hour)
	recreated, err := receiver.BeginImport(context.Background(), token.Token, request)
	if err != nil {
		t.Fatal(err)
	}
	if recreated.State != ImportUploading || !recreated.ExpiresAt.After(initial.ExpiresAt) {
		t.Fatalf("recreated import = %+v, initial expiry = %s", recreated, initial.ExpiresAt)
	}

	// The replacement receipt and refreshed token survive a target restart.
	restarted, err := New(Config{Backups: backups, RootDir: stateRoot, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := restarted.BeginImport(context.Background(), token.Token, request)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.TransferID != request.TransferID || !resumed.ExpiresAt.Equal(recreated.ExpiresAt) {
		t.Fatalf("resumed import = %+v, recreated = %+v", resumed, recreated)
	}
	stateBody, err := os.ReadFile(filepath.Join(stateRoot, "receiver.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBody), token.Token) {
		t.Fatal("receiver persisted the raw pairing token")
	}
}

func TestImmediatelyRevokedReceiveTokenSurvivesStateReloadAsRevoked(t *testing.T) {
	current := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return current }
	backups, root := newReceiverTestBackupManager(t, now)
	stateRoot := filepath.Join(root, "peer-transfer")
	receiver, err := New(Config{Backups: backups, RootDir: stateRoot, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	token, err := receiver.GenerateReceiveToken(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := receiver.RevokeReceiveToken(token.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(Config{Backups: backups, RootDir: stateRoot, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.AuthorizeReceiveToken(token.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("authorize revoked token = %v, want ErrUnauthorized", err)
	}
}
