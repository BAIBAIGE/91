package applog

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func BenchmarkFilteredLogQuery(b *testing.B) {
	benchmarkFilteredLogQuery(b, "35us", "diagnostic context ")
}

func BenchmarkFilteredUnicodeLogQuery(b *testing.B) {
	benchmarkFilteredLogQuery(b, "35\u00b5s", "\u65e5\u5fd7\u4e0a\u4e0b\u6587 ")
}

func benchmarkFilteredLogQuery(b *testing.B, elapsed, diagnostic string) {
	directory := b.TempDir()
	file, err := os.Create(filepath.Join(directory, activeLogFileName))
	if err != nil {
		b.Fatal(err)
	}
	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	for id := uint64(1); id <= 30000; id++ {
		entry := Entry{
			ID: id, Timestamp: time.Unix(1700000000+int64(id), 0).UTC(),
			Source: SourceHTTP, Level: LevelInfo, Method: MethodGET, Status: 200,
			Path: "/api/settings/preview", Remote: "192.0.2.1", Bytes: 24, Elapsed: elapsed,
			Fields:  Fields{RequestID: "1b0e4c339cae75f3d37aa989e8cf6007"},
			Message: `"GET http://example.test:9191/api/settings/preview HTTP/1.1" from 192.0.2.1 - 200 24B in ` + elapsed + " " + strings.Repeat(diagnostic, 10),
		}
		if id%100 == 0 {
			entry.Source = SourceApplication
			entry.Level = LevelError
			entry.Message = "scan failed: diagnostic context"
		}
		if err := encoder.Encode(entry); err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		b.Fatal(err)
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}
	store, err := Open(Config{Directory: directory, MaxFileSizeBytes: 32 << 20, MaxTotalSizeBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	for _, benchmark := range []struct {
		name  string
		query Query
	}{
		{"Latest10000", Query{Source: SourceHTTP, Limit: 10000}},
		{"Latest1000", Query{Source: SourceHTTP, Limit: 1000}},
		{"RareSource", Query{Source: SourceApplication, Limit: 1000}},
		{"NoMatchingLevel", Query{Level: LevelWarning, Limit: 1000}},
		{"NoMatchingSearch", Query{Search: "not-present-in-logs", Limit: 1000}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := store.Query(context.Background(), benchmark.query); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
