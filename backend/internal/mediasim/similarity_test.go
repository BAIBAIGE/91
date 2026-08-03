package mediasim

import (
	"encoding/base64"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

const similarityWebPBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func TestTitleSimilarityNormalizesPunctuationAndWhitespace(t *testing.T) {
	score := TitleSimilarity("AB-123  测试视频.mp4", "ab123测试视频")
	if score < 0.90 {
		t.Fatalf("similarity = %.3f, want >= 0.90", score)
	}
}

func TestTitleSimilarityUsesLeadingCoreTitle(t *testing.T) {
	score := TitleSimilarity(
		"反差极品大二女友，叫声可射～，“射进小骚逼里面～” - 性感小皮鞭",
		"反差极品大二女友，叫声可射～，“射进小骚逼里面～”",
	)
	if score < 0.99 {
		t.Fatalf("similarity = %.3f, want core-title match", score)
	}
}

func TestTitleSimilarityDoesNotMatchBySharedSuffixOnly(t *testing.T) {
	score := TitleSimilarity(
		"高颜值大学生宿舍自拍视频完整流出 - 同一个来源",
		"户外旅行风景记录城市夜景合集 - 同一个来源",
	)
	if score >= 0.90 {
		t.Fatalf("similarity = %.3f, want < 0.90", score)
	}
}

func TestTitleSimilarityRejectsDifferentTitles(t *testing.T) {
	score := TitleSimilarity("完全不同的视频标题", "another unrelated movie")
	if score >= 0.90 {
		t.Fatalf("similarity = %.3f, want < 0.90", score)
	}
}

func TestSSIMScoresIdenticalAndDifferentImages(t *testing.T) {
	red := solidImage(color.RGBA{R: 220, G: 20, B: 20, A: 255})
	redAgain := solidImage(color.RGBA{R: 220, G: 20, B: 20, A: 255})
	blue := solidImage(color.RGBA{R: 20, G: 20, B: 220, A: 255})

	if score := SSIM(red, redAgain); score < 0.999 {
		t.Fatalf("identical SSIM = %.6f, want close to 1", score)
	}
	if score := SSIM(red, blue); score >= 0.95 {
		t.Fatalf("different SSIM = %.6f, want < 0.95", score)
	}
}

func TestImageSSIMDecodesWebPWithJPEGSuffix(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(similarityWebPBase64)
	if err != nil {
		t.Fatalf("decode WebP fixture: %v", err)
	}
	directory := t.TempDir()
	leftPath := filepath.Join(directory, "left.jpg")
	rightPath := filepath.Join(directory, "right.jpg")
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write WebP fixture: %v", err)
		}
	}
	score, err := ImageSSIM(leftPath, rightPath)
	if err != nil {
		t.Fatalf("ImageSSIM: %v", err)
	}
	if score < 0.999 {
		t.Fatalf("WebP SSIM = %.6f, want close to 1", score)
	}
}

func solidImage(c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}
