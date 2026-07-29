package mediaasset

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilenamesKeepShortSafeIDs(t *testing.T) {
	if got := ThumbnailFilename("video-1"); got != "video-1.jpg" {
		t.Fatalf("thumbnail filename = %q, want video-1.jpg", got)
	}
	if got := PreviewFilename("video-1"); got != "video-1.mp4" {
		t.Fatalf("preview filename = %q, want video-1.mp4", got)
	}
}

func TestFilenamesHashLongOrUnsafeIDs(t *testing.T) {
	longID := "localstorage-" + strings.Repeat("x", 240)
	got := ThumbnailFilename(longID)
	if !strings.HasPrefix(got, "v-") || !strings.HasSuffix(got, ".jpg") {
		t.Fatalf("thumbnail filename = %q, want hashed jpg", got)
	}
	if len([]byte(got)) >= len([]byte(longID+".jpg")) {
		t.Fatalf("thumbnail filename = %q should be shorter than original id", got)
	}

	unsafe := ThumbnailFilename("dir/video")
	if unsafe == "dir/video.jpg" || strings.ContainsAny(unsafe, `/\`) {
		t.Fatalf("unsafe thumbnail filename = %q, want hashed single filename", unsafe)
	}
}

func TestThumbnailPathCandidatesIncludeLegacyForHashedIDs(t *testing.T) {
	localDir := t.TempDir()
	mediumID := "localstorage-" + strings.Repeat("x", 190)
	got := ThumbnailPathCandidates(localDir, mediumID)
	if len(got) != 2 {
		t.Fatalf("candidates = %#v, want hashed and legacy paths", got)
	}
	if got[0] != ThumbnailPath(localDir, mediumID) {
		t.Fatalf("first candidate = %q, want safe path %q", got[0], ThumbnailPath(localDir, mediumID))
	}
	if filepath.Base(got[1]) != mediumID+".jpg" {
		t.Fatalf("legacy candidate = %q, want original id jpg", got[1])
	}
}

func TestThumbnailPathCandidatesSkipOverlongLegacy(t *testing.T) {
	localDir := t.TempDir()
	longID := "localstorage-" + strings.Repeat("x", 240)
	got := ThumbnailPathCandidates(localDir, longID)
	if len(got) != 1 {
		t.Fatalf("candidates = %#v, want only hashed path for overlong id", got)
	}
}

func TestEnsureShortsBackgroundCreatesSmallPreblurredJPEG(t *testing.T) {
	localDir := t.TempDir()
	sourcePath := ThumbnailPath(localDir, "video-1")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	source, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 480, 270))
	for y := 0; y < 270; y++ {
		for x := 0; x < 480; x++ {
			frame.SetRGBA(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}
	if err := jpeg.Encode(source, frame, &jpeg.Options{Quality: 80}); err != nil {
		_ = source.Close()
		t.Fatalf("encode source: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	destinationPath := ShortsBackgroundPath(localDir, "video-1")
	if err := EnsureShortsBackground(sourcePath, destinationPath); err != nil {
		t.Fatalf("EnsureShortsBackground: %v", err)
	}
	generated, err := os.Open(destinationPath)
	if err != nil {
		t.Fatalf("open generated background: %v", err)
	}
	decoded, err := jpeg.Decode(generated)
	_ = generated.Close()
	if err != nil {
		t.Fatalf("decode generated background: %v", err)
	}
	if longest := max(decoded.Bounds().Dx(), decoded.Bounds().Dy()); longest > shortsBackgroundMaxDimension {
		t.Fatalf("generated dimensions = %v, longest side exceeds %d", decoded.Bounds(), shortsBackgroundMaxDimension)
	}
	if filepath.Dir(destinationPath) != filepath.Join(localDir, "thumbs-shorts-bg") {
		t.Fatalf("background path = %q, want dedicated cache directory", destinationPath)
	}

	firstBytes, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read first background: %v", err)
	}
	replacement, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("replace source: %v", err)
	}
	replacementFrame := image.NewRGBA(image.Rect(0, 0, 480, 270))
	for y := 0; y < replacementFrame.Bounds().Dy(); y++ {
		for x := 0; x < replacementFrame.Bounds().Dx(); x++ {
			replacementFrame.SetRGBA(x, y, color.RGBA{R: 240, G: 20, B: 40, A: 255})
		}
	}
	if err := jpeg.Encode(replacement, replacementFrame, &jpeg.Options{Quality: 80}); err != nil {
		_ = replacement.Close()
		t.Fatalf("encode replacement source: %v", err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatalf("close replacement source: %v", err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(sourcePath, future, future); err != nil {
		t.Fatalf("advance source timestamp: %v", err)
	}
	if err := EnsureShortsBackground(sourcePath, destinationPath); err != nil {
		t.Fatalf("refresh shorts background: %v", err)
	}
	refreshedBytes, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("read refreshed background: %v", err)
	}
	if bytes.Equal(firstBytes, refreshedBytes) {
		t.Fatal("background cache did not refresh after the source changed")
	}
	refreshedInfo, err := os.Stat(destinationPath)
	if err != nil {
		t.Fatalf("stat refreshed background: %v", err)
	}
	if refreshedInfo.ModTime().Before(future) {
		t.Fatalf(
			"refreshed timestamp = %v, want at least source timestamp %v",
			refreshedInfo.ModTime(),
			future,
		)
	}
}
