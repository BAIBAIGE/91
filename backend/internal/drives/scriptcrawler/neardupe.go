package scriptcrawler

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/mediaasset"
	"github.com/video-site/backend/internal/mediasim"
	"github.com/video-site/backend/internal/preview"
)

// 阈值统一定义在 mediasim（NearDuplicate* / ContentDuplicate*），与夜间维护共用。
const nearDuplicateCandidateLimit = 200

type nearDuplicateMatch struct {
	video           *catalog.Video
	titleSimilarity float64
	thumbnailSSIM   float64
	contentSSIM     float64 // >0 时表示由内容通道命中
}

// findNearDuplicateVideo 在导入前查找库内近重复视频，两条通道：
//  1. 标题相似 + 封面 SSIM（同源重发场景）；
//  2. 内容级：时长几乎相等时比较候选 teaser 与本地新视频的对齐帧
//     （跨源不同压制场景，标题和封面通常完全对不上）。
func (c *Crawler) findNearDuplicateVideo(ctx context.Context, source *catalog.Video, sourceThumbPath, sourceVideoPath string) (*nearDuplicateMatch, error) {
	if c == nil || c.cfg.Catalog == nil || source == nil {
		return nil, nil
	}
	if strings.TrimSpace(source.Title) == "" || source.DurationSeconds <= 0 {
		return nil, nil
	}

	candidates, err := c.cfg.Catalog.ListNearDuplicateVideoCandidates(ctx, source, mediasim.NearDuplicateDurationToleranceSeconds, nearDuplicateCandidateLimit)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	if match := c.findTitleThumbDuplicate(source, sourceThumbPath, candidates); match != nil {
		return match, nil
	}
	return c.findContentDuplicate(ctx, source, sourceVideoPath, candidates)
}

func (c *Crawler) findTitleThumbDuplicate(source *catalog.Video, sourceThumbPath string, candidates []*catalog.Video) *nearDuplicateMatch {
	sourceThumbPath = strings.TrimSpace(sourceThumbPath)
	commonThumbDir := strings.TrimSpace(c.cfg.CommonThumbDir)
	if sourceThumbPath == "" || commonThumbDir == "" {
		return nil
	}
	if _, err := os.Stat(sourceThumbPath); err != nil {
		return nil
	}
	for _, candidate := range candidates {
		if candidate == nil || candidate.ID == source.ID {
			continue
		}
		titleScore := mediasim.TitleSimilarity(source.Title, candidate.Title)
		if titleScore < mediasim.NearDuplicateTitleThreshold {
			continue
		}
		candidateThumbPath := mediaasset.ThumbnailPathInDir(commonThumbDir, candidate.ID)
		if _, err := os.Stat(candidateThumbPath); err != nil {
			continue
		}
		ssimScore, err := mediasim.ImageSSIM(sourceThumbPath, candidateThumbPath)
		if err != nil {
			log.Printf("[scriptcrawler] drive=%s source_id=%s candidate=%s thumbnail ssim failed: %v", c.cfg.Driver.ID(), source.ID, candidate.ID, err)
			continue
		}
		if ssimScore >= mediasim.NearDuplicateThumbSSIMThreshold {
			return &nearDuplicateMatch{
				video:           candidate,
				titleSimilarity: titleScore,
				thumbnailSSIM:   ssimScore,
			}
		}
	}
	return nil
}

func (c *Crawler) findContentDuplicate(ctx context.Context, source *catalog.Video, sourceVideoPath string, candidates []*catalog.Video) (*nearDuplicateMatch, error) {
	sourceVideoPath = strings.TrimSpace(sourceVideoPath)
	if sourceVideoPath == "" || source.DurationSeconds < mediasim.ContentDuplicateMinDurationSeconds {
		return nil, nil
	}

	var sourceSig *mediasim.FrameSignature
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if candidate == nil || candidate.ID == source.ID {
			continue
		}
		if candidate.DurationSeconds < mediasim.ContentDuplicateMinDurationSeconds {
			continue
		}
		teaserPath := strings.TrimSpace(candidate.PreviewLocal)
		if strings.TrimSpace(candidate.PreviewStatus) != "ready" || teaserPath == "" {
			continue
		}
		if info, err := os.Stat(teaserPath); err != nil || !info.Mode().IsRegular() {
			continue
		}

		if sourceSig == nil {
			times := preview.TeaserFrameSourceTimes(float64(source.DurationSeconds), mediasim.FrameSignatureMaxFrames)
			sig, err := mediasim.ExtractFrameSignatureAtTimes(ctx, c.cfg.FFmpegPath, sourceVideoPath, times)
			if err != nil {
				log.Printf("[scriptcrawler] drive=%s source_id=%s content signature failed: %v", c.cfg.Driver.ID(), source.ID, err)
				return nil, nil
			}
			if sig.InformativeFrames() < mediasim.ContentDuplicateMinComparisons {
				return nil, nil
			}
			sourceSig = sig
		}

		candidateSig, err := mediasim.ExtractTeaserFrameSignature(ctx, c.cfg.FFmpegPath, teaserPath)
		if err != nil {
			log.Printf("[scriptcrawler] drive=%s source_id=%s candidate=%s teaser signature failed: %v", c.cfg.Driver.ID(), source.ID, candidate.ID, err)
			continue
		}
		cmp := mediasim.CompareFrameSignatures(sourceSig, candidateSig)
		if cmp.IsContentDuplicate() {
			return &nearDuplicateMatch{
				video:       candidate,
				contentSSIM: cmp.MedianSSIM,
			}, nil
		}
		// 候选 teaser 含兜底段时对齐帧整段错位，时长精确相等时用交叉匹配兜底。
		if candidate.DurationSeconds == source.DurationSeconds {
			if cross := mediasim.CompareFrameSignaturesCross(sourceSig, candidateSig); cross.IsContentDuplicate() {
				log.Printf("[scriptcrawler] drive=%s source_id=%s content duplicate (cross) candidate=%s strong=%d/%d,%d/%d median_best=%.3f",
					c.cfg.Driver.ID(), source.ID, candidate.ID, cross.LeftStrong, cross.LeftFrames, cross.RightStrong, cross.RightFrames, cross.MedianBest)
				return &nearDuplicateMatch{
					video:       candidate,
					contentSSIM: cross.MedianBest,
				}, nil
			}
		}
		if cmp.IsContentNearMiss() {
			log.Printf("[scriptcrawler] drive=%s source_id=%s content near-miss candidate=%s median_ssim=%.3f min_ssim=%.3f comparisons=%d candidate_title=%q",
				c.cfg.Driver.ID(), source.ID, candidate.ID, cmp.MedianSSIM, cmp.MinSSIM, cmp.Comparisons, candidate.Title)
		}
	}
	return nil, nil
}
