package remoteupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// mediaInfo contains only the ffprobe fields needed to validate an uploaded
// file and preserve its source container extension.
type mediaInfo struct {
	FormatName  string
	VideoCodecs []string
}

func probeMediaFile(ctx context.Context, ffprobePath, filePath string) (mediaInfo, error) {
	probeCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(
		probeCtx,
		ffprobePath,
		"-v", "error",
		"-show_entries", "format=format_name",
		"-show_entries", "stream=codec_type,codec_name",
		"-of", "json",
		filePath,
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return mediaInfo{}, fmt.Errorf("remote upload probe: %w: %s", err, tailProbeText(string(exitErr.Stderr), 300))
		}
		return mediaInfo{}, fmt.Errorf("remote upload probe: %w", err)
	}
	var parsed struct {
		Format struct {
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return mediaInfo{}, fmt.Errorf("remote upload probe: parse output: %w", err)
	}
	info := mediaInfo{FormatName: parsed.Format.FormatName}
	for _, stream := range parsed.Streams {
		if stream.CodecType == "video" {
			info.VideoCodecs = append(info.VideoCodecs, stream.CodecName)
		}
	}
	return info, nil
}

func tailProbeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
