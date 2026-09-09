package applog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryRejectsCanceledContext(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Query(ctx, Query{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("query error = %v, want cancellation", err)
	}
}

type cancelDuringLogScan struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelDuringLogScan) Err() error {
	c.checks++
	if c.checks == 4 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestQueriesStopWhenCanceledDuringScan(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 1<<20, 4<<20, 8192)
	appendTestEntry(t, store, Entry{Message: "before cursor"})
	cursor := queryTestLogs(t, store, Query{}).NextCursor
	for i := 0; i < 20; i++ {
		appendTestEntry(t, store, Entry{Message: "after cursor"})
	}
	for _, query := range []Query{
		{Limit: 10}, {Limit: 10, Before: 20}, {Limit: 10, Cursor: cursor},
		{Limit: 10, Cursor: "invalid"}, {Limit: 10, Search: "not-found"},
	} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelDuringLogScan{Context: base, cancel: cancel}
		_, err := store.Query(ctx, query)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("query %+v error = %v, want cancellation during scan", query, err)
		}
	}
}

func TestFilteredQueriesRedactLegacyRecordsBeforeSearchAndOutput(t *testing.T) {
	directory := t.TempDir()
	legacy := Entry{
		ID: 1, Timestamp: time.Now().UTC(), Source: SourceHTTP, Level: LevelError, Method: MethodGET,
		Message: "request failed: token=private-token", Error: "password=private-password",
		Path: "/api/test?code=private-code", Fields: Fields{RequestID: "request-1"},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, activeLogFileName), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, directory, 1<<20, 4<<20, 8192)
	files, _, _, err := store.snapshotFiles()
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := newLogCursor(files[0], 0)
	closeLogFileSnapshots(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []Query{
		{Limit: 10}, {Limit: 10, Before: 2}, {Limit: 10, Cursor: cursor},
		{Limit: 10, Source: SourceHTTP, Level: LevelError, Method: MethodGET},
		{Limit: 10, Search: "[redacted]"},
	} {
		result := queryTestLogs(t, store, query)
		encoded, err := json.Marshal(result.Entries)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 1 || bytes.Contains(encoded, []byte("private-")) {
			t.Fatalf("query %+v exposed or lost legacy record: %s", query, encoded)
		}
	}
	if result := queryTestLogs(t, store, Query{Search: "private-token"}); len(result.Entries) != 0 {
		t.Fatal("search must not match unredacted credentials")
	}
	var output bytes.Buffer
	if err := store.Export(context.Background(), Query{Search: "[redacted]"}, &output); err != nil {
		t.Fatal(err)
	}
	if output.Len() == 0 || strings.Contains(output.String(), "private-") {
		t.Fatalf("export exposed or lost legacy record: %s", output.String())
	}
}

func TestSmallLogBatchesKeepCompleteHistoryAvailable(t *testing.T) {
	store := openTestStore(t, t.TempDir(), 4096, 1<<20, 8192)
	for i := 0; i < 105; i++ {
		appendTestEntry(t, store, Entry{Source: SourceApplication, Level: LevelError, Message: "matching record"})
		appendTestEntry(t, store, Entry{Source: SourceHTTP, Level: LevelInfo, Message: "other record"})
	}
	var before uint64
	seen := make(map[uint64]bool)
	for {
		page := queryTestLogs(t, store, Query{Limit: 10, Source: SourceApplication, Level: LevelError, Before: before})
		for _, entry := range page.Entries {
			if seen[entry.ID] || (before != 0 && entry.ID >= before) {
				t.Fatalf("duplicate or out-of-order entry %d before %d", entry.ID, before)
			}
			seen[entry.ID] = true
		}
		if !page.HasMore {
			break
		}
		if len(page.Entries) != 10 {
			t.Fatalf("page with older history has %d entries", len(page.Entries))
		}
		before = page.Entries[0].ID
	}
	if len(seen) != 105 {
		t.Fatalf("paged entries = %d, want 105", len(seen))
	}
}
