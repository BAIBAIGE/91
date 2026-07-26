package transcode

import (
	"net/http"
	"testing"

	"github.com/video-site/backend/internal/drives"
)

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
	want := "Cookie: a=1\r\nCookie: b=2\r\nUser-Agent: test-agent\r\n"
	if got := formatFFmpegHeaders(h); got != want {
		t.Fatalf("formatFFmpegHeaders = %q, want %q", got, want)
	}
}
