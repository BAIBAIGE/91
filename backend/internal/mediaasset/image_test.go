package mediaasset

import (
	"bytes"
	"encoding/base64"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// Small lossless WebP fixture derived from golang.org/x/image/testdata.
const testWebPBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func writeTestWebP(t *testing.T, path string) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(testWebPBase64)
	if err != nil {
		t.Fatalf("decode WebP fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir WebP fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write WebP fixture: %v", err)
	}
}

func TestDecodeImageSupportsWebPWithJPEGSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disguised.jpg")
	writeTestWebP(t, path)
	decoded, err := DecodeImage(path)
	if err != nil {
		t.Fatalf("DecodeImage: %v", err)
	}
	if decoded.Bounds().Dx() <= 0 || decoded.Bounds().Dy() <= 0 {
		t.Fatalf("decoded bounds = %v, want positive dimensions", decoded.Bounds())
	}
}

func TestNormalizeThumbnailJPEGConvertsWebPInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jpg")
	writeTestWebP(t, path)
	if err := NormalizeThumbnailJPEG(path, path); err != nil {
		t.Fatalf("NormalizeThumbnailJPEG: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read normalized JPEG: %v", err)
	}
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		t.Fatalf("normalized prefix = %x, want JPEG SOI", data[:min(2, len(data))])
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("decode normalized JPEG: %v", err)
	}
}

func TestNormalizeThumbnailJPEGDoesNotOverwriteOnInvalidSource(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "invalid.jpg")
	destinationPath := filepath.Join(directory, "existing.jpg")
	if err := os.WriteFile(sourcePath, []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write invalid source: %v", err)
	}
	want := []byte("existing thumbnail")
	if err := os.WriteFile(destinationPath, want, 0o644); err != nil {
		t.Fatalf("write existing destination: %v", err)
	}
	if err := NormalizeThumbnailJPEG(sourcePath, destinationPath); err == nil {
		t.Fatal("NormalizeThumbnailJPEG succeeded for invalid source")
	}
	got, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read existing destination: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("destination changed to %q, want %q", got, want)
	}
}

func TestNormalizeThumbnailDirectoryJPEGContinuesPastInvalidFiles(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "legacy.jpg")
	writeTestWebP(t, legacyPath)
	invalidPath := filepath.Join(directory, "invalid.jpg")
	if err := os.WriteFile(invalidPath, []byte("invalid"), 0o644); err != nil {
		t.Fatalf("write invalid thumbnail: %v", err)
	}
	ignoredPath := filepath.Join(directory, "ignored.webp")
	writeTestWebP(t, ignoredPath)

	stats, err := NormalizeThumbnailDirectoryJPEG(directory)
	if err == nil {
		t.Fatal("NormalizeThumbnailDirectoryJPEG succeeded despite invalid JPG")
	}
	if stats.Scanned != 2 || stats.Normalized != 1 || stats.Failed != 1 {
		t.Fatalf("stats = %+v, want scanned=2 normalized=1 failed=1", stats)
	}
	if format, err := imageFormat(legacyPath); err != nil || format != "jpeg" {
		t.Fatalf("legacy format = %q err=%v, want jpeg", format, err)
	}
	if got, err := os.ReadFile(invalidPath); err != nil || string(got) != "invalid" {
		t.Fatalf("invalid file changed: data=%q err=%v", got, err)
	}
}
