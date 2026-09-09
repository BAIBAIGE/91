package applog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	logCursorVersion        = 1
	logCursorFingerprintMax = 4 * 1024
	defaultQueryLimit       = 500
)

type logCursor struct {
	Version         int    `json:"v"`
	File            string `json:"file"`
	Offset          int64  `json:"offset"`
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"modTimeUnixNano"`
	Fingerprint     string `json:"fingerprint"`
}

type logFileSnapshot struct {
	name            string
	file            *os.File
	size            int64
	boundary        int64
	modTimeUnixNano int64
}

func (s *Store) Query(ctx context.Context, query Query) (Snapshot, error) {
	if s == nil {
		return Snapshot{}, errors.New("log store is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if query.Limit <= 0 {
		query.Limit = defaultQueryLimit
	}
	query.Search = strings.ToLower(strings.TrimSpace(query.Search))

	files, storageBytes, maxStorageBytes, err := s.snapshotFiles()
	if err != nil {
		return Snapshot{}, err
	}
	defer closeLogFileSnapshots(files)

	base := Snapshot{
		Entries:         []Entry{},
		StorageBytes:    storageBytes,
		MaxStorageBytes: maxStorageBytes,
		WriteHealth:     s.Health(),
	}
	if strings.TrimSpace(query.Cursor) == "" {
		return queryLogTail(ctx, files, query, base)
	}

	cursor, err := decodeLogCursor(query.Cursor)
	if err != nil {
		base.Reset = true
		return queryLogTail(ctx, files, query, base)
	}
	startIndex, ok, err := locateLogCursor(files, cursor)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		base.Reset = true
		return queryLogTail(ctx, files, query, base)
	}

	entries, nextCursor, err := queryLogsFromCursor(ctx, files, startIndex, cursor, query)
	if err != nil {
		return Snapshot{}, err
	}
	base.Entries = entries
	base.Matched = len(entries)
	base.NextCursor = nextCursor
	return base, nil
}

func (s *Store) snapshotFiles() ([]logFileSnapshot, int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0, 0, errors.New("log store is closed")
	}

	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list log directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isRuntimeLogFile(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == activeLogFileName {
			return false
		}
		if names[j] == activeLogFileName {
			return true
		}
		return names[i] < names[j]
	})

	files := make([]logFileSnapshot, 0, len(names))
	var storageBytes int64
	for _, name := range names {
		path := filepath.Join(s.directory, name)
		file, err := os.Open(path)
		if err != nil {
			closeLogFileSnapshots(files)
			return nil, 0, 0, fmt.Errorf("open log file %s: %w", name, err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			closeLogFileSnapshots(files)
			return nil, 0, 0, fmt.Errorf("inspect log file %s: %w", name, err)
		}
		boundary, err := completeLogBoundary(file, info.Size())
		if err != nil {
			_ = file.Close()
			closeLogFileSnapshots(files)
			return nil, 0, 0, fmt.Errorf("find complete log boundary %s: %w", name, err)
		}
		files = append(files, logFileSnapshot{
			name:            name,
			file:            file,
			size:            info.Size(),
			boundary:        boundary,
			modTimeUnixNano: info.ModTime().UnixNano(),
		})
		storageBytes += info.Size()
	}
	return files, storageBytes, s.maxTotalSizeBytes, nil
}

func closeLogFileSnapshots(files []logFileSnapshot) {
	for i := range files {
		_ = files[i].file.Close()
	}
}

func queryLogTail(ctx context.Context, files []logFileSnapshot, query Query, result Snapshot) (Snapshot, error) {
	reversed := make([]Entry, 0, query.Limit)
	for i := len(files) - 1; i >= 0 && len(reversed) <= query.Limit; i-- {
		err := scanLogFileReverse(ctx, files[i], func(raw []byte) bool {
			entry, ok := decodeMatchingEntry(raw, query)
			if !ok {
				return true
			}
			reversed = append(reversed, entry)
			return len(reversed) <= query.Limit
		})
		if err != nil {
			return Snapshot{}, fmt.Errorf("read log file %s: %w", files[i].name, err)
		}
	}
	result.HasMore = len(reversed) > query.Limit
	if result.HasMore {
		reversed = reversed[:query.Limit]
	}
	entries := make([]Entry, len(reversed))
	for i := range reversed {
		entries[len(reversed)-1-i] = reversed[i]
	}
	result.Entries = entries
	result.Matched = len(entries)
	if len(files) > 0 {
		cursor, err := newLogCursor(files[len(files)-1], files[len(files)-1].boundary)
		if err != nil {
			return Snapshot{}, err
		}
		result.NextCursor = cursor
	}
	return result, nil
}

func scanLogFileReverse(ctx context.Context, file logFileSnapshot, visit func([]byte) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if file.boundary == 0 {
		return nil
	}
	const chunkSize = 32 * 1024
	position := file.boundary
	pending := make([]byte, 0, chunkSize)
	firstChunk := true
	for position > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		start := position - chunkSize
		if start < 0 {
			start = 0
		}
		chunk := make([]byte, position-start)
		n, err := file.file.ReadAt(chunk, start)
		if err != nil && err != io.EOF {
			return err
		}
		chunk = chunk[:n]
		data := make([]byte, 0, len(chunk)+len(pending))
		data = append(data, chunk...)
		data = append(data, pending...)
		end := len(data)
		if firstChunk && end > 0 && data[end-1] == '\n' {
			end--
		}
		firstChunk = false
		for end > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			index := bytes.LastIndexByte(data[:end], '\n')
			if index < 0 {
				break
			}
			line := bytes.TrimSuffix(data[index+1:end], []byte{'\r'})
			if len(line) > maxStructuredLogBytes {
				return fmt.Errorf("log record exceeds %d bytes", maxStructuredLogBytes)
			}
			if len(line) > 0 && !visit(line) {
				return nil
			}
			end = index
		}
		pending = append(pending[:0], data[:end]...)
		if len(pending) > maxStructuredLogBytes {
			return fmt.Errorf("log record exceeds %d bytes", maxStructuredLogBytes)
		}
		position = start
	}
	if len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		visit(bytes.TrimSuffix(pending, []byte{'\r'}))
	}
	return nil
}

func queryLogsFromCursor(ctx context.Context, files []logFileSnapshot, startIndex int, cursor logCursor, query Query) ([]Entry, string, error) {
	entries := make([]Entry, 0, query.Limit)
	currentFile := files[startIndex]
	currentOffset := cursor.Offset
	advanced := false

scanFiles:
	for i := startIndex; i < len(files); i++ {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		offset := int64(0)
		if i == startIndex {
			offset = cursor.Offset
		}
		if offset > files[i].boundary {
			return nil, "", errors.New("log cursor points beyond the complete file boundary")
		}
		if offset == files[i].boundary {
			continue
		}
		reader := io.NewSectionReader(files[i].file, offset, files[i].boundary-offset)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), maxStructuredLogBytes)
		position := offset
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			raw := scanner.Bytes()
			position += int64(len(raw)) + 1
			currentFile = files[i]
			currentOffset = position
			advanced = true
			entry, ok := decodeMatchingEntry(raw, query)
			if ok {
				entries = append(entries, entry)
				if len(entries) >= query.Limit {
					break scanFiles
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", fmt.Errorf("scan log file %s: %w", files[i].name, err)
		}
	}
	if !advanced {
		encoded, err := encodeLogCursor(cursor)
		return entries, encoded, err
	}
	nextCursor, err := newLogCursor(currentFile, currentOffset)
	if err != nil {
		return nil, "", err
	}
	return entries, nextCursor, nil
}

func entryMatchesMetadata(entry Entry, query Query) bool {
	if query.Before != 0 && entry.ID >= query.Before {
		return false
	}
	if !query.From.IsZero() && entry.Timestamp.Before(query.From) {
		return false
	}
	if !query.To.IsZero() && entry.Timestamp.After(query.To) {
		return false
	}
	if query.Source != "" && entry.Source != query.Source {
		return false
	}
	if query.Level != "" && entry.Level != query.Level {
		return false
	}
	if query.Method != "" && entry.Method != query.Method {
		return false
	}
	return true
}

func entryMatchesSearch(entry Entry, search string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		entry.Message,
		entry.Path,
		entry.Remote,
		entry.RequestID,
		entry.TaskID, entry.DriveID, entry.VideoID, entry.FileID, entry.Stage, entry.Component, entry.Error, entry.Stack,
		entry.Timestamp.Format(time.RFC3339), strconv.Itoa(entry.Status), string(entry.Level), string(entry.Source),
		string(entry.Method),
	}, " "))
	return strings.Contains(haystack, search)
}

func decodeEntry(raw []byte) (Entry, bool) {
	var entry Entry
	if len(raw) == 0 || len(raw) > maxStructuredLogBytes || json.Unmarshal(raw, &entry) != nil {
		return Entry{}, false
	}
	if entry.ID == 0 || entry.Timestamp.IsZero() {
		return Entry{}, false
	}
	if entry.Source != SourceApplication && entry.Source != SourceHTTP {
		return Entry{}, false
	}
	if entry.Level != LevelInfo && entry.Level != LevelWarning && entry.Level != LevelError {
		return Entry{}, false
	}
	return entry, true
}

func decodeMatchingEntry(raw []byte, query Query) (Entry, bool) {
	entry, ok := decodeEntry(raw)
	if !ok || !entryMatchesMetadata(entry, query) {
		return Entry{}, false
	}
	// Search and output still use sanitized text, including older on-disk logs.
	redactEntry(&entry)
	if query.Search != "" && !entryMatchesSearch(entry, query.Search) {
		return Entry{}, false
	}
	return entry, true
}

// Export reads a bounded snapshot of all retained files, not the viewer window.
func (s *Store) Export(ctx context.Context, query Query, output io.Writer) error {
	files, _, _, err := s.snapshotFiles()
	if err != nil {
		return err
	}
	defer closeLogFileSnapshots(files)
	query.Search = strings.ToLower(strings.TrimSpace(query.Search))
	for _, file := range files {
		reader := bufio.NewScanner(io.NewSectionReader(file.file, 0, file.boundary))
		reader.Buffer(make([]byte, 64*1024), maxStructuredLogBytes)
		for reader.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			entry, ok := decodeMatchingEntry(reader.Bytes(), query)
			if !ok {
				continue
			}
			if _, err := fmt.Fprintln(output, FormatEntry(entry)); err != nil {
				return err
			}
		}
		if err := reader.Err(); err != nil {
			return err
		}
	}
	return nil
}

func FormatEntry(entry Entry) string {
	redactEntry(&entry)
	parts := []string{entry.Timestamp.Format(time.RFC3339), "[" + string(entry.Level) + "]", "[" + string(entry.Source) + "]"}
	for _, field := range []struct{ key, value string }{
		{"component", entry.Component}, {"requestId", entry.RequestID}, {"taskId", entry.TaskID}, {"driveId", entry.DriveID}, {"videoId", entry.VideoID}, {"fileId", entry.FileID}, {"stage", entry.Stage},
	} {
		if field.value != "" {
			parts = append(parts, field.key+"="+strconv.Quote(field.value))
		}
	}
	parts = append(parts, entry.Message)
	if entry.Error != "" {
		parts = append(parts, "error="+strconv.Quote(entry.Error))
	}
	line := strings.Join(parts, " ")
	if entry.Stack != "" {
		line += "\n" + entry.Stack
	}
	return line
}

func (s *Store) recoverLastID() (uint64, error) {
	files, _, _, err := s.snapshotFiles()
	if err != nil {
		return 0, err
	}
	defer closeLogFileSnapshots(files)
	for i := len(files) - 1; i >= 0; i-- {
		var lastID uint64
		err := scanLogFileReverse(context.Background(), files[i], func(raw []byte) bool {
			entry, ok := decodeEntry(raw)
			if !ok {
				return true
			}
			lastID = entry.ID
			return false
		})
		if err != nil {
			return 0, err
		}
		if lastID > 0 {
			return lastID, nil
		}
	}
	return 0, nil
}

func locateLogCursor(files []logFileSnapshot, cursor logCursor) (int, bool, error) {
	if err := validateLogCursor(cursor); err != nil {
		return 0, false, nil
	}
	for i := range files {
		if files[i].name != cursor.File {
			continue
		}
		matches, err := logFileMatchesCursor(files[i], cursor)
		if err != nil {
			return 0, false, err
		}
		if matches {
			return i, true, nil
		}
		break
	}
	if cursor.File != activeLogFileName || cursor.Offset == 0 {
		return 0, false, nil
	}
	for i := len(files) - 1; i >= 0; i-- {
		if files[i].name == activeLogFileName {
			continue
		}
		matches, err := logFileMatchesCursor(files[i], cursor)
		if err != nil {
			return 0, false, err
		}
		if matches {
			return i, true, nil
		}
	}
	return 0, false, nil
}

func logFileMatchesCursor(file logFileSnapshot, cursor logCursor) (bool, error) {
	if file.boundary < cursor.Offset || file.size < cursor.Offset {
		return false, nil
	}
	if cursor.Offset == 0 && (file.size != cursor.Size || file.modTimeUnixNano != cursor.ModTimeUnixNano) {
		return false, nil
	}
	fingerprint, err := logFileFingerprint(file.file, cursor.Offset)
	if err != nil {
		return false, err
	}
	return fingerprint == cursor.Fingerprint, nil
}

func newLogCursor(file logFileSnapshot, offset int64) (string, error) {
	if offset < 0 || offset > file.boundary {
		return "", errors.New("invalid log cursor offset")
	}
	fingerprint, err := logFileFingerprint(file.file, offset)
	if err != nil {
		return "", err
	}
	return encodeLogCursor(logCursor{
		Version:         logCursorVersion,
		File:            file.name,
		Offset:          offset,
		Size:            file.size,
		ModTimeUnixNano: file.modTimeUnixNano,
		Fingerprint:     fingerprint,
	})
}

func encodeLogCursor(cursor logCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeLogCursor(raw string) (logCursor, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return logCursor{}, errors.New("empty log cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return logCursor{}, errors.New("invalid log cursor encoding")
	}
	var cursor logCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return logCursor{}, errors.New("invalid log cursor payload")
	}
	if err := validateLogCursor(cursor); err != nil {
		return logCursor{}, err
	}
	return cursor, nil
}

func validateLogCursor(cursor logCursor) error {
	if cursor.Version != logCursorVersion {
		return errors.New("unsupported log cursor version")
	}
	if !isRuntimeLogFile(cursor.File) || filepath.Base(cursor.File) != cursor.File {
		return errors.New("invalid log cursor file")
	}
	if cursor.Offset < 0 || cursor.Size < 0 || cursor.ModTimeUnixNano < 0 {
		return errors.New("invalid log cursor position")
	}
	if strings.TrimSpace(cursor.Fingerprint) == "" {
		return errors.New("invalid log cursor fingerprint")
	}
	return nil
}

func logFileFingerprint(file *os.File, boundary int64) (string, error) {
	if boundary < 0 {
		return "", errors.New("invalid log fingerprint boundary")
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if boundary > info.Size() {
		return "", errors.New("log fingerprint boundary exceeds file size")
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "runtime-log-cursor-v1:%d:", boundary)
	firstLength := minInt64(boundary, logCursorFingerprintMax)
	if err := writeFileRange(hash, file, 0, firstLength); err != nil {
		return "", err
	}
	tailLength := minInt64(boundary, logCursorFingerprintMax)
	tailStart := boundary - tailLength
	_, _ = fmt.Fprintf(hash, ":%d:", tailStart)
	if err := writeFileRange(hash, file, tailStart, tailLength); err != nil {
		return "", err
	}
	sum := hash.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum[:12]), nil
}

func writeFileRange(dst io.Writer, file *os.File, start, length int64) error {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, 32*1024)
	position := start
	remaining := length
	for remaining > 0 {
		chunk := minInt64(int64(len(buf)), remaining)
		n, err := file.ReadAt(buf[:chunk], position)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
			position += int64(n)
			remaining -= int64(n)
		}
		if err != nil && !(err == io.EOF && remaining == 0) {
			return err
		}
	}
	return nil
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
