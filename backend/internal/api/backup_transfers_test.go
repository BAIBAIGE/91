package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/video-site/backend/internal/auth"
	"github.com/video-site/backend/internal/backup"
	"github.com/video-site/backend/internal/backuptransfer"
	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/config"
)

type transferTestBackupEnv struct {
	root    string
	catalog *catalog.Catalog
	backups *backup.Manager
}

type failFirstChunkResponseTransport struct {
	base          http.RoundTripper
	chunkRequests atomic.Int32
	failed        atomic.Bool
}

func (transport *failFirstChunkResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil || request.Method != http.MethodPut || !strings.Contains(request.URL.Path, "/chunks/") {
		return response, err
	}
	transport.chunkRequests.Add(1)
	if !transport.failed.CompareAndSwap(false, true) {
		return response, nil
	}
	// Model a connection that disappears after the target durably accepted a
	// chunk but before the source received its response.
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, io.ErrUnexpectedEOF
}

func newTransferTestBackupEnv(t *testing.T) *transferTestBackupEnv {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		Server: config.Server{
			Listen: "127.0.0.1:9192",
			Admin: config.Admin{
				Username: "transfer-admin",
				Password: "transfer-password",
			},
		},
		Storage: config.Storage{
			DBPath:          filepath.Join(root, "video-site.db"),
			LocalPreviewDir: filepath.Join(root, "previews"),
		},
	}
	if err := os.MkdirAll(cfg.Storage.LocalPreviewDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configBody, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configPath, configBody, 0o600); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(cfg.Storage.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := backup.NewManager(backup.Config{
		Catalog:    cat,
		AppConfig:  cfg,
		ConfigPath: configPath,
		AppVersion: "v1.0.0",
	})
	if err != nil {
		_ = cat.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.Close()
		_ = cat.Close()
	})
	return &transferTestBackupEnv{root: root, catalog: cat, backups: manager}
}

func createTransferTestBackup(t *testing.T, manager *backup.Manager) backup.BackupRecord {
	t.Helper()
	if _, err := manager.Create(context.Background(), backup.BackupSelection{UserInfo: true}); err != nil {
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
			if len(result.Backups) != 1 {
				t.Fatalf("backup count = %d, want 1", len(result.Backups))
			}
			if result.Backups[0].VerificationStatus != "verified" {
				t.Fatalf("backup verification = %q", result.Backups[0].VerificationStatus)
			}
			return result.Backups[0]
		}
		if status != nil && (status.State == "failed" || status.State == "canceled") {
			t.Fatalf("backup creation ended as %s: %s", status.State, status.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for source backup")
	return backup.BackupRecord{}
}

func waitForTransferJob(t *testing.T, manager *backuptransfer.Manager, id string) backuptransfer.TransferJob {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range manager.ListTransfers() {
			if job.ID != id {
				continue
			}
			switch job.State {
			case backuptransfer.TransferCompleted:
				return job
			case backuptransfer.TransferFailed, backuptransfer.TransferCanceled:
				t.Fatalf("transfer ended as %s: %s", job.State, job.Error)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for server transfer")
	return backuptransfer.TransferJob{}
}

func waitForTransferState(
	t *testing.T,
	manager *backuptransfer.Manager,
	id string,
	expected string,
) backuptransfer.TransferJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, job := range manager.ListTransfers() {
			if job.ID == id && job.State == expected {
				return job
			}
			if job.ID == id && (job.State == backuptransfer.TransferFailed ||
				job.State == backuptransfer.TransferCanceled) {
				t.Fatalf("transfer ended as %s while waiting for %s: %s", job.State, expected, job.Error)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for transfer state %s", expected)
	return backuptransfer.TransferJob{}
}

func TestPeerBackupTransferSupportsPlainHTTP(t *testing.T) {
	source := newTransferTestBackupEnv(t)
	target := newTransferTestBackupEnv(t)
	sourceRecord := createTransferTestBackup(t, source.backups)

	targetTransfers, err := backuptransfer.New(backuptransfer.Config{
		Backups: target.backups,
		RootDir: filepath.Join(target.root, "peer-transfer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	targetAPI := &AdminServer{
		Auth:            &auth.Authenticator{Catalog: target.catalog},
		Backups:         target.backups,
		BackupTransfers: targetTransfers,
	}
	router := chi.NewRouter()
	targetAPI.Register(router)
	targetServer := httptest.NewServer(router)
	defer targetServer.Close()

	receiveToken, err := targetTransfers.GenerateReceiveToken(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sourceTransfers, err := backuptransfer.New(backuptransfer.Config{
		Backups: source.backups,
		RootDir: filepath.Join(source.root, "peer-transfer"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext, stopTransfers := context.WithCancel(context.Background())
	sourceTransfers.Start(runContext)
	defer func() {
		stopTransfers()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sourceTransfers.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown HTTP source transfers: %v", err)
		}
	}()

	created, err := sourceTransfers.CreateTransfer(context.Background(), backuptransfer.CreateTransferInput{
		BackupID:     sourceRecord.ID,
		TargetURL:    targetServer.URL,
		ReceiveToken: receiveToken.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.TargetURL != targetServer.URL || !strings.HasPrefix(created.TargetURL, "http://") {
		t.Fatalf("plain HTTP target URL = %q", created.TargetURL)
	}
	completed := waitForTransferJob(t, sourceTransfers, created.ID)
	targetRecord, err := target.backups.BackupRecord(completed.TargetBackupID)
	if err != nil {
		t.Fatal(err)
	}
	if !targetRecord.Imported || targetRecord.VerificationStatus != "verified" ||
		!strings.EqualFold(targetRecord.SHA256, sourceRecord.SHA256) {
		t.Fatalf("plain HTTP target backup = %+v", targetRecord)
	}
}

func TestPeerBackupTransferImportsDirectlyAndRecoversIdempotentReceipt(t *testing.T) {
	source := newTransferTestBackupEnv(t)
	target := newTransferTestBackupEnv(t)
	sourceRecord := createTransferTestBackup(t, source.backups)

	targetStateRoot := filepath.Join(target.root, "peer-transfer")
	targetTransfers, err := backuptransfer.New(backuptransfer.Config{
		Backups: target.backups,
		RootDir: targetStateRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetAPI := &AdminServer{
		Auth:            &auth.Authenticator{Catalog: target.catalog},
		Backups:         target.backups,
		BackupTransfers: targetTransfers,
	}
	router := chi.NewRouter()
	targetAPI.Register(router)
	targetServer := httptest.NewTLSServer(router)
	defer targetServer.Close()

	unauthorizedRequest, err := http.NewRequest(
		http.MethodGet,
		targetServer.URL+"/peer/v1/backups/capabilities",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResponse, err := targetServer.Client().Do(unauthorizedRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, unauthorizedResponse.Body)
	_ = unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized peer status = %d", unauthorizedResponse.StatusCode)
	}
	if unauthorizedResponse.Header.Get("WWW-Authenticate") != `Bearer realm="backup-transfer"` {
		t.Fatalf("WWW-Authenticate = %q", unauthorizedResponse.Header.Get("WWW-Authenticate"))
	}

	receiveToken, err := targetTransfers.GenerateReceiveToken(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sourceStateRoot := filepath.Join(source.root, "peer-transfer")
	targetHTTPClient := targetServer.Client()
	flakyTransport := &failFirstChunkResponseTransport{base: targetHTTPClient.Transport}
	sourceHTTPClient := *targetHTTPClient
	sourceHTTPClient.Transport = flakyTransport
	sourceTransferConfig := backuptransfer.Config{
		Backups:    source.backups,
		RootDir:    sourceStateRoot,
		HTTPClient: &sourceHTTPClient,
	}
	initialSourceTransfers, err := backuptransfer.New(sourceTransferConfig)
	if err != nil {
		t.Fatal(err)
	}
	initialRunContext, stopInitialTransfers := context.WithCancel(context.Background())
	defer stopInitialTransfers()
	initialSourceTransfers.Start(initialRunContext)
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := initialSourceTransfers.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown initial source transfers: %v", err)
		}
	}()

	created, err := initialSourceTransfers.CreateTransfer(context.Background(), backuptransfer.CreateTransferInput{
		BackupID:     sourceRecord.ID,
		TargetURL:    targetServer.URL,
		ReceiveToken: receiveToken.Token,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTransferState(t, initialSourceTransfers, created.ID, backuptransfer.TransferRetrying)
	if _, err := initialSourceTransfers.CreateTransfer(
		context.Background(),
		backuptransfer.CreateTransferInput{
			BackupID:     sourceRecord.ID,
			TargetURL:    targetServer.URL,
			ReceiveToken: receiveToken.Token,
		},
	); !errors.Is(err, backuptransfer.ErrTransferBusy) {
		t.Fatalf("second concurrent transfer error = %v, want ErrTransferBusy", err)
	}
	stopInitialTransfers()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := initialSourceTransfers.Shutdown(shutdownContext); err != nil {
		shutdownCancel()
		t.Fatal(err)
	}
	shutdownCancel()

	// Reload the durable source job while the target already owns the first
	// chunk. The new worker must trust the target's received ranges and avoid
	// sending that chunk a second time.
	resumedSourceTransfers, err := backuptransfer.New(sourceTransferConfig)
	if err != nil {
		t.Fatal(err)
	}
	resumedRunContext, stopResumedTransfers := context.WithCancel(context.Background())
	defer stopResumedTransfers()
	resumedSourceTransfers.Start(resumedRunContext)
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := resumedSourceTransfers.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown resumed source transfers: %v", err)
		}
	}()
	completed := waitForTransferJob(t, resumedSourceTransfers, created.ID)
	if completed.ProcessedBytes != sourceRecord.Size || completed.TargetBackupID == "" {
		t.Fatalf("completed transfer = %+v", completed)
	}
	if completed.Attempts != 1 || flakyTransport.chunkRequests.Load() != 1 {
		t.Fatalf(
			"resume attempts = %d, chunk requests = %d; want one lost response without retransmission",
			completed.Attempts,
			flakyTransport.chunkRequests.Load(),
		)
	}
	targetRecord, err := target.backups.BackupRecord(completed.TargetBackupID)
	if err != nil {
		t.Fatal(err)
	}
	if !targetRecord.Imported || targetRecord.VerificationStatus != "verified" ||
		!strings.EqualFold(targetRecord.SHA256, sourceRecord.SHA256) {
		t.Fatalf("target backup = %+v", targetRecord)
	}
	targetList, err := target.backups.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if targetList.PendingRestore {
		t.Fatal("peer import unexpectedly scheduled an automatic restore")
	}

	// The target persists only a token hash, and the source scrubs its raw
	// token as soon as a terminal receipt has been stored.
	for _, statePath := range []string{
		filepath.Join(targetStateRoot, "receiver.json"),
		filepath.Join(sourceStateRoot, "outgoing", created.ID+".json"),
	} {
		stateBody, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(stateBody), receiveToken.Token) {
			t.Fatalf("raw receive token remained in %s", statePath)
		}
	}
	publicBody, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicBody), receiveToken.Token) {
		t.Fatal("public transfer status exposed the receive token")
	}

	// Reopening the receiver state simulates a service restart after a lost
	// finalize response. The same request must return the original receipt and
	// must not publish a second backup artifact.
	restartedTarget, err := backuptransfer.New(backuptransfer.Config{
		Backups: target.backups,
		RootDir: targetStateRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := restartedTarget.FinalizeImport(
		context.Background(),
		receiveToken.Token,
		created.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.State != backuptransfer.ImportCompleted || receipt.Record == nil ||
		receipt.Record.ID != completed.TargetBackupID {
		t.Fatalf("recovered receipt = %+v", receipt)
	}
	targetList, err = target.backups.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(targetList.Backups) != 1 {
		t.Fatalf("target backup count after repeated finalize = %d, want 1", len(targetList.Backups))
	}

	otherRequest := backuptransfer.ImportRequest{
		TransferID:     strings.Repeat("1", 32),
		SourceServerID: strings.Repeat("2", 32),
		BackupID:       sourceRecord.ID,
		FileName:       sourceRecord.Name,
		Size:           sourceRecord.Size,
		SHA256:         sourceRecord.SHA256,
		FormatVersion:  backup.FormatVersion,
	}
	if _, err := restartedTarget.BeginImport(
		context.Background(), receiveToken.Token, otherRequest,
	); !errors.Is(err, backuptransfer.ErrTokenBound) {
		t.Fatalf("reuse bound token error = %v, want ErrTokenBound", err)
	}
}
