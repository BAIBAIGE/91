package mediaasset

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
)

const (
	shortsBackgroundMaxDimension = 96
	shortsBackgroundBlurRadius   = 3
)

// ShortsBackgroundPath stores the tiny, pre-blurred texture used behind a
// contained shorts video. It is derived lazily from the normal 480px thumbnail.
func ShortsBackgroundPath(localDir, videoID string) string {
	return filepath.Join(
		localDir,
		"thumbs-shorts-bg",
		safeFilename(videoID, ".jpg"),
	)
}

// EnsureShortsBackground creates or refreshes a small pre-blurred JPEG. The
// runtime page can upscale this texture without asking the GPU to blur a
// viewport-sized layer on every slide.
func EnsureShortsBackground(sourcePath, destinationPath string) error {
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if destinationInfo, statErr := os.Stat(destinationPath); statErr == nil &&
		shortsBackgroundIsFresh(destinationInfo, sourceInfo) {
		return nil
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	frame, err := jpeg.Decode(source)
	closeErr := source.Close()
	if err != nil {
		return fmt.Errorf("decode shorts background source: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}

	resized := resizeForShortsBackground(frame)
	blurred := boxBlur(resized, shortsBackgroundBlurRadius)
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destinationPath), ".shorts-bg-*.jpg")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := jpeg.Encode(temp, blurred, &jpeg.Options{Quality: 58}); err != nil {
		_ = temp.Close()
		return fmt.Errorf("encode shorts background: %w", err)
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Preserve the source generation timestamp so freshness checks also work
	// when files were restored with a timestamp slightly ahead of local time.
	if err := os.Chtimes(tempPath, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		return err
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		// Another request may have won the same lazy-generation race.
		if info, statErr := os.Stat(destinationPath); statErr == nil &&
			shortsBackgroundIsFresh(info, sourceInfo) {
			return nil
		}
		// Windows does not replace an existing destination atomically. Remove
		// only the stale derived file, then retry; a competing request either
		// wins the rename or leaves an equally fresh result for us to accept.
		if removeErr := os.Remove(destinationPath); removeErr != nil &&
			!os.IsNotExist(removeErr) {
			return err
		}
		if renameErr := os.Rename(tempPath, destinationPath); renameErr != nil {
			if info, statErr := os.Stat(destinationPath); statErr == nil &&
				shortsBackgroundIsFresh(info, sourceInfo) {
				return nil
			}
			return renameErr
		}
	}
	return nil
}

func shortsBackgroundIsFresh(destination, source os.FileInfo) bool {
	return destination.Size() > 0 &&
		!destination.ModTime().Before(source.ModTime())
}

func resizeForShortsBackground(source image.Image) *image.RGBA {
	bounds := source.Bounds()
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	targetWidth := sourceWidth
	targetHeight := sourceHeight
	longest := max(sourceWidth, sourceHeight)
	if longest > shortsBackgroundMaxDimension {
		targetWidth = max(1, sourceWidth*shortsBackgroundMaxDimension/longest)
		targetHeight = max(1, sourceHeight*shortsBackgroundMaxDimension/longest)
	}

	target := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		sourceY := bounds.Min.Y + min(sourceHeight-1, y*sourceHeight/targetHeight)
		for x := 0; x < targetWidth; x++ {
			sourceX := bounds.Min.X + min(sourceWidth-1, x*sourceWidth/targetWidth)
			target.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	return target
}

func boxBlur(source *image.RGBA, radius int) *image.RGBA {
	if radius <= 0 {
		return source
	}
	bounds := source.Bounds()
	target := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			var red, green, blue, alpha, samples uint32
			for sampleY := max(bounds.Min.Y, y-radius); sampleY <= min(bounds.Max.Y-1, y+radius); sampleY++ {
				for sampleX := max(bounds.Min.X, x-radius); sampleX <= min(bounds.Max.X-1, x+radius); sampleX++ {
					r, g, b, a := source.RGBAAt(sampleX, sampleY).RGBA()
					red += r >> 8
					green += g >> 8
					blue += b >> 8
					alpha += a >> 8
					samples++
				}
			}
			target.SetRGBA(x, y, color.RGBA{
				R: uint8(red / samples),
				G: uint8(green / samples),
				B: uint8(blue / samples),
				A: uint8(alpha / samples),
			})
		}
	}
	return target
}
