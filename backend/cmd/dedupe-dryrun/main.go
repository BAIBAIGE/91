// dedupe-dryrun：只读预演夜间维护的内容级查重通道（Phase 5 content channel）。
// 按与生产完全相同的判定规则（mediasim 阈值常量）打印将被合并的重复分组、
// 保留/删除决策和疑似区（near-miss）名单，不写库、不删文件。
//
// 用法：在 backend 目录下运行
//
//	go run ./cmd/dedupe-dryrun -db data/video-site.db -local-dir data/previews
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

const durationToleranceSeconds = 2 // 与 cmd/server 的 videoMaintenanceDurationToleranceSeconds 保持一致

type candidate struct {
	video      *catalog.Video
	teaserPath string
}

func main() {
	dbPath := flag.String("db", "data/video-site.db", "sqlite path")
	localDir := flag.String("local-dir", "data/previews", "本地预览目录(config storage.local_preview_dir)")
	ffmpegPath := flag.String("ffmpeg", "ffmpeg", "ffmpeg 路径")
	workers := flag.Int("workers", 8, "签名提取并发数")
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
	for i := range involved {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sig, err := mediasim.ExtractTeaserFrameSignature(ctx, *ffmpegPath, candidates[i].teaserPath)
			mu.Lock()
			defer mu.Unlock()
			done++
			if done%300 == 0 {
				fmt.Fprintf(os.Stderr, "  %d/%d\n", done, len(involved))
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
	fmt.Fprintf(os.Stderr, "signatures=%d\n", len(sigs))

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
			} else if cmp.IsContentNearMiss() {
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

	fmt.Printf("\n=== 内容级重复分组：%d 组（配对命中 %d 次）===\n", len(multi), matched)
	wouldDelete := 0
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
	}
	fmt.Printf("\n将删除 %d 个视频。\n", wouldDelete)

	fmt.Printf("\n=== 疑似区（不自动处理，供人工复核）：%d 对 ===\n", len(nearMisses))
	for _, nm := range nearMisses {
		fmt.Printf("  median=%.3f min=%.3f n=%d  %s (%q)  <->  %s (%q)\n",
			nm.cmp.MedianSSIM, nm.cmp.MinSSIM, nm.cmp.Comparisons, nm.left.ID, nm.left.Title, nm.right.ID, nm.right.Title)
	}
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
