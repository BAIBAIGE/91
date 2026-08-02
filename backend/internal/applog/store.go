package applog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DefaultMaxLineBytes      = 8 * 1024
	DefaultMaxFileSizeBytes  = 10 * 1024 * 1024
	DefaultMaxTotalSizeBytes = 200 * 1024 * 1024

	activeLogFileName     = "runtime.log"
	maxPathBytes          = 4 * 1024
	maxRequestIDBytes     = 256
	maxStructuredLogBytes = 128 * 1024
)

type Source string

const (
	SourceApplication Source = "application"
	SourceHTTP        Source = "http"
)

type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
)

type Method string

const (
	MethodGET     Method = "GET"
	MethodPOST    Method = "POST"
	MethodPUT     Method = "PUT"
	MethodPATCH   Method = "PATCH"
	MethodDELETE  Method = "DELETE"
	MethodOPTIONS Method = "OPTIONS"
	MethodHEAD    Method = "HEAD"
)

// Entry is one durable JSONL record. HTTP fields are captured directly by the
// request middleware so filtering never depends on parsing a rendered message.
type Entry struct {
	ID        uint64    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Source    Source    `json:"source"`
	Level     Level     `json:"level"`
	Method    Method    `json:"method,omitempty"`
	Status    int       `json:"status,omitempty"`
	Path      string    `json:"path,omitempty"`
	Remote    string    `json:"remote,omitempty"`
	Bytes     int       `json:"bytes,omitempty"`
	Elapsed   string    `json:"elapsed,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
	Message   string    `json:"message"`
}

type Query struct {
	Limit  int
	Cursor string
	Source Source
	Level  Level
	Method Method
	Search string
}

type Snapshot struct {
	Entries         []Entry `json:"entries"`
	Matched         int     `json:"matched"`
	StorageBytes    int64   `json:"storageBytes"`
	MaxStorageBytes int64   `json:"maxStorageBytes"`
	NextCursor      string  `json:"nextCursor,omitempty"`
	Reset           bool    `json:"reset,omitempty"`
}

type Config struct {
	Directory         string
	MaxLineBytes      int
	MaxFileSizeBytes  int64
	MaxTotalSizeBytes int64
}

// Store owns the active JSONL file and its rotated history. Writes are
// serialized, while queries read stable file descriptors capped at a captured
// complete-line boundary so they do not block application logging.
type Store struct {
	mu                sync.Mutex
	directory         string
	activePath        string
	file              *os.File
	activeSize        int64
	totalSize         int64
	nextID            uint64
	maxLineBytes      int
	maxFileSizeBytes  int64
	maxTotalSizeBytes int64
	closed            bool
	now               func() time.Time
}

func Open(cfg Config) (*Store, error) {
	directory := strings.TrimSpace(cfg.Directory)
	if directory == "" {
		return nil, errors.New("log directory is required")
	}
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve log directory: %w", err)
	}
	if cfg.MaxLineBytes <= 0 {
		cfg.MaxLineBytes = DefaultMaxLineBytes
	}
	if cfg.MaxFileSizeBytes <= 0 {
		cfg.MaxFileSizeBytes = DefaultMaxFileSizeBytes
	}
	if cfg.MaxTotalSizeBytes <= 0 {
		cfg.MaxTotalSizeBytes = DefaultMaxTotalSizeBytes
	}
	if cfg.MaxTotalSizeBytes < cfg.MaxFileSizeBytes {
		return nil, errors.New("maximum total log size must be at least the maximum file size")
	}
	if err := os.MkdirAll(absDirectory, 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}

	activePath := filepath.Join(absDirectory, activeLogFileName)
	file, err := os.OpenFile(activePath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("open active log file: %w", err)
	}
	if err := repairTrailingPartialRecord(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("repair active log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect active log file: %w", err)
	}

	store := &Store{
		directory:         absDirectory,
		activePath:        activePath,
		file:              file,
		activeSize:        info.Size(),
		maxLineBytes:      cfg.MaxLineBytes,
		maxFileSizeBytes:  cfg.MaxFileSizeBytes,
		maxTotalSizeBytes: cfg.MaxTotalSizeBytes,
		now:               time.Now,
	}
	if err := store.cleanupLocked(); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("enforce log size limit: %w", err)
	}
	lastID, err := store.recoverLastID()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("recover last log id: %w", err)
	}
	store.nextID = lastID
	return store, nil
}

func (s *Store) Directory() string {
	if s == nil {
		return ""
	}
	return s.directory
}

func (s *Store) Writer(source Source) io.Writer {
	return sourceWriter{store: s, source: source}
}

type sourceWriter struct {
	store  *Store
	source Source
}

func (w sourceWriter) Write(p []byte) (int, error) {
	if w.store == nil {
		return len(p), nil
	}
	text := strings.ReplaceAll(string(p), "\r\n", "\n")
	var firstErr error
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := w.store.Append(w.source, line); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return len(p), firstErr
}

func (s *Store) Append(source Source, line string) error {
	timestamp, message := splitTimestamp(line, time.Now())
	return s.AppendEntry(Entry{
		Timestamp: timestamp,
		Source:    source,
		Message:   message,
	})
}

func (s *Store) AppendEntry(entry Entry) error {
	if s == nil {
		return errors.New("log store is unavailable")
	}
	entry = s.normalizeEntry(entry)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return errors.New("log store is closed")
	}

	s.nextID++
	entry.ID = s.nextID
	record, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode log entry: %w", err)
	}
	record = append(record, '\n')
	if len(record) > maxStructuredLogBytes {
		return fmt.Errorf("encoded log entry exceeds %d bytes", maxStructuredLogBytes)
	}
	if s.activeSize > 0 && s.activeSize+int64(len(record)) > s.maxFileSizeBytes {
		if err := s.rotateLocked(); err != nil {
			return err
		}
	}
	written, err := s.file.Write(record)
	if err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}
	if written != len(record) {
		return io.ErrShortWrite
	}
	s.activeSize += int64(written)
	s.totalSize += int64(written)
	if s.totalSize > s.maxTotalSizeBytes {
		if err := s.cleanupLocked(); err != nil {
			return fmt.Errorf("enforce total log size: %w", err)
		}
	}
	return nil
}

func (s *Store) normalizeEntry(entry Entry) Entry {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	entry.Timestamp = entry.Timestamp.UTC()
	if entry.Source != SourceHTTP {
		entry.Source = SourceApplication
	}
	entry.Message = strings.Clone(truncateUTF8(strings.TrimSpace(entry.Message), s.maxLineBytes))
	entry.Path = strings.Clone(truncateUTF8(strings.TrimSpace(entry.Path), maxPathBytes))
	entry.Remote = strings.Clone(truncateUTF8(strings.TrimSpace(entry.Remote), maxPathBytes))
	entry.RequestID = strings.Clone(truncateUTF8(strings.TrimSpace(entry.RequestID), maxRequestIDBytes))
	entry.Elapsed = strings.Clone(truncateUTF8(strings.TrimSpace(entry.Elapsed), 128))
	if entry.Level == "" {
		entry.Level = classify(entry.Source, entry.Status, entry.Message)
	}
	if !validMethod(entry.Method) {
		entry.Method = ""
	}
	if entry.Status < 100 || entry.Status > 599 {
		entry.Status = 0
	}
	if entry.Bytes < 0 {
		entry.Bytes = 0
	}
	return entry
}

func (s *Store) rotateLocked() error {
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("sync active log before rotation: %w", err)
	}
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close active log before rotation: %w", err)
	}
	s.file = nil

	rotatedPath, err := s.nextRotatedPathLocked()
	if err != nil {
		return err
	}
	if err := os.Rename(s.activePath, rotatedPath); err != nil {
		_ = s.reopenActiveLocked()
		return fmt.Errorf("rotate active log: %w", err)
	}
	if err := s.reopenActiveLocked(); err != nil {
		return err
	}
	if err := s.cleanupLocked(); err != nil {
		return fmt.Errorf("clean rotated logs: %w", err)
	}
	return nil
}

func (s *Store) nextRotatedPathLocked() (string, error) {
	stamp := s.now().Format("2006-01-02T15-04-05.000000000")
	base := strings.TrimSuffix(activeLogFileName, filepath.Ext(activeLogFileName))
	ext := filepath.Ext(activeLogFileName)
	for attempt := 0; attempt < 1000; attempt++ {
		suffix := ""
		if attempt > 0 {
			suffix = fmt.Sprintf("-%d", attempt)
		}
		candidate := filepath.Join(s.directory, base+"-"+stamp+suffix+ext)
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("unable to allocate rotated log filename")
}

func (s *Store) reopenActiveLocked() error {
	file, err := os.OpenFile(s.activePath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("reopen active log file: %w", err)
	}
	s.file = file
	s.activeSize = 0
	return nil
}

func (s *Store) cleanupLocked() error {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return err
	}
	type candidate struct {
		path    string
		size    int64
		modTime time.Time
	}
	var total int64
	rotated := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isRuntimeLogFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		if entry.Name() != activeLogFileName {
			rotated = append(rotated, candidate{
				path:    filepath.Join(s.directory, entry.Name()),
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}
	}
	if total <= s.maxTotalSizeBytes {
		s.totalSize = total
		return nil
	}
	sort.Slice(rotated, func(i, j int) bool {
		if rotated[i].modTime.Equal(rotated[j].modTime) {
			return rotated[i].path < rotated[j].path
		}
		return rotated[i].modTime.Before(rotated[j].modTime)
	})
	for _, file := range rotated {
		if total <= s.maxTotalSizeBytes {
			break
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= file.size
	}
	s.totalSize = total
	return nil
}

func (s *Store) Clear() error {
	if s == nil {
		return errors.New("log store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.file == nil {
		return errors.New("log store is closed")
	}
	if err := s.file.Truncate(0); err != nil {
		return fmt.Errorf("truncate active log: %w", err)
	}
	if _, err := s.file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek active log: %w", err)
	}
	s.totalSize -= s.activeSize
	if s.totalSize < 0 {
		s.totalSize = 0
	}
	s.activeSize = 0
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == activeLogFileName || !isRuntimeLogFile(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if err := os.Remove(filepath.Join(s.directory, entry.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove rotated log %s: %w", entry.Name(), err)
		}
		if infoErr == nil {
			s.totalSize -= info.Size()
			if s.totalSize < 0 {
				s.totalSize = 0
			}
		}
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.file == nil {
		return nil
	}
	if err := s.file.Sync(); err != nil {
		_ = s.file.Close()
		s.file = nil
		return err
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func repairTrailingPartialRecord(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	boundary, err := completeLogBoundary(file, info.Size())
	if err != nil {
		return err
	}
	if boundary < info.Size() {
		if err := file.Truncate(boundary); err != nil {
			return err
		}
	}
	_, err = file.Seek(0, io.SeekEnd)
	return err
}

func completeLogBoundary(file *os.File, size int64) (int64, error) {
	if size <= 0 {
		return 0, nil
	}
	buf := make([]byte, 32*1024)
	for pos := size; pos > 0; {
		chunk := int64(len(buf))
		if pos < chunk {
			chunk = pos
		}
		pos -= chunk
		n, err := file.ReadAt(buf[:chunk], pos)
		if err != nil && err != io.EOF {
			return 0, err
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return pos + int64(i) + 1, nil
			}
		}
	}
	return 0, nil
}

func isRuntimeLogFile(name string) bool {
	if name == activeLogFileName {
		return true
	}
	ext := filepath.Ext(activeLogFileName)
	base := strings.TrimSuffix(activeLogFileName, ext)
	return strings.HasPrefix(name, base+"-") && strings.HasSuffix(name, ext)
}

func splitTimestamp(line string, fallback time.Time) (time.Time, string) {
	const layout = "2006/01/02 15:04:05"
	if len(line) > len(layout) && line[len(layout)] == ' ' {
		if parsed, err := time.ParseInLocation(layout, line[:len(layout)], time.Local); err == nil {
			return parsed, strings.TrimSpace(line[len(layout)+1:])
		}
	}
	return fallback, strings.TrimSpace(line)
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut] + "… [truncated]"
}

func classify(source Source, status int, message string) Level {
	if source == SourceHTTP {
		if status >= 500 {
			return LevelError
		}
		if status >= 400 {
			return LevelWarning
		}
		return LevelInfo
	}

	lower := strings.ToLower(message)
	if containsAny(lower,
		"[warn]", "warning", "warn:", " retry", "retry_", " skipping ", " skip ",
		" skipped:", " canceled ", " canceled:", " cancelled ", " cancelled:",
		" cooling down", " cooldown ", "cooldown=", "警告", "跳过", "重试", "取消",
	) {
		return LevelWarning
	}
	if containsAny(lower,
		"[error]", " error:", " error ", " failed:", " failed ",
		" failure:", " failure ", " fatal", " panic", "失败", "错误",
	) || strings.HasSuffix(lower, " error") || strings.HasSuffix(lower, " failed") {
		return LevelError
	}
	if hasPositiveCounter(lower, "errors=", "failed=", "failures=") {
		return LevelWarning
	}
	return LevelInfo
}

func hasPositiveCounter(value string, names ...string) bool {
	for _, name := range names {
		remaining := value
		for {
			index := strings.Index(remaining, name)
			if index < 0 {
				break
			}
			digits := remaining[index+len(name):]
			end := 0
			for end < len(digits) && digits[end] >= '0' && digits[end] <= '9' {
				end++
			}
			if end > 0 {
				count, _ := strconv.Atoi(digits[:end])
				if count > 0 {
					return true
				}
			}
			remaining = digits
		}
	}
	return false
}

func validMethod(method Method) bool {
	switch method {
	case MethodGET, MethodPOST, MethodPUT, MethodPATCH, MethodDELETE, MethodOPTIONS, MethodHEAD:
		return true
	default:
		return false
	}
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
