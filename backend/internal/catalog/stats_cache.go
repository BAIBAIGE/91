package catalog

import (
	"context"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const statsRefreshTimeout = 10 * time.Second

// statsCache keeps one immutable snapshot. Cold callers serialize behind the
// mutex; once a value exists, expired callers receive it immediately while one
// detached refresh replaces it in the background.
type statsCache[T any] struct {
	mu         sync.Mutex
	key        string
	value      *T
	fetchedAt  time.Time
	refreshing bool
	generation uint64
}

func (c *statsCache[T]) get(
	ctx context.Context,
	refreshBase context.Context,
	label string,
	key string,
	freshFor time.Duration,
	load func(context.Context) (T, error),
) (T, error) {
	c.mu.Lock()
	if c.key != key {
		c.generation++
		c.key = key
		c.value = nil
		c.fetchedAt = time.Time{}
		c.refreshing = false
	}

	if c.value == nil {
		// Keep the lock while the first value is loaded. Other cold callers wait
		// here, then reuse the result instead of opening duplicate full scans.
		value, err := load(ctx)
		if err != nil {
			c.mu.Unlock()
			return value, err
		}
		stored := value
		c.value = &stored
		c.fetchedAt = time.Now()
		c.mu.Unlock()
		return value, nil
	}

	value := *c.value
	if freshFor > 0 && time.Since(c.fetchedAt) <= freshFor {
		c.mu.Unlock()
		return value, nil
	}

	if !c.refreshing {
		c.refreshing = true
		generation := c.generation
		go c.refresh(refreshBase, label, key, generation, load)
	}
	c.mu.Unlock()
	return value, nil
}

func (c *statsCache[T]) refresh(
	refreshBase context.Context,
	label string,
	key string,
	generation uint64,
	load func(context.Context) (T, error),
) {
	if refreshBase == nil {
		refreshBase = context.Background()
	}
	ctx, cancel := context.WithTimeout(refreshBase, statsRefreshTimeout)
	value, err := load(ctx)
	cancel()

	c.mu.Lock()
	if c.generation != generation || c.key != key {
		c.mu.Unlock()
		return
	}
	c.refreshing = false
	if err == nil {
		stored := value
		c.value = &stored
		c.fetchedAt = time.Now()
	}
	c.mu.Unlock()

	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[catalog] refresh %s stats: %v", label, err)
	}
}

func (c *statsCache[T]) invalidate() {
	c.mu.Lock()
	c.generation++
	c.key = ""
	c.value = nil
	c.fetchedAt = time.Time{}
	c.refreshing = false
	c.mu.Unlock()
}

// CrawlerAssetStatsSpec describes the stable ID namespace owned by one
// configured crawler. The list cache is keyed by the normalized complete set.
type CrawlerAssetStatsSpec struct {
	ID       string
	Prefixes []string
}

// CachedDriveAssetStats returns the combined drive snapshot using
// stale-while-revalidate after freshFor. CountDriveAssetStats remains the exact
// path for callers that cannot tolerate stale data.
func (c *Catalog) CachedDriveAssetStats(
	ctx context.Context,
	freshFor time.Duration,
) (DriveAssetStats, error) {
	stats, err := c.driveStats.get(
		ctx,
		c.statsContext,
		"drive asset",
		"all-drives",
		freshFor,
		c.CountDriveAssetStats,
	)
	if err != nil {
		return DriveAssetStats{}, err
	}
	return cloneDriveAssetStats(stats), nil
}

// CachedCrawlerAssetStats caches the complete crawler-list result as one
// snapshot. Adding, removing, or changing a crawler namespace changes the key
// and forces one exact cold load.
func (c *Catalog) CachedCrawlerAssetStats(
	ctx context.Context,
	specs []CrawlerAssetStatsSpec,
	freshFor time.Duration,
) (map[string]CrawlerAssetCounts, error) {
	normalized := normalizeCrawlerAssetStatsSpecs(specs)
	key := crawlerAssetStatsCacheKey(normalized)
	stats, err := c.crawlerStats.get(
		ctx,
		c.statsContext,
		"crawler asset",
		key,
		freshFor,
		func(loadCtx context.Context) (map[string]CrawlerAssetCounts, error) {
			return c.countCrawlerAssetStats(loadCtx, normalized)
		},
	)
	if err != nil {
		return nil, err
	}
	return cloneMap(stats), nil
}

// InvalidateAssetStats removes both snapshots. The next list request performs
// an exact blocking load, so explicit administrator mutations are reflected in
// that response rather than after one additional stale polling cycle.
func (c *Catalog) InvalidateAssetStats() {
	if c == nil {
		return
	}
	c.driveStats.invalidate()
	c.crawlerStats.invalidate()
}

func (c *Catalog) countCrawlerAssetStats(
	ctx context.Context,
	specs []CrawlerAssetStatsSpec,
) (map[string]CrawlerAssetCounts, error) {
	out := make(map[string]CrawlerAssetCounts, len(specs))
	for _, spec := range specs {
		counts, err := c.CountCrawlerAssets(ctx, spec.ID, spec.Prefixes)
		if err != nil {
			return nil, err
		}
		out[spec.ID] = counts
	}
	return out, nil
}

func normalizeCrawlerAssetStatsSpecs(specs []CrawlerAssetStatsSpec) []CrawlerAssetStatsSpec {
	prefixesByID := make(map[string][]string, len(specs))
	for _, spec := range specs {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			continue
		}
		prefixesByID[id] = append(prefixesByID[id], spec.Prefixes...)
	}

	ids := make([]string, 0, len(prefixesByID))
	for id := range prefixesByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]CrawlerAssetStatsSpec, 0, len(ids))
	for _, id := range ids {
		prefixes := cleanCrawlerIDPrefixes(prefixesByID[id])
		sort.Strings(prefixes)
		out = append(out, CrawlerAssetStatsSpec{ID: id, Prefixes: prefixes})
	}
	return out
}

func crawlerAssetStatsCacheKey(specs []CrawlerAssetStatsSpec) string {
	var key strings.Builder
	key.WriteString("crawler-assets:")
	for _, spec := range specs {
		appendStatsCacheKeyPart(&key, spec.ID)
		for _, prefix := range spec.Prefixes {
			appendStatsCacheKeyPart(&key, prefix)
		}
		key.WriteByte(';')
	}
	return key.String()
}

func appendStatsCacheKeyPart(key *strings.Builder, value string) {
	key.WriteString(strconv.Itoa(len(value)))
	key.WriteByte(':')
	key.WriteString(value)
	key.WriteByte(',')
}

func cloneDriveAssetStats(stats DriveAssetStats) DriveAssetStats {
	return DriveAssetStats{
		Teasers:      cloneMap(stats.Teasers),
		Thumbnails:   cloneMap(stats.Thumbnails),
		Fingerprints: cloneMap(stats.Fingerprints),
	}
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	if source == nil {
		return nil
	}
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
