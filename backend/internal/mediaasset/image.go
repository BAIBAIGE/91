package mediaasset

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"
)

const (
	ThumbnailJPEGQuality  = 88
	maxDecodedImagePixels = 64 * 1024 * 1024
)

// ThumbnailNormalizationStats describes a canonical thumbnail migration pass.
type ThumbnailNormalizationStats struct {
	Scanned    int
	Normalized int
	Failed     int
}

// DecodeImage reads a supported image by its content rather than its filename.
// Keeping format registration here gives every pixel consumer the same JPEG,
// PNG, GIF, and WebP compatibility contract.
func DecodeImage(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return nil, fmt.Errorf("decode image config: %w", err)
	}
	if err := validateImageDimensions(config); err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("rewind image: %w", err)
	}
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	return decoded, nil
}

func validateImageDimensions(config image.Config) error {
	width := int64(config.Width)
	height := int64(config.Height)
	if width <= 0 || height <= 0 {
		return errors.New("image has invalid dimensions")
	}
	if width > maxDecodedImagePixels/height {
		return fmt.Errorf("image dimensions %dx%d exceed the %d-pixel limit", width, height, maxDecodedImagePixels)
	}
	return nil
}

// NormalizeThumbnailJPEG validates sourcePath and writes a real JPEG to
// destinationPath. Decoding finishes before the destination is touched, and a
// same-directory temporary file keeps replacement atomic on supported systems.
func NormalizeThumbnailJPEG(sourcePath, destinationPath string) error {
	frame, err := DecodeImage(sourcePath)
	if err != nil {
		return err
	}
	destinationDir := filepath.Dir(destinationPath)
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return fmt.Errorf("create thumbnail directory: %w", err)
	}
	temp, err := os.CreateTemp(destinationDir, ".thumbnail-*.jpg")
	if err != nil {
		return fmt.Errorf("create temporary thumbnail: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set thumbnail permissions: %w", err)
	}
	if err := jpeg.Encode(temp, frame, &jpeg.Options{Quality: ThumbnailJPEGQuality}); err != nil {
		_ = temp.Close()
		return fmt.Errorf("encode thumbnail JPEG: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync thumbnail JPEG: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close thumbnail JPEG: %w", err)
	}
	if err := os.Rename(tempPath, destinationPath); err != nil {
		return fmt.Errorf("publish thumbnail JPEG: %w", err)
	}
	keepTemp = false
	return nil
}

// NormalizeThumbnailDirectoryJPEG repairs legacy canonical thumbnails whose
// .jpg suffix does not match their encoded content. Invalid files are retained
// for diagnosis and reported after all other files have been processed.
func NormalizeThumbnailDirectoryJPEG(directory string) (ThumbnailNormalizationStats, error) {
	stats := ThumbnailNormalizationStats{}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return stats, nil
	}
	if err != nil {
		return stats, fmt.Errorf("list thumbnail directory: %w", err)
	}

	var normalizationErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jpg") {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		stats.Scanned++
		path := filepath.Join(directory, entry.Name())
		format, err := imageFormat(path)
		if err != nil {
			stats.Failed++
			normalizationErrors = append(normalizationErrors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		if format == "jpeg" {
			continue
		}
		if err := NormalizeThumbnailJPEG(path, path); err != nil {
			stats.Failed++
			normalizationErrors = append(normalizationErrors, fmt.Errorf("%s: %w", entry.Name(), err))
			continue
		}
		stats.Normalized++
	}
	return stats, errors.Join(normalizationErrors...)
}

func imageFormat(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return "", fmt.Errorf("decode image config: %w", err)
	}
	if err := validateImageDimensions(config); err != nil {
		return "", err
	}
	return format, nil
}
