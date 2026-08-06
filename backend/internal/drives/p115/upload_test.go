package p115

import (
	"errors"
	"strings"
	"testing"
)

func TestParseP115UploadCallbackReturnsExactFileID(t *testing.T) {
	body := []byte(`{"state":true,"data":{"file_id":"file-exact","sha1":"abcdef"}}`)
	fileID, err := parseP115UploadCallback(body, "ABCDEF")
	if err != nil {
		t.Fatalf("parse callback: %v", err)
	}
	if fileID != "file-exact" {
		t.Fatalf("file ID = %q, want file-exact", fileID)
	}
}

func TestParseP115UploadCallbackRejectsMismatchedSHA1(t *testing.T) {
	body := []byte(`{"state":true,"data":{"file_id":"file-wrong","sha1":"AAAA"}}`)
	_, err := parseP115UploadCallback(body, "BBBB")
	if err == nil || !strings.Contains(err.Error(), "SHA1 mismatch") {
		t.Fatalf("error = %v, want SHA1 mismatch", err)
	}
	if errors.Is(err, errP115UploadResultUnavailable) {
		t.Fatalf("SHA1 mismatch must not be treated as an unavailable callback: %v", err)
	}
}

func TestParseP115UploadCallbackMarksUnavailableResults(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "empty", body: nil},
		{name: "malformed", body: []byte(`{"state":`)},
		{name: "missing file ID", body: []byte(`{"state":true,"data":{"sha1":"ABC"}}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseP115UploadCallback(tt.body, "ABC")
			if !errors.Is(err, errP115UploadResultUnavailable) {
				t.Fatalf("error = %v, want result unavailable", err)
			}
		})
	}
}
