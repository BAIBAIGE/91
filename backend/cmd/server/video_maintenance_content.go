package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/mediaasset"
	"github.com/video-site/backend/internal/mediasim"
)

// 内容级查重通道：teaser 选段起点只由时长决定，时长几乎相等的两个视频即使
// 压制、标题、封面完全不同，teaser 对齐帧也来自同一源画面。该通道补齐
// 标题/封面通道抓不到的"同内容不同压制"重复（爬虫压缩版 vs 网盘原版等）。
type contentDuplicateMaintenanceStats struct {
	Candidates    int
	Extracted     int
	ExtractFailed int
	Comparisons   int
	CrossMatched  int
	Groups        int
	Deleted       int
}

type contentDupCandidate struct {
	video      *catalog.Video
	teaserPath string
}

// contentSignatureExtractor 允许测试注入合成签名，生产始终用 ffmpeg 提取。
var contentSignatureExtractor = mediasim.ExtractTeaserFrameSignature

func (a *App) cleanupContentDuplicateVideos(ctx context.Context, localDir string, videos []*catalog.Video, deleted map[string]struct{}) (contentDuplicateMaintenanceStats, error) {
	stats := contentDuplicateMaintenanceStats{}
	candidates := collectContentDupCandidates(localDir, videos, deleted)
	stats.Candidates = len(candidates)
	if len(candidates) < 2 {
		return stats, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i].video, candidates[j].video
		if left.DurationSeconds != right.DurationSeconds {
			return left.DurationSeconds < right.DurationSeconds
		}
		return earlierVideo(left, right)
	})

	ffmpegPath := ""
	if a.cfg != nil {
		ffmpegPath = a.cfg.Preview.FFmpegPath
	}

	// 按时长滑动窗口配对；签名懒提取，滑出窗口即释放，峰值内存可忽略。
	sigs := make(map[int]*mediasim.FrameSignature)
	sigFailed := make(map[int]bool)
	ensureSig := func(i int) *mediasim.FrameSignature {
		if sig, ok := sigs[i]; ok {
			return sig
		}
		if sigFailed[i] {
			return nil
		}
		cachePath := mediaasset.FrameSignaturePath(localDir, candidates[i].video.ID)
		if sig, ok := mediasim.LoadCachedTeaserSignature(cachePath, candidates[i].teaserPath); ok {
			if sig.InformativeFrames() >= mediasim.ContentDuplicateMinComparisons {
				sigs[i] = sig
				stats.Extracted++
				return sig
			}
			sigFailed[i] = true
			stats.ExtractFailed++
			return nil
		}
		sig, err := contentSignatureExtractor(ctx, ffmpegPath, candidates[i].teaserPath)
		if err != nil || sig.InformativeFrames() < mediasim.ContentDuplicateMinComparisons {
			if err != nil {
				log.Printf("[dedupe-maintenance] content signature failed id=%s: %v", candidates[i].video.ID, err)
			}
			sigFailed[i] = true
			stats.ExtractFailed++
			return nil
		}
		if err := mediasim.StoreCachedTeaserSignature(cachePath, candidates[i].teaserPath, sig); err != nil {
			log.Printf("[dedupe-maintenance] content signature cache write id=%s: %v", candidates[i].video.ID, err)
		}
		sigs[i] = sig
		stats.Extracted++
		return sig
	}

	sets := newVideoMaintenanceDisjointSet(len(candidates))
	windowStart := 0
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		for windowStart < i && candidates[i].video.DurationSeconds-candidates[windowStart].video.DurationSeconds > mediasim.NearDuplicateDurationToleranceSeconds {
			delete(sigs, windowStart)
			delete(sigFailed, windowStart)
			windowStart++
		}
		if windowStart == i {
			continue
		}
		rightSig := ensureSig(i)
		if rightSig == nil {
			continue
		}
		for j := windowStart; j < i; j++ {
			leftSig := ensureSig(j)
			if leftSig == nil {
				continue
			}
			cmp := mediasim.CompareFrameSignatures(leftSig, rightSig)
			stats.Comparisons++
			left, right := candidates[j].video, candidates[i].video
			if cmp.IsContentDuplicate() {
				sets.union(i, j)
				log.Printf("[dedupe-maintenance] content duplicate matched left=%s right=%s median_ssim=%.3f min_ssim=%.3f comparisons=%d duration=%d/%d",
					left.ID, right.ID, cmp.MedianSSIM, cmp.MinSSIM, cmp.Comparisons, left.DurationSeconds, right.DurationSeconds)
				continue
			}
			// teaser 兜底段会造成整段错位：对齐分骤降但内容确为重复。
			// 仅在时长精确相等时用双向逐帧最优匹配兜底。
			if left.DurationSeconds == right.DurationSeconds {
				cross := mediasim.CompareFrameSignaturesCross(leftSig, rightSig)
				if cross.IsContentDuplicate() {
					sets.union(i, j)
					stats.CrossMatched++
					log.Printf("[dedupe-maintenance] content duplicate matched (cross) left=%s right=%s strong=%d/%d,%d/%d median_best=%.3f duration=%d",
						left.ID, right.ID, cross.LeftStrong, cross.LeftFrames, cross.RightStrong, cross.RightFrames, cross.MedianBest, left.DurationSeconds)
					continue
				}
			}
		}
	}

	groups := make(map[int][]videoMaintenanceCandidate)
	for i, candidate := range candidates {
		root := sets.find(i)
		groups[root] = append(groups[root], videoMaintenanceCandidate{
			video:      candidate.video,
			assetScore: videoAssetCompletenessScore(localDir, candidate.video),
		})
	}
	roots := make([]int, 0, len(groups))
	for root, group := range groups {
		if len(group) > 1 {
			roots = append(roots, root)
		}
	}
	sort.Ints(roots)

	for _, root := range roots {
		group := groups[root]
		canonical := selectNearDuplicateCanonical(group)
		if canonical.video == nil {
			continue
		}
		stats.Groups++
		for _, candidate := range group {
			v := candidate.video
			if v == nil || v.ID == canonical.video.ID {
				continue
			}
			if _, ok := deleted[v.ID]; ok {
				continue
			}
			if err := a.deleteDuplicateVideoWithAssets(ctx, localDir, v, canonical.video.ID); err != nil {
				return stats, fmt.Errorf("content duplicate canonical=%s duplicate=%s: %w", canonical.video.ID, v.ID, err)
			}
			deleted[v.ID] = struct{}{}
			stats.Deleted++
			log.Printf("[dedupe-maintenance] content duplicate deleted id=%s canonical=%s size=%d canonical_size=%d duration=%d title=%q",
				v.ID, canonical.video.ID, v.Size, canonical.video.Size, v.DurationSeconds, v.Title)
		}
	}
	return stats, nil
}

func collectContentDupCandidates(localDir string, videos []*catalog.Video, deleted map[string]struct{}) []contentDupCandidate {
	out := make([]contentDupCandidate, 0, len(videos))
	for _, v := range videos {
		if v == nil || v.DurationSeconds < mediasim.ContentDuplicateMinDurationSeconds {
			continue
		}
		if _, ok := deleted[v.ID]; ok {
			continue
		}
		teaserPath, ok := localGeneratedPreviewPath(localDir, v)
		if !ok {
			continue
		}
		out = append(out, contentDupCandidate{video: v, teaserPath: teaserPath})
	}
	return out
}
