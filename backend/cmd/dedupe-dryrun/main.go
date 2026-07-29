// dedupe-dryrun：预演/执行夜间维护的内容级查重通道（Phase 5 content channel）。
// 按与生产完全相同的判定规则（mediasim 阈值常量）打印将被合并的重复分组、
// 保留/删除决策和疑似区（near-miss）名单。
//
// 默认只读，不写库、不删文件；加 -apply 后真正执行：删除项按重复墓碑落库并
// 清理本地资产（与夜间维护同一条路径），near-miss 写入复核队列。
//
// 用法：在 backend 目录下运行
//
//	go run ./cmd/dedupe-dryrun -db data/video-site.db -local-dir data/previews [-apply]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/mediaasset"
	"github.com/video-site/backend/internal/mediasim"
)

const durationToleranceSeconds = mediasim.NearDuplicateDurationToleranceSeconds

type candidate struct {
	video      *catalog.Video
	teaserPath string
}

func main() {
	dbPath := flag.String("db", "data/video-site.db", "sqlite path")
	localDir := flag.String("local-dir", "data/previews", "本地预览目录(config storage.local_preview_dir)")
	ffmpegPath := flag.String("ffmpeg", "ffmpeg", "ffmpeg 路径")
	workers := flag.Int("workers", 8, "签名提取并发数")
	apply := flag.Bool("apply", false, "真正执行：删除重复项并写入复核队列（默认只读预演）")
	flag.Parse()

	cat, err := catalog.Open(*dbPath)
	if err != nil {
		log.Fatalf("open catalog: %v", err)
	}
	defer cat.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	videos, err := cat.ListVideoMaintenanceCandidates(ctx)
	if err != nil {
		log.Fatalf("list videos: %v", err)
	}

	localAbs, err := filepath.Abs(*localDir)
	if err != nil {
		log.Fatalf("local dir: %v", err)
	}
	var candidates []candidate
	for _, v := range videos {
		if v == nil || v.DurationSeconds < mediasim.ContentDuplicateMinDurationSeconds {
			continue
		}
		if strings.TrimSpace(v.PreviewStatus) != "ready" || strings.TrimSpace(v.PreviewLocal) == "" {
			continue
		}
		pathAbs, err := filepath.Abs(v.PreviewLocal)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(localAbs, pathAbs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if info, err := os.Stat(pathAbs); err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidates = append(candidates, candidate{video: v, teaserPath: pathAbs})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].video.DurationSeconds != candidates[j].video.DurationSeconds {
			return candidates[i].video.DurationSeconds < candidates[j].video.DurationSeconds
		}
		return candidates[i].video.ID < candidates[j].video.ID
	})
	fmt.Fprintf(os.Stderr, "videos=%d content_candidates=%d\n", len(videos), len(candidates))

	// 找出参与 ±tolerance 配对的视频，先并发提取签名。
	involved := make(map[int]struct{})
	for i := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].video.DurationSeconds-candidates[i].video.DurationSeconds > durationToleranceSeconds {
				break
			}
			involved[i] = struct{}{}
			involved[j] = struct{}{}
		}
	}
	fmt.Fprintf(os.Stderr, "involved_in_pairs=%d, extracting signatures...\n", len(involved))

	sigs := make(map[int]*mediasim.FrameSignature)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, *workers)
	done := 0
	cacheHits := 0
	for i := range involved {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cachePath := mediaasset.FrameSignaturePath(localAbs, candidates[i].video.ID)
			sig, cached := mediasim.LoadCachedTeaserSignature(cachePath, candidates[i].teaserPath)
			var err error
			if !cached {
				sig, err = mediasim.ExtractTeaserFrameSignature(ctx, *ffmpegPath, candidates[i].teaserPath)
				if err == nil {
					if storeErr := mediasim.StoreCachedTeaserSignature(cachePath, candidates[i].teaserPath, sig); storeErr != nil {
						fmt.Fprintf(os.Stderr, "  cache write failed id=%s: %v\n", candidates[i].video.ID, storeErr)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			done++
			if cached {
				cacheHits++
			}
			if done%300 == 0 {
				fmt.Fprintf(os.Stderr, "  %d/%d (cache hits %d)\n", done, len(involved), cacheHits)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "  extract failed id=%s: %v\n", candidates[i].video.ID, err)
				return
			}
			if sig.InformativeFrames() < mediasim.ContentDuplicateMinComparisons {
				return
			}
			sigs[i] = sig
		}(i)
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "signatures=%d cache_hits=%d\n", len(sigs), cacheHits)

	parent := make([]int, len(candidates))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	type nearMiss struct {
		left, right *catalog.Video
		cmp         mediasim.FrameSignatureComparison
	}
	var nearMisses []nearMiss
	matched := 0
	crossMatched := 0
	for i := range candidates {
		if sigs[i] == nil {
			continue
		}
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].video.DurationSeconds-candidates[i].video.DurationSeconds > durationToleranceSeconds {
				break
			}
			if sigs[j] == nil {
				continue
			}
			cmp := mediasim.CompareFrameSignatures(sigs[i], sigs[j])
			if cmp.IsContentDuplicate() {
				union(i, j)
				matched++
				continue
			}
			if candidates[i].video.DurationSeconds == candidates[j].video.DurationSeconds {
				if cross := mediasim.CompareFrameSignaturesCross(sigs[i], sigs[j]); cross.IsContentDuplicate() {
					union(i, j)
					crossMatched++
					fmt.Printf("[交叉命中] %s (%q) <-> %s (%q) strong=%d/%d,%d/%d median_best=%.3f dur=%d\n",
						candidates[i].video.ID, candidates[i].video.Title, candidates[j].video.ID, candidates[j].video.Title,
						cross.LeftStrong, cross.LeftFrames, cross.RightStrong, cross.RightFrames, cross.MedianBest, candidates[i].video.DurationSeconds)
					continue
				}
			}
			if cmp.IsContentNearMiss() {
				nearMisses = append(nearMisses, nearMiss{candidates[i].video, candidates[j].video, cmp})
			}
		}
	}

	groups := make(map[int][]candidate)
	for i := range candidates {
		if sigs[i] == nil {
			continue
		}
		root := find(i)
		groups[root] = append(groups[root], candidates[i])
	}
	var multi [][]candidate
	for _, group := range groups {
		if len(group) > 1 {
			multi = append(multi, group)
		}
	}
	sort.Slice(multi, func(i, j int) bool { return multi[i][0].video.ID < multi[j][0].video.ID })

	fmt.Printf("\n=== 内容级重复分组：%d 组（对齐命中 %d 次，交叉命中 %d 次）===\n", len(multi), matched, crossMatched)
	wouldDelete := 0
	deleted := 0
	deleteFailed := 0
	for gi, group := range multi {
		canonicalIdx := 0
		for k := 1; k < len(group); k++ {
			if betterCanonical(*localDir, group[k].video, group[canonicalIdx].video) {
				canonicalIdx = k
			}
		}
		fmt.Printf("\n组 %d（时长 %ds）：\n", gi+1, group[0].video.DurationSeconds)
		for k, c := range group {
			marker := "删除"
			if k == canonicalIdx {
				marker = "保留"
			} else {
				wouldDelete++
			}
			fmt.Printf("  [%s] %s size=%d drive=%s title=%q\n", marker, c.video.ID, c.video.Size, c.video.DriveID, c.video.Title)
		}
		if *apply {
			canonicalID := group[canonicalIdx].video.ID
			for k, c := range group {
				if k == canonicalIdx {
					continue
				}
				if err := deleteDuplicateWithAssets(ctx, cat, localAbs, c.video, canonicalID); err != nil {
					deleteFailed++
					fmt.Fprintf(os.Stderr, "  删除失败 id=%s: %v\n", c.video.ID, err)
					continue
				}
				deleted++
			}
		}
	}
	if *apply {
		fmt.Printf("\n已删除 %d 个视频（失败 %d）。\n", deleted, deleteFailed)
	} else {
		fmt.Printf("\n将删除 %d 个视频（只读预演，加 -apply 执行）。\n", wouldDelete)
	}

	fmt.Printf("\n=== 疑似区（%s）：%d 对 ===\n",
		map[bool]string{true: "已写入后台复核队列", false: "不自动处理，供人工复核"}[*apply], len(nearMisses))
	queued := 0
	for _, nm := range nearMisses {
		fmt.Printf("  median=%.3f min=%.3f n=%d  %s (%q)  <->  %s (%q)\n",
			nm.cmp.MedianSSIM, nm.cmp.MinSSIM, nm.cmp.Comparisons, nm.left.ID, nm.left.Title, nm.right.ID, nm.right.Title)
		if *apply {
			if err := cat.UpsertDuplicateReviewPair(ctx, nm.left.ID, nm.right.ID, nm.cmp.MedianSSIM, nm.cmp.MinSSIM, nm.cmp.Comparisons); err != nil {
				fmt.Fprintf(os.Stderr, "  入队失败 %s|%s: %v\n", nm.left.ID, nm.right.ID, err)
				continue
			}
			queued++
		}
	}
	if *apply {
		if pruned, err := cat.PruneDuplicateReviewPairs(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "清理失效复核对失败: %v\n", err)
		} else if pruned > 0 {
			fmt.Printf("已清理 %d 个失效复核对。\n", pruned)
		}
		fmt.Printf("复核队列新写入/刷新 %d 对。\n", queued)
	}
}

// deleteDuplicateWithAssets 与 cmd/server 夜间维护的 deleteDuplicateVideoWithAssets
// 行为一致：先清本地资产（teaser、封面及其派生图、帧签名缓存），再按重复墓碑
// 删除 catalog 行；SQLite busy 时退避重试。
func deleteDuplicateWithAssets(ctx context.Context, cat *catalog.Catalog, localDir string, v *catalog.Video, canonicalID string) error {
	removeCandidates := []string{v.PreviewLocal}
	removeCandidates = append(removeCandidates, mediaasset.PreviewPathCandidates(localDir, v.ID)...)
	removeCandidates = append(removeCandidates, mediaasset.ThumbnailAssetPathCandidates(localDir, v.ID)...)
	removeCandidates = append(removeCandidates, mediaasset.FrameSignaturePath(localDir, v.ID))
	seen := map[string]struct{}{}
	for _, candidate := range removeCandidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		pathAbs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(localDir, pathAbs); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if _, ok := seen[pathAbs]; ok {
			continue
		}
		seen[pathAbs] = struct{}{}
		if info, err := os.Stat(pathAbs); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(pathAbs); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove asset %s: %w", pathAbs, err)
		}
	}
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := cat.DeleteVideoWithTombstoneOptions(ctx, v.ID, catalog.DeleteVideoTombstoneOptions{
			Reason:           catalog.DeletedVideoReasonDuplicate,
			CanonicalVideoID: canonicalID,
		})
		if err == nil {
			return nil
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "busy") && !strings.Contains(msg, "locked") {
			return err
		}
		lastErr = err
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
	return fmt.Errorf("delete %s after retries: %w", v.ID, lastErr)
}

// betterCanonical 与 cmd/server 夜间维护的 betterNearDuplicateCanonical 规则一致：
// 体积大者优先，其次本地资产完整度，最后入库早者。
func betterCanonical(localDir string, left, right *catalog.Video) bool {
	if left.Size != right.Size {
		return left.Size > right.Size
	}
	leftScore, rightScore := assetScore(localDir, left), assetScore(localDir, right)
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.ID < right.ID
}

func assetScore(localDir string, v *catalog.Video) int {
	score := 0
	if strings.TrimSpace(v.PreviewStatus) == "ready" && strings.TrimSpace(v.PreviewLocal) != "" {
		if info, err := os.Stat(v.PreviewLocal); err == nil && info.Mode().IsRegular() {
			score++
		}
	}
	if strings.TrimSpace(v.ThumbnailURL) == "/p/thumb/"+v.ID {
		for _, p := range mediaasset.ThumbnailPathCandidates(localDir, v.ID) {
			if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
				score++
				break
			}
		}
	}
	if strings.TrimSpace(v.SampledSHA256) != "" && strings.TrimSpace(v.FingerprintStatus) == "ready" {
		score++
	}
	return score
}
