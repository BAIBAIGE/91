package applog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
)

func TestRedactionPreservesDiagnosticContext(t *testing.T) {
	for _, input := range []string{
		`GET https://alice:private-password@example.com/path?deviceCode=private-code&loginUuid=private-uuid&dir=42`,
		`Authorization: Bearer private-token`,
		`Cookie: session=private-session; other=private-cookie`,
		`{"access_token":"private-access","refresh_token":"private-refresh","password":"private-pass"}`,
		`GET /api/status?token=private-token&dir=42`,
		`request failed: Get "https://example.com/path?signature=private-signature": deadline exceeded`,
		`https://example.com/file?X-Amz-Signature=private-signature&X-Amz-Credential=private-credential&auth_key=private-auth`,
		`{"code":500,"nested":{"clientSecret":"private-client","\u0074oken":"private-escaped"}}`,
	} {
		got := Redact(input)
		if strings.Contains(got, "private-") {
			t.Fatalf("secret remained: %s", got)
		}
	}
	url := RedactURL("/api/status?device%43ode=private-code&dir=42&access_token=private-token")
	if strings.Contains(url, "private-") || !strings.Contains(url, "dir=42") {
		t.Fatal(url)
	}
	if got := Redact("[scanner] drive=drive-1 file=42: permission denied"); got != "[scanner] drive=drive-1 file=42: permission denied" {
		t.Fatal(got)
	}
	if got := Redact(`{"code":500,"id":12345678901234567890}`); !strings.Contains(got, `"code":500`) || !strings.Contains(got, `"id":12345678901234567890`) {
		t.Fatal(got)
	}
}

func TestStructuredEventsKeepLevelContextAndMultilineStack(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	var operational bytes.Buffer
	logger := log.New(Output(&operational, store), "", log.LstdFlags)
	LogEntry(logger, Entry{
		Level: LevelError, Message: "panic", Error: "permission denied", Stack: "goroutine 1:\nhandler()\n/file.go:42",
		Fields: Fields{RequestID: "req-1", TaskID: "task-1", DriveID: "drive-1", FileID: "file-1"},
	})
	LogEntry(logger, Entry{Level: LevelInfo, Message: "retry queue ready"})
	result := queryTestLogs(t, store, Query{Level: LevelError, Limit: 10})
	if len(result.Entries) != 1 {
		t.Fatalf("entries=%d", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.RequestID != "req-1" || entry.FileID != "file-1" || !strings.Contains(entry.Stack, "handler()\n/file.go:42") {
		t.Fatalf("entry=%+v", entry)
	}
	logger.Printf("[http] panic: example\ngoroutine 2:\nhandler()\n/file.go:12")
	result = queryTestLogs(t, store, Query{Level: LevelError, Limit: 10})
	if len(result.Entries) != 2 || !strings.Contains(result.Entries[1].Message, "handler()\n/file.go:12") {
		t.Fatalf("multiline entry=%+v", result)
	}
}

func TestLogOutputRedactsBothSinks(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	var operational bytes.Buffer
	logger := log.New(Output(&operational, store), "", 0)
	logger.Print(`request failed https://example.com/?deviceCode=private-code`)
	LogEntry(logger, Entry{Level: LevelError, Error: `Authorization: Bearer private-token`, Message: "request failed"})
	if strings.Contains(operational.String(), "private-") {
		t.Fatal("operational output leaked credentials")
	}
	for _, entry := range queryTestLogs(t, store, Query{Limit: 10}).Entries {
		if strings.Contains(entry.Message+entry.Error, "private-") {
			t.Fatal("durable output leaked credentials")
		}
	}
}

func TestContextFieldsKeepTaskAndRequestIdentity(t *testing.T) {
	ctx := WithFields(context.Background(), Fields{RequestID: "request-1"})
	first := NewTask(ctx, "scan", "drive-1")
	second := NewTask(ctx, "scan", "drive-1")
	fields := ContextFields(WithFields(first, Fields{FileID: "file-1", Stage: "lookup"}))
	if fields.RequestID != "request-1" || fields.DriveID != "drive-1" || fields.FileID != "file-1" || fields.TaskID == "" || fields.TaskID == ContextFields(second).TaskID {
		t.Fatalf("fields=%+v", fields)
	}
}

func TestCanceledOperationsAreNotErrorEvents(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	previous := log.Writer()
	log.SetOutput(store.Writer(SourceApplication))
	defer log.SetOutput(previous)
	Error(context.Background(), "Task interrupted", fmt.Errorf("request: %w", context.Canceled), Fields{TaskID: "task-1"})
	entry := queryTestLogs(t, store, Query{Limit: 1}).Entries[0]
	if entry.Level != LevelWarning || entry.TaskID != "task-1" {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestQueryHistoryAndExportSearchAllRetainedFiles(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 2048, 1<<20, 8192)
	start := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 30; i++ {
		appendTestEntry(t, store, Entry{Timestamp: start.Add(time.Duration(i) * time.Second), Level: LevelError, Message: fmt.Sprintf("event-%02d", i), Fields: Fields{DriveID: "drive-1"}, Error: "lookup failure"})
	}
	latest := queryTestLogs(t, store, Query{Limit: 10, Search: "drive-1"})
	if !latest.HasMore || len(latest.Entries) != 10 {
		t.Fatalf("latest=%+v", latest)
	}
	older := queryTestLogs(t, store, Query{Limit: 10, Before: latest.Entries[0].ID})
	if !older.HasMore || older.Entries[9].ID >= latest.Entries[0].ID {
		t.Fatalf("older=%+v", older)
	}
	oldest := queryTestLogs(t, store, Query{Limit: 10, Before: older.Entries[0].ID})
	if oldest.HasMore || oldest.Entries[0].Message != "event-00" {
		t.Fatalf("oldest=%+v", oldest)
	}
	var output bytes.Buffer
	if err := store.Export(context.Background(), Query{Limit: 1, Search: "drive-1", From: start.Add(5 * time.Second), To: start.Add(20 * time.Second)}, &output); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "event-"); got != 16 {
		t.Fatalf("exported=%d", got)
	}
	if !strings.Contains(output.String(), `driveId="drive-1"`) || !strings.Contains(output.String(), "lookup failure") {
		t.Fatal(output.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Export(ctx, Query{}, &output); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestFileWriteFailureIsVisibleAndRecoveryClearsAlert(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	if err := store.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(SourceApplication, "cannot write"); err == nil {
		t.Fatal("expected write failure")
	}
	if health := store.Health(); health.LastError == "" || health.FailedWrites != 1 {
		t.Fatalf("health=%+v", health)
	}
	if result := queryTestLogs(t, store, Query{}); result.WriteHealth.LastError == "" {
		t.Fatal("write health not visible after failure")
	}
	if err := store.Append(SourceApplication, "recovered"); err != nil {
		t.Fatal(err)
	}
	if health := queryTestLogs(t, store, Query{}).WriteHealth; health.LastError != "" || health.LastSuccessAt == nil || health.FailedWrites != 1 {
		t.Fatalf("health=%+v", health)
	}
}
