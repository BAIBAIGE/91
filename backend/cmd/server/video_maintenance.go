package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/video-site/backend/internal/catalog"
	"github.com/video-site/backend/internal/drives/localupload"
	"github.com/video-site/backend/internal/localpath"
	"github.com/video-site/backend/internal/mediaasset"
	"github.com/video-site/backend/internal/mediasim"
	"github.com/video-site/backend/internal/persistence"
	"github.com/video-site/backend/internal/videoname"
)

type duplicateVideoMaintenanceStats struct {
	VideosScanned       int
	ExactGroups         int
	ExactDeleted        int
	NearCandidates      int
	NearSSIMComparisons int
	NearGroups          int
	NearDeleted         int
	ContentCandidates   int
	ContentComparisons  int
	ContentCrossMatched int
	ContentNearMisses   int
	ContentGroups       int
	ContentDeleted      int
}

type nearDuplicateMaintenanceStats struct {
	Candidates      int
	SSIMComparisons int
	Groups          int
	Deleted         int
}

type videoMaintenanceCandidate struct {
	video         *catalog.Video
	thumbnailPath string
	assetScore    int
	titleKeys     []string
	titleQGrams   map[string]struct{}
	titleBuckets  []string
}

type fingerprintDuplicateKey struct {
	size    int64
	sampled string
}

func (a *App) cleanupDuplicateVideoAssets(ctx context.Context) error {
	if a == nil || a.cat == nil {
		return nil
	}
	a.maintainLocalUploadFileNames(ctx)
	localDir := ""
	if a.cfg != nil {
		localDir = strings.TrimSpace(a.cfg.Storage.LocalPreviewDir)
	}
	videos, err := a.cat.ListVideoMaintenanceCandidates(ctx)
	if err != nil {
		return err
	}
	stats := duplicateVideoMaintenanceStats{VideosScanned: len(videos)}
	if len(videos) == 0 {
		log.Printf("[dedupe-maintenance] no videos to maintain")
		return nil
	}

	deleted := make(map[string]struct{})
	exactGroups, exactDeleted, err := a.cleanupExactDuplicateVideos(ctx, localDir, videos, deleted)
	if err != nil {
		return err
	}
	stats.ExactGroups = exactGroups
	stats.ExactDeleted = exactDeleted

	remaining := make([]*catalog.Video, 0, len(videos)-len(deleted))
	for _, v := range videos {
		if v == nil {
			continue
		}
		if _, ok := deleted[v.ID]; ok {
			continue
		}
		remaining = append(remaining, v)
	}
	nearStats, err := a.cleanupNearDuplicateVideos(ctx, localDir, remaining, deleted)
	if err != nil {
		return err
	}
	stats.NearCandidates = nearStats.Candidates
	stats.NearSSIMComparisons = nearStats.SSIMComparisons
	stats.NearGroups = nearStats.Groups
	stats.NearDeleted = nearStats.Deleted

	remainingAfterNear := make([]*catalog.Video, 0, len(remaining))
	for _, v := range remaining {
		if v == nil {
			continue
		}
		if _, ok := deleted[v.ID]; ok {
			continue
		}
		remainingAfterNear = append(remainingAfterNear, v)
	}
	contentStats, err := a.cleanupContentDuplicateVideos(ctx, localDir, remainingAfterNear, deleted)
	if err != nil {
		return err
	}
	stats.ContentCandidates = contentStats.Candidates
	stats.ContentComparisons = contentStats.Comparisons
	stats.ContentCrossMatched = contentStats.CrossMatched
	stats.ContentNearMisses = contentStats.NearMisses
	stats.ContentGroups = contentStats.Groups
	stats.ContentDeleted = contentStats.Deleted

	if pruned, err := a.cat.PruneDuplicateReviewPairs(ctx); err != nil {
		log.Printf("[dedupe-maintenance] prune duplicate review pairs: %v", err)
	} else if pruned > 0 {
		log.Printf("[dedupe-maintenance] pruned %d stale duplicate review pairs", pruned)
	}

	log.Printf("[dedupe-maintenance] videos=%d exact_groups=%d exact_deleted=%d near_candidates=%d near_ssim_comparisons=%d near_groups=%d near_deleted=%d content_candidates=%d content_comparisons=%d content_cross_matched=%d content_near_misses=%d content_groups=%d content_deleted=%d",
		stats.VideosScanned, stats.ExactGroups, stats.ExactDeleted, stats.NearCandidates, stats.NearSSIMComparisons, stats.NearGroups, stats.NearDeleted,
		stats.ContentCandidates, stats.ContentComparisons, stats.ContentCrossMatched, stats.ContentNearMisses, stats.ContentGroups, stats.ContentDeleted)
	return nil
}

// maintainLocalUploadFileNames migrates legacy local uploads from random
// physical names to the user-visible title. It is intentionally best-effort:
// one invalid or missing legacy file must not block the nightly maintenance
// pipeline for every other video.
func (a *App) maintainLocalUploadFileNames(ctx context.Context) {
	if a == nil || a.cat == nil {
		return
	}
	root := a.localUploadDir()
	videos, err := a.cat.ListVideosByDrive(ctx, localupload.DriveID)
	if err != nil {
		log.Printf("[local-upload-maintenance] list videos: %v", err)
		return
	}
	renamed, skipped := 0, 0
	for _, v := range videos {
		if err := ctx.Err(); err != nil {
			return
		}
		if v == nil || filepath.Base(v.FileID) != v.FileID {
			skipped++
			continue
		}
		title := strings.TrimSpace(v.Title)
		if title == "" {
			title = videoname.TitleFromFileName(v.FileName)
		}
		ext := filepath.Ext(v.FileID)
		if ext == "" && strings.TrimSpace(v.Ext) != "" {
			ext = "." + strings.TrimPrefix(strings.TrimSpace(v.Ext), ".")
		}
		if err := videoname.ValidateUploadTitle(title, ext); err != nil {
			log.Printf("[local-upload-maintenance] skip id=%s invalid title=%q: %v", v.ID, title, err)
			skipped++
			continue
		}

		newName := videoname.UploadFileName(title, ext, v.FileID, false)
		oldPath := filepath.Join(root, v.FileID)
		newPath := filepath.Join(root, newName)
		if newName != v.FileID {
			if _, err := os.Stat(oldPath); err != nil {
				log.Printf("[local-upload-maintenance] skip id=%s source=%q: %v", v.ID, v.FileID, err)
				skipped++
				continue
			}
			if _, err := os.Stat(newPath); err == nil {
				uploadID := strings.TrimSuffix(v.FileID, filepath.Ext(v.FileID))
				newName = videoname.UploadFileName(title, ext, uploadID, true)
				newPath = filepath.Join(root, newName)
				if _, err := os.Stat(newPath); err == nil || !os.IsNotExist(err) {
					log.Printf("[local-upload-maintenance] skip id=%s collision=%q", v.ID, newName)
					skipped++
					continue
				}
			} else if !os.IsNotExist(err) {
				log.Printf("[local-upload-maintenance] skip id=%s destination=%q: %v", v.ID, newName, err)
				skipped++
				continue
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				log.Printf("[local-upload-maintenance] rename id=%s %q -> %q: %v", v.ID, v.FileID, newName, err)
				skipped++
				continue
			}
		}

		finalTitle := videoname.TitleFromFileName(newName)
		if err := a.cat.UpdateVideoFileIdentity(ctx, v.ID, newName, newName, finalTitle); err != nil {
			if newName != v.FileID {
				if rollbackErr := os.Rename(newPath, oldPath); rollbackErr != nil {
					log.Printf("[local-upload-maintenance] rollback id=%s: %v", v.ID, rollbackErr)
				}
			}
			log.Printf("[local-upload-maintenance] catalog id=%s: %v", v.ID, err)
			skipped++
			continue
		}
		if newName != v.FileID || v.FileName != newName || v.Title != finalTitle {
			renamed++
		}
	}
	log.Printf("[local-upload-maintenance] videos=%d updated=%d skipped=%d", len(videos), renamed, skipped)
}

func (a *App) cleanupExactDuplicateVideos(ctx context.Context, localDir string, videos []*catalog.Video, deleted map[string]struct{}) (int, int, error) {
	groups := make(map[fingerprintDuplicateKey][]*catalog.Video)
	for _, v := range videos {
		if v == nil {
			continue
		}
		if _, ok := deleted[v.ID]; ok {
			continue
		}
		sampled := strings.ToLower(strings.TrimSpace(v.SampledSHA256))
		if v.Size <= 0 || sampled == "" {
			continue
		}
		key := fingerprintDuplicateKey{size: v.Size, sampled: sampled}
		groups[key] = append(groups[key], v)
	}

	keys := make([]fingerprintDuplicateKey, 0, len(groups))
	for key := range groups {
		if len(groups[key]) > 1 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].size != keys[j].size {
			return keys[i].size < keys[j].size
		}
		return keys[i].sampled < keys[j].sampled
	})

	groupCount := 0
	deletedCount := 0
	for _, key := range keys {
		group := groups[key]
		canonical := selectExactDuplicateCanonical(localDir, group)
		if canonical == nil {
			continue
		}
		groupCount++
		for _, v := range group {
			if v == nil || v.ID == canonical.ID {
				continue
			}
			if _, ok := deleted[v.ID]; ok {
				continue
			}
			if err := a.deleteDuplicateVideoWithAssets(ctx, localDir, v, canonical.ID); err != nil {
				return groupCount, deletedCount, fmt.Errorf("exact duplicate size=%d sampled=%s: %w", key.size, shortHashForLog(key.sampled), err)
			}
			deleted[v.ID] = struct{}{}
			deletedCount++
			log.Printf("[dedupe-maintenance] exact duplicate deleted id=%s canonical=%s size=%d sampled=%s", v.ID, canonical.ID, key.size, shortHashForLog(key.sampled))
		}
	}
	return groupCount, deletedCount, nil
}

func (a *App) cleanupNearDuplicateVideos(ctx context.Context, localDir string, videos []*catalog.Video, deleted map[string]struct{}) (nearDuplicateMaintenanceStats, error) {
	candidates := collectNearDuplicateMaintenanceCandidates(localDir, videos, deleted)
	stats := nearDuplicateMaintenanceStats{Candidates: len(candidates)}
	if len(candidates) < 2 {
		return stats, nil
	}

	sets := newVideoMaintenanceDisjointSet(len(candidates))
	bucketIndex := make(map[int]map[string][]int)
	seenPairs := make(map[uint64]struct{})
	for i, right := range candidates {
		if right.video == nil {
			continue
		}
		for duration := right.video.DurationSeconds - mediasim.NearDuplicateDurationToleranceSeconds; duration <= right.video.DurationSeconds+mediasim.NearDuplicateDurationToleranceSeconds; duration++ {
			byBucket := bucketIndex[duration]
			if len(byBucket) == 0 {
				continue
			}
			for _, bucket := range right.titleBuckets {
				for _, j := range byBucket[bucket] {
					if j == i {
						continue
					}
					pairKey := videoMaintenancePairKey(i, j)
					if _, ok := seenPairs[pairKey]; ok {
						continue
					}
					seenPairs[pairKey] = struct{}{}
					left := candidates[j]
					if left.video == nil {
						continue
					}
					if !nearDuplicateTitlePrefilter(left, right) {
						continue
					}
					titleScore := mediasim.TitleSimilarity(left.video.Title, right.video.Title)
					if titleScore < mediasim.NearDuplicateTitleThreshold {
						continue
					}
					stats.SSIMComparisons++
					ssimScore, err := mediasim.ImageSSIM(left.thumbnailPath, right.thumbnailPath)
					if err != nil {
						log.Printf("[dedupe-maintenance] thumbnail ssim failed left=%s right=%s: %v", left.video.ID, right.video.ID, err)
						continue
					}
					if ssimScore >= mediasim.NearDuplicateThumbSSIMThreshold {
						sets.union(i, j)
					}
				}
			}
		}
		if len(right.titleBuckets) == 0 {
			continue
		}
		byBucket := bucketIndex[right.video.DurationSeconds]
		if byBucket == nil {
			byBucket = make(map[string][]int)
			bucketIndex[right.video.DurationSeconds] = byBucket
		}
		for _, bucket := range right.titleBuckets {
			byBucket[bucket] = append(byBucket[bucket], i)
		}
	}

	groups := make(map[int][]videoMaintenanceCandidate)
	for i, candidate := range candidates {
		root := sets.find(i)
		groups[root] = append(groups[root], candidate)
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
				return stats, fmt.Errorf("near duplicate canonical=%s duplicate=%s: %w", canonical.video.ID, v.ID, err)
			}
			deleted[v.ID] = struct{}{}
			stats.Deleted++
			log.Printf("[dedupe-maintenance] near duplicate deleted id=%s canonical=%s size=%d canonical_size=%d duration=%d title=%q",
				v.ID, canonical.video.ID, v.Size, canonical.video.Size, v.DurationSeconds, v.Title)
		}
	}
	return stats, nil
}

func collectNearDuplicateMaintenanceCandidates(localDir string, videos []*catalog.Video, deleted map[string]struct{}) []videoMaintenanceCandidate {
	localDir = strings.TrimSpace(localDir)
	if localDir == "" {
		return nil
	}
	out := make([]videoMaintenanceCandidate, 0, len(videos))
	for _, v := range videos {
		if v == nil || strings.TrimSpace(v.ID) == "" {
			continue
		}
		if _, ok := deleted[v.ID]; ok {
			continue
		}
		if strings.TrimSpace(v.Title) == "" || v.DurationSeconds <= 0 {
			continue
		}
		titleKeys := mediasim.TitleKeys(v.Title)
		if len(titleKeys) == 0 {
			continue
		}
		titleBuckets := titlePrefixBuckets(titleKeys, 12)
		if len(titleBuckets) == 0 {
			continue
		}
		thumbPath, ok := localGeneratedThumbnailPath(localDir, v)
		if !ok {
			continue
		}
		out = append(out, videoMaintenanceCandidate{
			video:         v,
			thumbnailPath: thumbPath,
			assetScore:    videoAssetCompletenessScore(localDir, v),
			titleKeys:     titleKeys,
			titleQGrams:   titleQGrams(titleKeys, 4),
			titleBuckets:  titleBuckets,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].video
		right := out[j].video
		if left.DurationSeconds != right.DurationSeconds {
			return left.DurationSeconds < right.DurationSeconds
		}
		return earlierVideo(left, right)
	})
	return out
}

func nearDuplicateTitlePrefilter(left, right videoMaintenanceCandidate) bool {
	if !titleLengthCouldReachThreshold(left.titleKeys, right.titleKeys, mediasim.NearDuplicateTitleThreshold) {
		return false
	}
	return qGramContainment(left.titleQGrams, right.titleQGrams) >= 0.45
}

func videoMaintenancePairKey(left, right int) uint64 {
	if left > right {
		left, right = right, left
	}
	return uint64(uint32(left))<<32 | uint64(uint32(right))
}

func titlePrefixBuckets(keys []string, prefixRunes int) []string {
	if prefixRunes <= 0 {
		prefixRunes = 12
	}
	seen := make(map[string]struct{})
	var out []string
	var fallback []string
	for _, key := range keys {
		runes := []rune(key)
		if len(runes) == 0 {
			continue
		}
		if len(runes) > prefixRunes {
			runes = runes[:prefixRunes]
		}
		bucket := string(runes)
		if _, ok := seen[bucket]; ok {
			continue
		}
		seen[bucket] = struct{}{}
		if lowInformationTitleBucket(bucket) {
			fallback = append(fallback, bucket)
			continue
		}
		out = append(out, bucket)
	}
	if len(out) > 0 {
		return out
	}
	return fallback
}

func lowInformationTitleBucket(bucket string) bool {
	if strings.HasPrefix(bucket, "www") {
		return true
	}
	if strings.Contains(bucket, "com") {
		limit := len(bucket)
		if limit > 8 {
			limit = 8
		}
		for _, r := range bucket[:limit] {
			if r >= '0' && r <= '9' {
				return true
			}
		}
	}
	return false
}

func titleLengthCouldReachThreshold(leftKeys, rightKeys []string, threshold float64) bool {
	for _, left := range leftKeys {
		leftLen := len([]rune(left))
		if leftLen == 0 {
			continue
		}
		for _, right := range rightKeys {
			rightLen := len([]rune(right))
			if rightLen == 0 {
				continue
			}
			maxLen := leftLen
			minLen := rightLen
			if rightLen > maxLen {
				maxLen = rightLen
				minLen = leftLen
			}
			if float64(minLen)/float64(maxLen) >= threshold {
				return true
			}
		}
	}
	return false
}

func titleQGrams(keys []string, n int) map[string]struct{} {
	out := make(map[string]struct{})
	if n <= 0 {
		n = 4
	}
	for _, key := range keys {
		runes := []rune(key)
		if len(runes) == 0 {
			continue
		}
		if len(runes) <= n {
			out[string(runes)] = struct{}{}
			continue
		}
		for i := 0; i+n <= len(runes); i++ {
			out[string(runes[i:i+n])] = struct{}{}
		}
	}
	return out
}

func qGramContainment(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	smaller := left
	larger := right
	if len(right) < len(left) {
		smaller = right
		larger = left
	}
	common := 0
	for gram := range smaller {
		if _, ok := larger[gram]; ok {
			common++
		}
	}
	return float64(common) / float64(len(smaller))
}

func selectExactDuplicateCanonical(localDir string, group []*catalog.Video) *catalog.Video {
	var best *catalog.Video
	for _, v := range group {
		if v == nil {
			continue
		}
		if best == nil || betterExactDuplicateCanonical(localDir, v, best) {
			best = v
		}
	}
	return best
}

func betterExactDuplicateCanonical(localDir string, left, right *catalog.Video) bool {
	leftScore := videoAssetCompletenessScore(localDir, left)
	rightScore := videoAssetCompletenessScore(localDir, right)
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	return earlierVideo(left, right)
}

func selectNearDuplicateCanonical(group []videoMaintenanceCandidate) videoMaintenanceCandidate {
	var best videoMaintenanceCandidate
	for _, candidate := range group {
		if candidate.video == nil {
			continue
		}
		if best.video == nil || betterNearDuplicateCanonical(candidate, best) {
			best = candidate
		}
	}
	return best
}

func betterNearDuplicateCanonical(left, right videoMaintenanceCandidate) bool {
	if right.video == nil {
		return true
	}
	if left.video == nil {
		return false
	}
	if left.video.Size != right.video.Size {
		return left.video.Size > right.video.Size
	}
	if left.assetScore != right.assetScore {
		return left.assetScore > right.assetScore
	}
	return earlierVideo(left.video, right.video)
}

func earlierVideo(left, right *catalog.Video) bool {
	if right == nil {
		return true
	}
	if left == nil {
		return false
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.ID < right.ID
}

func videoAssetCompletenessScore(localDir string, v *catalog.Video) int {
	if v == nil {
		return 0
	}
	score := 0
	if localGeneratedPreviewReady(localDir, v) {
		score++
	}
	if _, ok := localGeneratedThumbnailPath(localDir, v); ok {
		score++
	}
	if strings.TrimSpace(v.SampledSHA256) != "" && strings.TrimSpace(v.FingerprintStatus) == "ready" {
		score++
	}
	return score
}

func localGeneratedPreviewReady(localDir string, v *catalog.Video) bool {
	if v == nil || strings.TrimSpace(v.PreviewStatus) != "ready" || strings.TrimSpace(v.PreviewLocal) == "" {
		return false
	}
	localDir = strings.TrimSpace(localDir)
	if localDir == "" {
		return true
	}
	_, ok := localGeneratedPreviewPath(localDir, v)
	return ok
}

// localGeneratedPreviewPath 返回本地 teaser 的实际路径；仅当预览就绪、
// 路径落在 localDir 内且文件存在时才可用。
func localGeneratedPreviewPath(localDir string, v *catalog.Video) (string, bool) {
	if v == nil || strings.TrimSpace(v.PreviewStatus) != "ready" || strings.TrimSpace(v.PreviewLocal) == "" {
		return "", false
	}
	if strings.TrimSpace(localDir) == "" {
		return "", false
	}
	clean, ok := localPathWithin(localDir, v.PreviewLocal)
	if !ok {
		return "", false
	}
	if !regularFileExists(clean) {
		return "", false
	}
	return clean, true
}

func localGeneratedThumbnailPath(localDir string, v *catalog.Video) (string, bool) {
	if v == nil || strings.TrimSpace(localDir) == "" || strings.TrimSpace(v.ID) == "" {
		return "", false
	}
	if strings.TrimSpace(v.ThumbnailURL) != "/p/thumb/"+v.ID {
		return "", false
	}
	for _, candidate := range mediaasset.ThumbnailPathCandidates(localDir, v.ID) {
		clean, ok := localPathWithin(localDir, candidate)
		if !ok {
			continue
		}
		if regularFileExists(clean) {
			return clean, true
		}
	}
	return "", false
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (a *App) deleteDuplicateVideoWithAssets(ctx context.Context, localDir string, v *catalog.Video, canonicalID string) error {
	if v == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	persistence.RLock()
	defer persistence.RUnlock()
	if err := removeLocalVideoAssets(localDir, v); err != nil {
		return fmt.Errorf("remove local assets for %s: %w", v.ID, err)
	}
	var lastErr error
	for attempt := 0; attempt < 12; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.cat.DeleteVideoWithTombstoneOptions(ctx, v.ID, catalog.DeleteVideoTombstoneOptions{
			Reason:           catalog.DeletedVideoReasonDuplicate,
			CanonicalVideoID: canonicalID,
		}); err != nil {
			if !isSQLiteBusyError(err) {
				return fmt.Errorf("delete catalog video %s canonical=%s: %w", v.ID, canonicalID, err)
			}
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("delete catalog video %s canonical=%s after retries: %w", v.ID, canonicalID, lastErr)
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

func shortHashForLog(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

type videoMaintenanceDisjointSet struct {
	parent []int
	rank   []int
}

func newVideoMaintenanceDisjointSet(n int) *videoMaintenanceDisjointSet {
	parent := make([]int, n)
	rank := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	return &videoMaintenanceDisjointSet{parent: parent, rank: rank}
}

func (s *videoMaintenanceDisjointSet) find(x int) int {
	if s.parent[x] != x {
		s.parent[x] = s.find(s.parent[x])
	}
	return s.parent[x]
}

func (s *videoMaintenanceDisjointSet) union(a, b int) {
	rootA := s.find(a)
	rootB := s.find(b)
	if rootA == rootB {
		return
	}
	if s.rank[rootA] < s.rank[rootB] {
		s.parent[rootA] = rootB
		return
	}
	if s.rank[rootA] > s.rank[rootB] {
		s.parent[rootB] = rootA
		return
	}
	s.parent[rootB] = rootA
	s.rank[rootA]++
}

func localPathWithin(root, path string) (string, bool) {
	return localpath.Within(root, path)
}
