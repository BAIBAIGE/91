package backuptransfer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeTargetURLSupportsHTTPAndHTTPS(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "plain HTTP IPv4 and port",
			raw:  "  http://192.0.2.10:9192/  ",
			want: "http://192.0.2.10:9192",
		},
		{
			name: "plain HTTP IPv6 and port",
			raw:  "http://[2001:db8::10]:9192",
			want: "http://[2001:db8::10]:9192",
		},
		{
			name: "HTTPS hostname",
			raw:  "https://backup.example.com/",
			want: "https://backup.example.com",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeTargetURL(test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("normalizeTargetURL(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNormalizeTargetURLRejectsUnsafeOrUnsupportedURLs(t *testing.T) {
	tests := []struct {
		raw     string
		message string
	}{
		{raw: "192.0.2.10:9192", message: "完整的目标服务器"},
		{raw: "ftp://192.0.2.10:9192", message: "仅支持 HTTP 或 HTTPS"},
		{raw: "http://user:password@192.0.2.10:9192", message: "不能包含凭据"},
		{raw: "http://192.0.2.10:9192/admin", message: "不能包含额外路径"},
		{raw: "http://192.0.2.10:9192?token=value", message: "不能包含凭据、查询参数或片段"},
		{raw: "http://192.0.2.10:9192#fragment", message: "不能包含凭据、查询参数或片段"},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			if got, err := normalizeTargetURL(test.raw); err == nil {
				t.Fatalf("normalizeTargetURL(%q) = %q, want error", test.raw, got)
			} else if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("normalizeTargetURL(%q) error = %q, want %q", test.raw, err, test.message)
			}
		})
	}
}

func TestUploadRangesUsesParallelStreamingRequestsWithoutRangeDigests(t *testing.T) {
	const (
		rangeSize   = int64(1 << 20)
		totalRanges = 3
	)
	var active atomic.Int32
	var maximum atomic.Int32
	var started atomic.Int32
	var requests atomic.Int32
	var invalidHeader atomic.Bool
	allStarted := make(chan struct{})
	var startOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.Contains(r.URL.Path, "/ranges/") {
			http.NotFound(w, r)
			return
		}
		requests.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maximum.Load()
			if current <= seen || maximum.CompareAndSwap(seen, current) {
				break
			}
		}
		if r.Header.Get("Content-Digest") != "" || r.Header.Get("X-Chunk-SHA256") != "" ||
			r.Header.Get("Content-Range") == "" {
			invalidHeader.Store(true)
		}
		if started.Add(1) == totalRanges {
			startOnce.Do(func() { close(allStarted) })
		}
		select {
		case <-allStarted:
		case <-time.After(5 * time.Second):
			http.Error(w, "parallel requests did not start", http.StatusGatewayTimeout)
			return
		}
		written, err := io.Copy(io.Discard, r.Body)
		if err != nil || written != rangeSize {
			http.Error(w, "invalid range body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "range-source-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(rangeSize * totalRanges); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	outgoingDir := filepath.Join(t.TempDir(), "outgoing")
	if err := os.MkdirAll(outgoingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := storedTransferJob{TransferJob: TransferJob{
		ID:          id,
		BackupID:    "source-backup",
		BackupName:  "source.zip",
		TargetURL:   server.URL,
		State:       TransferUploading,
		Size:        rangeSize * totalRanges,
		SHA256:      strings.Repeat("b", 64),
		CreatedAt:   now,
		UpdatedAt:   now,
		Cancellable: true,
	}, ReceiveToken: "test-token"}
	manager := &Manager{
		client:           server.Client(),
		now:              func() time.Time { return time.Now().UTC() },
		outgoingDir:      outgoingDir,
		jobs:             map[string]*storedTransferJob{id: &job},
		outgoingProgress: make(map[string]*streamProgress),
		incomingProgress: make(map[string]*streamProgress),
	}
	status := ImportStatus{
		TransferID:  id,
		State:       ImportUploading,
		Size:        job.Size,
		SHA256:      job.SHA256,
		RangeSize:   rangeSize,
		TotalRanges: totalRanges,
		Committed:   []IndexRange{},
	}
	if err := manager.uploadRanges(
		context.Background(), id, job, file, status, map[int]bool{}, totalRanges,
	); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != totalRanges || maximum.Load() != totalRanges {
		t.Fatalf("requests = %d, maximum concurrency = %d", requests.Load(), maximum.Load())
	}
	if invalidHeader.Load() {
		t.Fatal("parallel range request included a digest or omitted Content-Range")
	}
	result := manager.jobs[id].TransferJob
	if result.ProcessedBytes != job.Size || result.ProcessedRanges != totalRanges {
		t.Fatalf("final progress = %+v", result)
	}
}

func TestRemoteCancellationRetriesAfterRestartBeforeScrubbingToken(t *testing.T) {
	var requests atomic.Int32
	var invalidRequest atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete ||
			r.URL.Path != PeerBackupPath+"/imports/"+strings.Repeat("c", 32) {
			invalidRequest.Store(true)
			http.NotFound(w, r)
			return
		}
		if requests.Add(1) == 1 {
			http.Error(w, "target still draining ranges", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	backups, root := newReceiverTestBackupManager(t, time.Now)
	stateRoot := filepath.Join(root, "peer-transfer")
	config := Config{
		Backups:    backups,
		RootDir:    stateRoot,
		HTTPClient: server.Client(),
	}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := strings.Repeat("c", 32)
	token := receiveTokenPrefix + strings.Repeat("d", 32) + "_" + strings.Repeat("e", 64)
	job := &storedTransferJob{
		TransferJob: TransferJob{
			ID:          id,
			BackupID:    "source-backup",
			BackupName:  "source-backup.zip",
			TargetURL:   server.URL,
			State:       TransferCanceled,
			Size:        1,
			SHA256:      strings.Repeat("f", 64),
			CreatedAt:   now.Add(-time.Minute),
			UpdatedAt:   now,
			FinishedAt:  now,
			Cancellable: false,
		},
		ReceiveToken:    token,
		CancelRequested: true,
	}
	manager.mu.Lock()
	manager.jobs[id] = job
	if err := manager.saveJobLocked(job); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	manager.mu.Unlock()

	firstCtx, stopFirst := context.WithCancel(context.Background())
	manager.Start(firstCtx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		pending := manager.jobs[id].CancelRequested &&
			manager.jobs[id].ReceiveToken == token &&
			!manager.jobs[id].NextAttemptAt.IsZero()
		manager.mu.Unlock()
		if requests.Load() == 1 && pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requests.Load() != 1 {
		t.Fatalf("initial cancellation requests = %d, want 1", requests.Load())
	}
	stopFirst()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := manager.Shutdown(shutdownCtx); err != nil {
		shutdownCancel()
		t.Fatal(err)
	}
	shutdownCancel()

	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.mu.Lock()
	reloaded := *restarted.jobs[id]
	restarted.mu.Unlock()
	if !reloaded.CancelRequested || reloaded.ReceiveToken != token {
		t.Fatalf("reloaded pending cancellation = %+v", reloaded)
	}
	secondCtx, stopSecond := context.WithCancel(context.Background())
	restarted.Start(secondCtx)
	defer func() {
		stopSecond()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := restarted.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown restarted cancellation worker: %v", err)
		}
	}()

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		restarted.mu.Lock()
		finished := !restarted.jobs[id].CancelRequested && restarted.jobs[id].ReceiveToken == ""
		restarted.mu.Unlock()
		if requests.Load() >= 2 && finished {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if invalidRequest.Load() || requests.Load() != 2 {
		t.Fatalf("cancellation requests = %d, invalid = %v", requests.Load(), invalidRequest.Load())
	}
	stateBody, err := os.ReadFile(filepath.Join(stateRoot, "outgoing", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBody), token) || strings.Contains(string(stateBody), "cancelRequested") {
		t.Fatal("confirmed cancellation retained its receive credential or pending marker")
	}
}
