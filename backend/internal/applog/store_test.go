package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T, directory string, maxFileSize, maxTotalSize int64, maxLineBytes int) *Store {
	t.Helper()
	store, err := Open(Config{
		Directory:         directory,
		MaxLineBytes:      maxLineBytes,
		MaxFileSizeBytes:  maxFileSize,
		MaxTotalSizeBytes: maxTotalSize,
	})
	if err != nil {
		t.Fatalf("open log store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func appendTestEntry(t *testing.T, store *Store, entry Entry) {
	t.Helper()
	if err := store.AppendEntry(entry); err != nil {
		t.Fatalf("append log entry: %v", err)
	}
}

func queryTestLogs(t *testing.T, store *Store, query Query) Snapshot {
	t.Helper()
	snapshot, err := store.Query(query)
	if err != nil {
		t.Fatalf("query logs: %v", err)
	}
	return snapshot
}

func TestStorePersistsStructuredEntriesAcrossRestart(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory, 1<<20, 4<<20, 1024)
	if err := store.Append(SourceApplication, "worker ready"); err != nil {
		t.Fatal(err)
	}
	appendTestEntry(t, store, Entry{
		Source:  SourceHTTP,
		Method:  MethodPOST,
		Status:  201,
		Path:    "/api/videos?q=ready",
		Remote:  "127.0.0.1:1234",
		Bytes:   42,
		Elapsed: "2ms",
		Message: `"POST /api/videos?q=ready" from 127.0.0.1:1234 - 201 42B in 2ms`,
	})

	before := queryTestLogs(t, store, Query{Method: MethodPOST, Limit: 10})
	if len(before.Entries) != 1 || before.Entries[0].Status != 201 || before.Entries[0].Path != "/api/videos?q=ready" {
		t.Fatalf("structured HTTP entry = %#v", before.Entries)
	}
	lastID := before.Entries[0].ID
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestStore(t, directory, 1<<20, 4<<20, 1024)
	after := queryTestLogs(t, reopened, Query{Limit: 10})
	if len(after.Entries) != 2 || after.Entries[1].ID != lastID {
		t.Fatalf("reopened entries = %#v", after.Entries)
	}
	if err := reopened.Append(SourceApplication, "after restart"); err != nil {
		t.Fatal(err)
	}
	latest := queryTestLogs(t, reopened, Query{Limit: 1})
	if len(latest.Entries) != 1 || latest.Entries[0].ID != lastID+1 {
		t.Fatalf("persistent id sequence = %#v", latest.Entries)
	}
}

func TestStoreCursorReturnsOnlyNewMatchingEntries(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 1024)
	_ = store.Append(SourceApplication, "worker ready")
	appendTestEntry(t, store, Entry{Source: SourceHTTP, Method: MethodGET, Status: 200, Path: "/one", Message: "GET /one"})

	initial := queryTestLogs(t, store, Query{Source: SourceHTTP, Limit: 10})
	if len(initial.Entries) != 1 || initial.NextCursor == "" {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	_ = store.Append(SourceApplication, "worker ignored by filter")
	appendTestEntry(t, store, Entry{Source: SourceHTTP, Method: MethodPOST, Status: 503, Path: "/two", Message: "POST /two"})

	incremental := queryTestLogs(t, store, Query{Source: SourceHTTP, Cursor: initial.NextCursor, Limit: 10})
	if incremental.Reset || len(incremental.Entries) != 1 {
		t.Fatalf("incremental snapshot = %#v", incremental)
	}
	if got := incremental.Entries[0]; got.Method != MethodPOST || got.Level != LevelError {
		t.Fatalf("incremental entry = %#v", got)
	}

	noChanges := queryTestLogs(t, store, Query{Source: SourceHTTP, Cursor: incremental.NextCursor, Limit: 10})
	if noChanges.Reset || len(noChanges.Entries) != 0 || noChanges.NextCursor != incremental.NextCursor {
		t.Fatalf("stable cursor snapshot = %#v", noChanges)
	}
}

func TestStoreCursorContinuesAcrossRotationAndRestart(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory, 420, 16<<10, 1024)
	_ = store.Append(SourceApplication, "before cursor")
	initial := queryTestLogs(t, store, Query{Limit: 10})

	for i := 0; i < 12; i++ {
		if err := store.Append(SourceApplication, fmt.Sprintf("rotated line %02d %s", i, strings.Repeat("x", 80))); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestStore(t, directory, 420, 16<<10, 1024)
	result := queryTestLogs(t, reopened, Query{Cursor: initial.NextCursor, Limit: 20})
	if result.Reset || len(result.Entries) != 12 {
		t.Fatalf("rotation snapshot reset=%v entries=%d: %#v", result.Reset, len(result.Entries), result.Entries)
	}
	for index, entry := range result.Entries {
		if !strings.Contains(entry.Message, fmt.Sprintf("line %02d", index)) {
			t.Fatalf("entry %d = %q", index, entry.Message)
		}
	}
}

func TestStoreClearInvalidatesCursorAndKeepsIDSequence(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 1024)
	_ = store.Append(SourceApplication, "before clear")
	initial := queryTestLogs(t, store, Query{Limit: 10})
	oldID := initial.Entries[0].ID
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	_ = store.Append(SourceApplication, "after clear")

	reset := queryTestLogs(t, store, Query{Cursor: initial.NextCursor, Limit: 10})
	if !reset.Reset || len(reset.Entries) != 1 || reset.Entries[0].Message != "after clear" {
		t.Fatalf("reset snapshot = %#v", reset)
	}
	if reset.Entries[0].ID != oldID+1 {
		t.Fatalf("id after clear = %d, want %d", reset.Entries[0].ID, oldID+1)
	}
}

func TestStoreEnforcesTotalRotatedFileSize(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory, 400, 900, 1024)
	for i := 0; i < 30; i++ {
		if err := store.Append(SourceApplication, fmt.Sprintf("line-%02d-%s", i, strings.Repeat("z", 90))); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	var rotated int
	for _, entry := range entries {
		if !isRuntimeLogFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
		if entry.Name() != activeLogFileName {
			rotated++
		}
	}
	if total > 900 {
		t.Fatalf("total log size = %d, want <= 900", total)
	}
	if rotated == 0 {
		t.Fatal("expected at least one retained rotated file")
	}
}

func TestStoreRepairsTrailingPartialJSONOnRestart(t *testing.T) {
	directory := t.TempDir()
	store := openTestStore(t, directory, 1<<20, 4<<20, 1024)
	_ = store.Append(SourceApplication, "complete")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(directory, activeLogFileName)
	file, err := os.OpenFile(activePath, os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"id":999,"message":"partial"}`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	reopened := openTestStore(t, directory, 1<<20, 4<<20, 1024)
	_ = reopened.Append(SourceApplication, "after repair")
	result := queryTestLogs(t, reopened, Query{Limit: 10})
	if len(result.Entries) != 2 || result.Entries[0].Message != "complete" || result.Entries[1].Message != "after repair" {
		t.Fatalf("repaired entries = %#v", result.Entries)
	}
}

func TestWriterParsesTimestampAndBoundsUTF8(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 5)
	writer := store.Writer(SourceApplication)
	if _, err := writer.Write([]byte("2026/08/02 12:34:56 你好世界\n")); err != nil {
		t.Fatal(err)
	}
	result := queryTestLogs(t, store, Query{Limit: 10})
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.Timestamp.In(time.Local).Format("2006/01/02 15:04:05") != "2026/08/02 12:34:56" {
		t.Fatalf("timestamp = %s", entry.Timestamp)
	}
	if entry.Message != "你… [truncated]" {
		t.Fatalf("message = %q", entry.Message)
	}
}

func TestStoreSupportsConcurrentWritesAndQueries(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 8<<20, 1024)
	var group sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			writer := store.Writer(SourceApplication)
			for i := 0; i < 50; i++ {
				_, _ = writer.Write([]byte(fmt.Sprintf("worker-%d line-%d\n", worker, i)))
				_, _ = store.Query(Query{Limit: 20, Search: "line"})
			}
		}(worker)
	}
	group.Wait()
	result := queryTestLogs(t, store, Query{Limit: 500})
	if len(result.Entries) != 300 {
		t.Fatalf("entries = %d, want 300", len(result.Entries))
	}
}

func TestClassifyDoesNotTreatSuccessfulSummariesAsErrors(t *testing.T) {
	tests := []struct {
		name    string
		source  Source
		status  int
		message string
		want    Level
	}{
		{name: "zero error summary", source: SourceApplication, message: "[scan] done scanned=5 errors=0", want: LevelInfo},
		{name: "nonzero errors", source: SourceApplication, message: "[scan] done scanned=5 errors=2", want: LevelWarning},
		{name: "real failure", source: SourceApplication, message: "[scan] attach failed: unavailable", want: LevelError},
		{name: "successful request", source: SourceHTTP, status: 200, message: "failed in path", want: LevelInfo},
		{name: "client error", source: SourceHTTP, status: 404, want: LevelWarning},
		{name: "server error", source: SourceHTTP, status: 503, want: LevelError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classify(test.source, test.status, test.message); got != test.want {
				t.Fatalf("classify() = %q, want %q", got, test.want)
			}
		})
	}
}
