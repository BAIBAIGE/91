package mediasim

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFrameSignatureCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	teaser := filepath.Join(dir, "teaser.mp4")
	if err := os.WriteFile(teaser, []byte("teaser-bytes"), 0o644); err != nil {
		t.Fatalf("write teaser: %v", err)
	}
	cachePath := filepath.Join(dir, "framesigs", "video.fsig")

	sig := &FrameSignature{Frames: [][]byte{randomFrame(1), nil, randomFrame(2)}}
	if _, ok := LoadCachedTeaserSignature(cachePath, teaser); ok {
		t.Fatalf("cache hit before store")
	}
	if err := StoreCachedTeaserSignature(cachePath, teaser, sig); err != nil {
		t.Fatalf("store: %v", err)
	}
	loaded, ok := LoadCachedTeaserSignature(cachePath, teaser)
	if !ok {
		t.Fatalf("cache miss after store")
	}
	if len(loaded.Frames) != 3 || loaded.Frames[1] != nil {
		t.Fatalf("frames shape = %d, nil@1=%v", len(loaded.Frames), loaded.Frames[1] == nil)
	}
	if got := ssimLuma(sig.Frames[0], loaded.Frames[0]); got < 0.999 {
		t.Fatalf("frame 0 roundtrip ssim = %f", got)
	}
	if got := ssimLuma(sig.Frames[2], loaded.Frames[2]); got < 0.999 {
		t.Fatalf("frame 2 roundtrip ssim = %f", got)
	}
}

func TestFrameSignatureCacheInvalidation(t *testing.T) {
	dir := t.TempDir()
	teaser := filepath.Join(dir, "teaser.mp4")
	if err := os.WriteFile(teaser, []byte("v1"), 0o644); err != nil {
		t.Fatalf("write teaser: %v", err)
	}
	cachePath := filepath.Join(dir, "video.fsig")
	sig := &FrameSignature{Frames: [][]byte{randomFrame(1)}}
	if err := StoreCachedTeaserSignature(cachePath, teaser, sig); err != nil {
		t.Fatalf("store: %v", err)
	}

	// teaser 重新生成（内容与 mtime 变化）后缓存必须失效。
	if err := os.WriteFile(teaser, []byte("v2-longer"), 0o644); err != nil {
		t.Fatalf("rewrite teaser: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(teaser, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, ok := LoadCachedTeaserSignature(cachePath, teaser); ok {
		t.Fatalf("stale cache accepted after teaser change")
	}

	// 缓存文件损坏时不 panic、返回 miss。
	if err := StoreCachedTeaserSignature(cachePath, teaser, sig); err != nil {
		t.Fatalf("re-store: %v", err)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	if err := os.WriteFile(cachePath, data[:len(data)/2], 0o644); err != nil {
		t.Fatalf("truncate cache: %v", err)
	}
	if _, ok := LoadCachedTeaserSignature(cachePath, teaser); ok {
		t.Fatalf("truncated cache accepted")
	}
}
