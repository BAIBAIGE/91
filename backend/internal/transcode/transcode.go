// Package transcode 实现"浏览器兼容性转码"：把网盘/本地存储中浏览器
// <video> 播不动的视频（AVI/WMV/FLV、MPEG-4 Part 2、RMVB 等）转成
// H.264 + AAC 的 MP4，并把产物上传回同一存储，播放源切到产物文件。
//
// 与封面/预览生成不同，转码不会自动运行——只能由管理员在网盘管理页
// 手动开启，也可以随时手动停止。
package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// MediaInfo 是 ffprobe 探测出来的、做兼容性判定所需的最小信息。
type MediaInfo struct {
	// FormatName 是 ffprobe 的 format_name，逗号分隔的 demuxer 别名，
	// 例如 "mov,mp4,m4a,3gp,3g2,mj2" / "avi" / "matroska,webm"。
	FormatName  string
	VideoCodecs []string
	AudioCodecs []string
}

// browserCompatibleVideoCodecs 是主流浏览器 <video> 普遍可解码的视频编码。
// HEVC/H.265 只有部分平台支持，保守起见不算兼容。
var browserCompatibleVideoCodecs = map[string]bool{
	"h264": true,
	"vp8":  true,
	"vp9":  true,
	"av1":  true,
}

// browserCompatibleAudioCodecs 是主流浏览器普遍可解码的音频编码。
var browserCompatibleAudioCodecs = map[string]bool{
	"aac":    true,
	"mp3":    true,
	"opus":   true,
	"vorbis": true,
	"flac":   true,
}

// NeedsTranscode 判断这个文件是否需要转码才能在浏览器里播放。
// ext 是 catalog 里记录的扩展名（小写、不带点），用来区分 mkv 和 webm
// （两者的 format_name 都是 "matroska,webm"）。
func NeedsTranscode(info MediaInfo, ext string) bool {
	if !containerCompatible(info.FormatName, ext) {
		return true
	}
	for _, codec := range info.VideoCodecs {
		if !browserCompatibleVideoCodecs[strings.ToLower(codec)] {
			return true
		}
	}
	for _, codec := range info.AudioCodecs {
		if !browserCompatibleAudioCodecs[strings.ToLower(codec)] {
			return true
		}
	}
	return false
}

func containerCompatible(formatName, ext string) bool {
	format := strings.ToLower(formatName)
	for _, name := range strings.Split(format, ",") {
		if name == "mp4" {
			return true
		}
	}
	// matroska,webm：只有真 .webm 信任为浏览器可播容器；.mkv 保守转码。
	if strings.Contains(format, "webm") && strings.EqualFold(ext, "webm") {
		return true
	}
	return false
}

// ProbeFile 用 ffprobe 探测本地文件的容器与音视频编码。
func ProbeFile(ctx context.Context, ffprobePath, path string) (MediaInfo, error) {
	return probe(ctx, ffprobePath, nil, path)
}

// ProbeURL 用 ffprobe 直接探测远程流（http/https）的容器与音视频编码，
// 不把文件下载到本地。MP4 只需读 moov 等头尾元数据（ffprobe 通过 Range
// 请求 seek），流量通常在 MB 级。headers 原样传给 ffprobe，携带网盘直链
// 需要的 Cookie / User-Agent / Referer 等。
func ProbeURL(ctx context.Context, ffprobePath, rawURL string, headers http.Header) (MediaInfo, error) {
	var extra []string
	if h := formatFFmpegHeaders(headers); h != "" {
		extra = append(extra, "-headers", h)
	}
	return probe(ctx, ffprobePath, extra, rawURL)
}

// formatFFmpegHeaders 把 http.Header 拼成 ffmpeg/-headers 要求的
// "Key: value\r\n" 逐行格式。key 排序保证输出稳定。
func formatFFmpegHeaders(h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range h[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

func probe(ctx context.Context, ffprobePath string, extraArgs []string, input string) (MediaInfo, error) {
	ctx2, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := []string{"-v", "error"}
	args = append(args, extraArgs...)
	args = append(args,
		"-show_entries", "format=format_name",
		"-show_entries", "stream=codec_type,codec_name",
		"-of", "json",
		input,
	)
	cmd := exec.CommandContext(ctx2, ffprobePath, args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return MediaInfo{}, fmt.Errorf("transcode: ffprobe: %w: %s", err, tailOf(string(ee.Stderr), 300))
		}
		return MediaInfo{}, fmt.Errorf("transcode: ffprobe: %w", err)
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
		return MediaInfo{}, fmt.Errorf("transcode: parse ffprobe output: %w", err)
	}
	info := MediaInfo{FormatName: parsed.Format.FormatName}
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			info.VideoCodecs = append(info.VideoCodecs, s.CodecName)
		case "audio":
			info.AudioCodecs = append(info.AudioCodecs, s.CodecName)
		}
	}
	return info, nil
}

// buildFFmpegArgs 按探测结果生成转码参数：
//   - 编码本就兼容、只是容器不行（如 AVI 里装 H.264）→ 流拷贝 remux，零质量损失；
//   - 否则视频转 H.264（裁到偶数尺寸 + yuv420p 保证兼容性）、音频转 AAC。
//
// 两种情况都加 +faststart 把 moov 提前，便于边下边播。
func buildFFmpegArgs(info MediaInfo, inPath, outPath string) []string {
	args := []string{"-y", "-i", inPath}
	videoOK := true
	for _, codec := range info.VideoCodecs {
		if !browserCompatibleVideoCodecs[strings.ToLower(codec)] {
			videoOK = false
			break
		}
	}
	audioOK := true
	for _, codec := range info.AudioCodecs {
		if !browserCompatibleAudioCodecs[strings.ToLower(codec)] {
			audioOK = false
			break
		}
	}
	if videoOK {
		args = append(args, "-c:v", "copy")
	} else {
		args = append(args,
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-crf", "23",
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
			"-pix_fmt", "yuv420p",
		)
	}
	if len(info.AudioCodecs) == 0 {
		args = append(args, "-an")
	} else if audioOK {
		args = append(args, "-c:a", "copy")
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", outPath)
	return args
}

// TranscodeFile 把本地输入文件转成浏览器可播的 MP4 写到 outPath。
func TranscodeFile(ctx context.Context, ffmpegPath string, info MediaInfo, inPath, outPath string) error {
	args := buildFFmpegArgs(info, inPath, outPath)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("transcode: ffmpeg: %w: %s", err, tailOf(string(out), 400))
	}
	return nil
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
