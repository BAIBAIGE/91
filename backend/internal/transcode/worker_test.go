package transcode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/video-site/backend/internal/drives"
)

type transcodeUploadTestDrive struct {
	entries     []drives.Entry
	listCalls   int
	uploadCalls int
	uploadFn    func(name string, data []byte, size int64) (string, error)
}

func (d *transcodeUploadTestDrive) Kind() string               { return "test" }
func (d *transcodeUploadTestDrive) ID() string                 { return "test-drive" }
func (d *transcodeUploadTestDrive) RootID() string             { return "root" }
func (d *transcodeUploadTestDrive) Init(context.Context) error { return nil }
func (d *transcodeUploadTestDrive) EnsureDir(context.Context, string) (string, error) {
	return "target", nil
}
func (d *transcodeUploadTestDrive) List(context.Context, string) ([]drives.Entry, error) {
	d.listCalls++
	return append([]drives.Entry(nil), d.entries...), nil
}
func (d *transcodeUploadTestDrive) Stat(context.Context, string) (*drives.Entry, error) {
	return nil, drives.ErrNotSupported
}
func (d *transcodeUploadTestDrive) StreamURL(context.Context, string) (*drives.StreamLink, error) {
	return nil, drives.ErrNotSupported
}
func (d *transcodeUploadTestDrive) Upload(_ context.Context, _ string, name string, r io.Reader, size int64) (string, error) {
	d.uploadCalls++
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if d.uploadFn != nil {
		return d.uploadFn(name, data, size)
	}
	return "uploaded", nil
}

func TestLocalSourcePath(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		wantPath string
		wantOK   bool
	}{
		{name: "plain absolute path", url: "/srv/media/a.mp4", wantPath: "/srv/media/a.mp4", wantOK: true},
		{name: "file scheme", url: "file:///srv/media/b.mkv", wantPath: "/srv/media/b.mkv", wantOK: true},
		{name: "http is remote", url: "http://cdn.example.com/x.mp4", wantOK: false},
		{name: "https is remote", url: "https://cdn.example.com/x.mp4", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, ok := localSourcePath(&drives.StreamLink{URL: tc.url})
			if ok != tc.wantOK {
				t.Fatalf("localSourcePath(%q) ok = %v, want %v", tc.url, ok, tc.wantOK)
			}
			if ok && path != tc.wantPath {
				t.Fatalf("localSourcePath(%q) path = %q, want %q", tc.url, path, tc.wantPath)
			}
		})
	}
}

func TestExtAssumedPlayable(t *testing.T) {
	for ext, want := range map[string]bool{
		"mp4": true, "MP4": true, " m4v ": true,
		"avi": false, "mov": false, "mkv": false, "": false,
	} {
		if got := extAssumedPlayable(ext); got != want {
			t.Fatalf("extAssumedPlayable(%q) = %v, want %v", ext, got, want)
		}
	}
}

func TestFormatFFmpegHeaders(t *testing.T) {
	if got := formatFFmpegHeaders(nil); got != "" {
		t.Fatalf("empty headers should format to empty string, got %q", got)
	}
	h := http.Header{}
	h.Set("User-Agent", "test-agent")
	h.Set("Cookie", "a=1")
	h.Add("Cookie", "b=2")
	want := "User-Agent: test-agent\r\n"
	if got := formatFFmpegHeaders(h); got != want {
		t.Fatalf("formatFFmpegHeaders = %q, want %q", got, want)
	}
}

func TestUploadTranscodedFileReusesExistingDestination(t *testing.T) {
	const name = "video-identity.mp4"
	drive := &transcodeUploadTestDrive{entries: []drives.Entry{{ID: "existing", Name: name, Size: 7}}}
	worker := NewWorker(Config{}, nil, drive)
	path := writeTranscodeTestFile(t, "payload")

	fileID, err := worker.uploadTranscodedFile(context.Background(), "target", name, path, 7)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if fileID != "existing" || drive.uploadCalls != 0 || drive.listCalls != 1 {
		t.Fatalf("fileID=%q uploads=%d lists=%d", fileID, drive.uploadCalls, drive.listCalls)
	}
}

func TestUploadTranscodedFileReconcilesAmbiguousUploadError(t *testing.T) {
	const name = "video-identity.mp4"
	drive := &transcodeUploadTestDrive{}
	drive.uploadFn = func(gotName string, data []byte, size int64) (string, error) {
		if gotName != name || string(data) != "payload" || size != 7 {
			t.Fatalf("upload got name=%q data=%q size=%d", gotName, data, size)
		}
		drive.entries = []drives.Entry{{ID: "committed", Name: name, Size: size}}
		return "", errors.New("connection reset after commit")
	}
	worker := NewWorker(Config{}, nil, drive)
	path := writeTranscodeTestFile(t, "payload")

	fileID, err := worker.uploadTranscodedFile(context.Background(), "target", name, path, 7)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if fileID != "committed" || drive.uploadCalls != 1 || drive.listCalls != 2 {
		t.Fatalf("fileID=%q uploads=%d lists=%d", fileID, drive.uploadCalls, drive.listCalls)
	}
}

func TestUploadTranscodedFileDoesNotProbeDuringRateLimit(t *testing.T) {
	drive := &transcodeUploadTestDrive{
		uploadFn: func(string, []byte, int64) (string, error) {
			return "", &drives.RateLimitError{Provider: "test", RetryAfter: time.Minute, Err: errors.New("limited")}
		},
	}
	worker := NewWorker(Config{}, nil, drive)
	path := writeTranscodeTestFile(t, "payload")

	_, err := worker.uploadTranscodedFile(context.Background(), "target", "video.mp4", path, 7)
	if _, ok := drives.RateLimitRetryAfter(err); !ok {
		t.Fatalf("error = %v, want rate limit", err)
	}
	if drive.listCalls != 1 || drive.uploadCalls != 1 {
		t.Fatalf("lists=%d uploads=%d, want 1/1", drive.listCalls, drive.uploadCalls)
	}
}

func writeTranscodeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/output.mp4"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
