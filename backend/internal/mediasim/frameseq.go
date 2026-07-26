package mediasim

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// 内容级查重：对时长几乎相等的两个视频，比较各自 teaser 在相同偏移处的帧。
// teaser 选段起点只由时长决定（见 preview.buildTeaserPlan），因此时长相等的
// 两个视频即使压制、水印、裁切不同，对齐帧也来自同一源画面。
const (
	// FrameSignatureGridSize 亮度采样网格边长，与 SSIM 的 ssimSampleSize 一致。
	FrameSignatureGridSize = 96
	// FrameSignatureMaxFrames teaser 按 1fps 采样的帧数上限（4 段 × 3 秒）。
	FrameSignatureMaxFrames = 12

	// ContentDuplicateMinDurationSeconds 内容级查重的最短时长。短视频的整秒
	// 时长碰撞太普遍（全库实测 10 秒档有近 400 个），时长相等不构成信号。
	ContentDuplicateMinDurationSeconds = 120
	// ContentDuplicateSSIMThreshold 判定重复的对齐帧 SSIM 中位数下限。
	// 全库实测（约 1 万对同时长负样本）：真重复 ≥0.90，非重复几乎全部 <0.5。
	ContentDuplicateSSIMThreshold = 0.92
	// ContentDuplicateNearMissThreshold 仅记录日志、不自动处理的疑似区下限。
	ContentDuplicateNearMissThreshold = 0.80
	// ContentDuplicateMinComparisons 有效对齐比较帧数下限，不足视为证据不够。
	ContentDuplicateMinComparisons = 6

	// 交叉匹配（错位召回）：teaser 某段生成失败回退到备选起点时，两个真重复
	// 的对齐帧会整段错位，对齐中位数骤降。此时改用双向逐帧最优匹配判定。
	// 只在时长精确相等时启用（调用方保证），且单帧强匹配线远高于对齐阈值，
	// 避免把"同场景不同片段"的静态画面对（对齐规则刻意留在疑似区的）捞进来。
	//
	// ContentDuplicateCrossFrameSSIM 单帧视为强匹配的下限。
	ContentDuplicateCrossFrameSSIM = 0.95
	// ContentDuplicateCrossMinRatio 双向强匹配帧占比下限。
	ContentDuplicateCrossMinRatio = 0.75
	// ContentDuplicateCrossMinFrames 双向各自的有效帧数下限。
	ContentDuplicateCrossMinFrames = 8

	// informativeFrameMinStdDev 亮度标准差低于该值的帧（黑场、纯色渐变）
	// 与任何同类帧的 SSIM 都接近 1，必须排除在比较之外。
	informativeFrameMinStdDev = 6.0

	frameExtractTimeout = 60 * time.Second
	frameBytes          = FrameSignatureGridSize * FrameSignatureGridSize
)

// FrameSignature 是按固定偏移采样的灰度帧序列。索引即对齐关系；
// 提取失败的帧以 nil 占位，保持后续帧的对齐。
type FrameSignature struct {
	Frames [][]byte
}

// InformativeFrames 返回参与比较的有效帧数。
func (s *FrameSignature) InformativeFrames() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, f := range s.Frames {
		if informativeFrame(f) {
			n++
		}
	}
	return n
}

// FrameSignatureComparison 是一对帧签名的比较结果。
type FrameSignatureComparison struct {
	Comparisons int     // 双方均为有效帧的对齐比较次数
	MedianSSIM  float64 // 对齐 SSIM 的中位数
	MinSSIM     float64 // 对齐 SSIM 的最小值（仅用于日志）
}

// IsContentDuplicate 按全库实测标定的阈值判定是否内容重复。
func (c FrameSignatureComparison) IsContentDuplicate() bool {
	return c.Comparisons >= ContentDuplicateMinComparisons && c.MedianSSIM >= ContentDuplicateSSIMThreshold
}

// IsContentNearMiss 报告比较结果是否落在仅记日志的疑似区。
func (c FrameSignatureComparison) IsContentNearMiss() bool {
	return c.Comparisons >= ContentDuplicateMinComparisons &&
		c.MedianSSIM >= ContentDuplicateNearMissThreshold &&
		c.MedianSSIM < ContentDuplicateSSIMThreshold
}

// CompareFrameSignatures 比较两个帧签名的对齐帧。
func CompareFrameSignatures(a, b *FrameSignature) FrameSignatureComparison {
	out := FrameSignatureComparison{}
	if a == nil || b == nil {
		return out
	}
	n := len(a.Frames)
	if len(b.Frames) < n {
		n = len(b.Frames)
	}
	scores := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		if !informativeFrame(a.Frames[i]) || !informativeFrame(b.Frames[i]) {
			continue
		}
		scores = append(scores, ssimLuma(a.Frames[i], b.Frames[i]))
	}
	out.Comparisons = len(scores)
	if len(scores) == 0 {
		return out
	}
	out.MinSSIM = scores[0]
	for _, s := range scores[1:] {
		if s < out.MinSSIM {
			out.MinSSIM = s
		}
	}
	sorted := append([]float64(nil), scores...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		out.MedianSSIM = sorted[mid]
	} else {
		out.MedianSSIM = (sorted[mid-1] + sorted[mid]) / 2
	}
	return out
}

// FrameSignatureCrossComparison 是双向逐帧最优匹配的结果。
type FrameSignatureCrossComparison struct {
	LeftFrames  int     // 左侧有效帧数
	RightFrames int     // 右侧有效帧数
	LeftStrong  int     // 左侧在右侧找到 ≥CrossFrameSSIM 匹配的帧数
	RightStrong int     // 右侧在左侧找到 ≥CrossFrameSSIM 匹配的帧数
	MedianBest  float64 // 左侧逐帧最优分的中位数（仅用于日志）
}

// IsContentDuplicate 判定错位场景下的内容重复：双向都有足够多的帧
// 在对方找到强匹配。调用方须保证两视频时长精确相等。
func (c FrameSignatureCrossComparison) IsContentDuplicate() bool {
	if c.LeftFrames < ContentDuplicateCrossMinFrames || c.RightFrames < ContentDuplicateCrossMinFrames {
		return false
	}
	return float64(c.LeftStrong) >= ContentDuplicateCrossMinRatio*float64(c.LeftFrames) &&
		float64(c.RightStrong) >= ContentDuplicateCrossMinRatio*float64(c.RightFrames)
}

// CompareFrameSignaturesCross 计算双向逐帧最优匹配，只在对齐规则未命中且
// 时长精确相等时值得调用（O(n²) 帧比较）。
func CompareFrameSignaturesCross(a, b *FrameSignature) FrameSignatureCrossComparison {
	out := FrameSignatureCrossComparison{}
	if a == nil || b == nil {
		return out
	}
	leftFrames := informativeOnly(a.Frames)
	rightFrames := informativeOnly(b.Frames)
	out.LeftFrames = len(leftFrames)
	out.RightFrames = len(rightFrames)
	if len(leftFrames) == 0 || len(rightFrames) == 0 {
		return out
	}
	rightBest := make([]float64, len(rightFrames))
	leftBest := make([]float64, 0, len(leftFrames))
	for _, left := range leftFrames {
		best := 0.0
		for j, right := range rightFrames {
			score := ssimLuma(left, right)
			if score > best {
				best = score
			}
			if score > rightBest[j] {
				rightBest[j] = score
			}
		}
		leftBest = append(leftBest, best)
		if best >= ContentDuplicateCrossFrameSSIM {
			out.LeftStrong++
		}
	}
	for _, best := range rightBest {
		if best >= ContentDuplicateCrossFrameSSIM {
			out.RightStrong++
		}
	}
	sorted := append([]float64(nil), leftBest...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		out.MedianBest = sorted[mid]
	} else {
		out.MedianBest = (sorted[mid-1] + sorted[mid]) / 2
	}
	return out
}

func informativeOnly(frames [][]byte) [][]byte {
	out := make([][]byte, 0, len(frames))
	for _, f := range frames {
		if informativeFrame(f) {
			out = append(out, f)
		}
	}
	return out
}

// ExtractTeaserFrameSignature 从本地 teaser 按 1fps 提取灰度帧签名。
func ExtractTeaserFrameSignature(ctx context.Context, ffmpegPath, teaserPath string) (*FrameSignature, error) {
	ctx2, cancel := context.WithTimeout(ctx, frameExtractTimeout)
	defer cancel()
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-i", teaserPath,
		"-vf", fmt.Sprintf("fps=1,scale=%d:%d,format=gray", FrameSignatureGridSize, FrameSignatureGridSize),
		"-frames:v", fmt.Sprintf("%d", FrameSignatureMaxFrames),
		"-f", "rawvideo", "pipe:1",
	}
	cmd := exec.CommandContext(ctx2, resolveFFmpegPath(ffmpegPath), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame signature: %w, stderr: %s", err, truncateFFmpegLog(stderr.String()))
	}
	raw := stdout.Bytes()
	count := len(raw) / frameBytes
	if count == 0 {
		return nil, fmt.Errorf("ffmpeg frame signature produced no frames, stderr: %s", truncateFFmpegLog(stderr.String()))
	}
	if count > FrameSignatureMaxFrames {
		count = FrameSignatureMaxFrames
	}
	sig := &FrameSignature{Frames: make([][]byte, 0, count)}
	for i := 0; i < count; i++ {
		frame := make([]byte, frameBytes)
		copy(frame, raw[i*frameBytes:(i+1)*frameBytes])
		sig.Frames = append(sig.Frames, frame)
	}
	return sig, nil
}

// ExtractFrameSignatureAtTimes 从本地完整视频按给定时间戳逐帧提取签名，
// 用于爬虫导入时把新视频与候选 teaser 对齐比较。失败的时间戳留 nil 占位。
func ExtractFrameSignatureAtTimes(ctx context.Context, ffmpegPath, videoPath string, times []float64) (*FrameSignature, error) {
	if len(times) == 0 {
		return nil, fmt.Errorf("no sample times")
	}
	sig := &FrameSignature{Frames: make([][]byte, len(times))}
	extracted := 0
	for i, t := range times {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame, err := extractSingleFrame(ctx, ffmpegPath, videoPath, t)
		if err != nil {
			continue
		}
		sig.Frames[i] = frame
		extracted++
	}
	if extracted == 0 {
		return nil, fmt.Errorf("no frames extracted from %d sample times", len(times))
	}
	return sig, nil
}

func extractSingleFrame(ctx context.Context, ffmpegPath, videoPath string, offset float64) ([]byte, error) {
	ctx2, cancel := context.WithTimeout(ctx, frameExtractTimeout)
	defer cancel()
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-ss", fmt.Sprintf("%.2f", offset),
		"-i", videoPath,
		"-frames:v", "1",
		"-vf", fmt.Sprintf("scale=%d:%d,format=gray", FrameSignatureGridSize, FrameSignatureGridSize),
		"-f", "rawvideo", "pipe:1",
	}
	cmd := exec.CommandContext(ctx2, resolveFFmpegPath(ffmpegPath), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg frame at %.2fs: %w, stderr: %s", offset, err, truncateFFmpegLog(stderr.String()))
	}
	raw := stdout.Bytes()
	if len(raw) < frameBytes {
		return nil, fmt.Errorf("ffmpeg frame at %.2fs produced %d bytes", offset, len(raw))
	}
	frame := make([]byte, frameBytes)
	copy(frame, raw[:frameBytes])
	return frame, nil
}

func resolveFFmpegPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return "ffmpeg"
	}
	return path
}

func truncateFFmpegLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

func informativeFrame(frame []byte) bool {
	if len(frame) == 0 {
		return false
	}
	var sum float64
	for _, v := range frame {
		sum += float64(v)
	}
	mean := sum / float64(len(frame))
	var variance float64
	for _, v := range frame {
		d := float64(v) - mean
		variance += d * d
	}
	variance /= float64(len(frame))
	return math.Sqrt(variance) >= informativeFrameMinStdDev
}

// ssimLuma 与 SSIM 使用相同的常数，但直接作用于灰度字节帧。
func ssimLuma(a, b []byte) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var meanA, meanB float64
	for i := range a {
		meanA += float64(a[i])
		meanB += float64(b[i])
	}
	n := float64(len(a))
	meanA /= n
	meanB /= n
	var varA, varB, cov float64
	for i := range a {
		da := float64(a[i]) - meanA
		db := float64(b[i]) - meanB
		varA += da * da
		varB += db * db
		cov += da * db
	}
	varA /= n
	varB /= n
	cov /= n
	const c1 = 6.5025
	const c2 = 58.5225
	den := (meanA*meanA + meanB*meanB + c1) * (varA + varB + c2)
	if den == 0 {
		return 0
	}
	score := ((2*meanA*meanB + c1) * (2*cov + c2)) / den
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score
}
