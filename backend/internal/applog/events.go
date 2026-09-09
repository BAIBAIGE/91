package applog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"time"
)

// Fields identify the operation independently of its human-readable message.
type Fields struct {
	Component string `json:"component,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	DriveID   string `json:"driveId,omitempty"`
	VideoID   string `json:"videoId,omitempty"`
	FileID    string `json:"fileId,omitempty"`
	Stage     string `json:"stage,omitempty"`
}

type fieldsKey struct{}

var fallbackSequence atomic.Uint64

func WithFields(ctx context.Context, fields Fields) context.Context {
	return context.WithValue(ctx, fieldsKey{}, mergeFields(ContextFields(ctx), fields))
}

func ContextFields(ctx context.Context) Fields {
	if ctx == nil {
		return Fields{}
	}
	fields, _ := ctx.Value(fieldsKey{}).(Fields)
	return fields
}

func NewTask(ctx context.Context, component, driveID string) context.Context {
	return WithFields(ctx, Fields{Component: component, DriveID: driveID, TaskID: NewID()})
}

func NewID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return fmt.Sprintf("local-%x-%x", time.Now().UnixNano(), fallbackSequence.Add(1))
	}
	return hex.EncodeToString(id[:])
}

func mergeFields(base, next Fields) Fields {
	if next.Component != "" {
		base.Component = next.Component
	}
	if next.RequestID != "" {
		base.RequestID = next.RequestID
	}
	if next.TaskID != "" {
		base.TaskID = next.TaskID
	}
	if next.DriveID != "" {
		base.DriveID = next.DriveID
	}
	if next.VideoID != "" {
		base.VideoID = next.VideoID
	}
	if next.FileID != "" {
		base.FileID = next.FileID
	}
	if next.Stage != "" {
		base.Stage = next.Stage
	}
	return base
}

const eventPrefix = "@applog "

func Info(ctx context.Context, message string, fields Fields) {
	event(ctx, LevelInfo, message, nil, fields)
}
func Warn(ctx context.Context, message string, err error, fields Fields) {
	event(ctx, LevelWarning, message, err, fields)
}
func Error(ctx context.Context, message string, err error, fields Fields) {
	level := LevelError
	if errors.Is(err, context.Canceled) {
		level = LevelWarning
	}
	event(ctx, level, message, err, fields)
}

func event(ctx context.Context, level Level, message string, err error, fields Fields) {
	entry := Entry{Timestamp: time.Now(), Source: SourceApplication, Level: level, Message: message, Fields: mergeFields(ContextFields(ctx), fields)}
	if err != nil {
		entry.Error = err.Error()
	}
	LogEntry(log.Default(), entry)
}

func LogEntry(logger *log.Logger, entry Entry) {
	if logger == nil {
		logger = log.Default()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.Source == "" {
		entry.Source = SourceApplication
	}
	if entry.Level == "" {
		entry.Level = classify(entry.Source, entry.Status, entry.Message)
	}
	redactEntry(&entry)
	encoded, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		return
	}
	// The standard logger remains the single application sink, including in tests.
	logger.Print(eventPrefix + string(encoded))
}

func loggedEntry(source Source, text string) Entry {
	timestamp, message := splitTimestamp(strings.TrimRight(text, "\r\n"), time.Now())
	if strings.HasPrefix(message, eventPrefix) {
		var entry Entry
		if json.Unmarshal([]byte(strings.TrimPrefix(message, eventPrefix)), &entry) == nil && entry.Level != "" {
			return entry
		}
	}
	return Entry{Timestamp: timestamp, Source: source, Message: message}
}

// Output redacts both the operational stream and the durable copy. A failed
// operational sink must not prevent the independent file sink from being tried.
func Output(operational io.Writer, store *Store) io.Writer {
	return outputWriter{operational: operational, store: store}
}

type outputWriter struct {
	operational io.Writer
	store       *Store
}

func (w outputWriter) Write(p []byte) (int, error) {
	entry := loggedEntry(SourceApplication, string(p))
	redactEntry(&entry)
	var outputErr error
	if w.operational != nil {
		text := Redact(string(p))
		if strings.Contains(string(p), eventPrefix) {
			encoded, _ := json.Marshal(entry)
			text = string(encoded) + "\n"
		}
		_, outputErr = io.WriteString(w.operational, text)
	}
	if w.store != nil {
		if err := w.store.AppendEntry(entry); err != nil {
			return len(p), err
		}
	}
	return len(p), outputErr
}
