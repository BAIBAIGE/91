import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useSearchParams } from "react-router";
import { AppShell } from "@/components/AppShell";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { TagCloud } from "@/components/TagCloud";
import { SectionHeader } from "@/components/SectionHeader";
import { SortToolbar, type ViewMode } from "@/components/SortToolbar";
import { VideoGrid } from "@/components/VideoGrid";
import { Pagination } from "@/components/Pagination";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { fetchHomeVideos, fetchListing } from "@/data/videos";
import { MOBILE_VIDEO_PAGE_SIZE, useIsMobile } from "@/lib/responsive";
import type { SortKey, VideoItem } from "@/types";

const DESKTOP_COUNT = 12;
const MOBILE_COUNT = 8;
const HOME_SEARCH_DESKTOP_PAGE_SIZE = 20;
const LATEST_POOL_SIZE = 96;
const HOME_LATEST_CURSOR_KEY = "home.latest.cursor";

// 模块级缓存：SPA 生命周期内保持，刷新页面时重置
let cachedRanking: VideoItem[] | null = null;
let cachedLatestPool: VideoItem[] | null = null;
let cachedLatestBatch: VideoItem[] | null = null;

function loadLatestCursor(poolLength: number): number {
  if (poolLength <= 0) return 0;
  try {
    const raw = window.localStorage.getItem(HOME_LATEST_CURSOR_KEY);
    const parsed = raw ? Number.parseInt(raw, 10) : 0;
    return Number.isFinite(parsed) && parsed >= 0 ? parsed % poolLength : 0;
  } catch {
    return 0;
  }
}

function saveLatestCursor(cursor: number) {
  try {
    window.localStorage.setItem(HOME_LATEST_CURSOR_KEY, String(cursor));
  } catch {
    // localStorage 不可用时只影响跨刷新循环进度，不影响展示。
  }
}

function nextLatestBatch(items: VideoItem[], count: number): VideoItem[] {
  if (items.length === 0 || count <= 0) return [];
  if (items.length <= count) {
    saveLatestCursor(0);
    return items;
  }

  const start = loadLatestCursor(items.length);
  const batch: VideoItem[] = [];
  // 缓存最多 12 条以便页面在桌面/手机断点之间切换，但续取游标只前进
  // 实际展示数量；手机显示 8 条时不会再悄悄跳过后面的 4 条。
  const batchSize = Math.min(DESKTOP_COUNT, items.length);
  for (let i = 0; i < batchSize; i += 1) {
    batch.push(items[(start + i) % items.length]);
  }
  saveLatestCursor((start + count) % items.length);
  return batch;
}

function cacheNextLatestBatch(items: VideoItem[], count: number): VideoItem[] {
  const batch = nextLatestBatch(items, count);
  cachedLatestBatch = batch;
  return batch;
}

export default function HomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSearchQuery = searchParams.get("q")?.trim() ?? "";
  const activeTag = searchParams.get("tag")?.trim() ?? "";
  const [rankingVideos, setRankingVideos] = useState<VideoItem[]>(cachedRanking ?? []);
  const [latestVideos, setLatestVideos] = useState<VideoItem[]>(cachedLatestBatch ?? []);
  const [rankingLoading, setRankingLoading] = useState(cachedRanking === null);
  const [rankingError, setRankingError] = useState(false);
  const [latestLoading, setLatestLoading] = useState(cachedLatestBatch === null);
  const [latestError, setLatestError] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [searchPage, setSearchPage] = useState(1);
  const [searchItems, setSearchItems] = useState<VideoItem[]>([]);
  const [searchTotal, setSearchTotal] = useState(0);
  const [searchLoading, setSearchLoading] = useState(false);
  const [searchError, setSearchError] = useState(false);
  const [searchSort, setSearchSort] = useState<SortKey>("hot");
  const [searchView, setSearchView] = useState<ViewMode>("grid");
  const homeRequestVersion = useRef(1);
  const isMobile = useIsMobile();
  const displayCount = isMobile ? MOBILE_COUNT : DESKTOP_COUNT;
  const searchPageSize = isMobile
    ? MOBILE_VIDEO_PAGE_SIZE
    : HOME_SEARCH_DESKTOP_PAGE_SIZE;
  const displayCountRef = useRef(displayCount);
  displayCountRef.current = displayCount;

  const resetSearchResults = useCallback(() => {
    setSearchPage(1);
    setSearchSort("hot");
  }, []);

  const refreshHome = useCallback(async () => {
    const requestVersion = ++homeRequestVersion.current;
    setRefreshing(true);
    setRankingLoading(true);
    setRankingError(false);
    setLatestLoading(true);
    setLatestError(false);

    const [rankingResult, latestResult] = await Promise.allSettled([
      fetchHomeVideos(displayCountRef.current),
      fetchListing(1, LATEST_POOL_SIZE, { sort: "latest", includeTotal: false }),
    ]);
    if (requestVersion !== homeRequestVersion.current) return;

    if (rankingResult.status === "fulfilled") {
      cachedRanking = rankingResult.value;
      setRankingVideos(rankingResult.value);
    } else {
      setRankingError(true);
    }
    if (latestResult.status === "fulfilled") {
      cachedLatestPool = latestResult.value.items;
      const latestBatch = cacheNextLatestBatch(
        latestResult.value.items,
        displayCountRef.current
      );
      setLatestVideos(latestBatch);
    } else {
      setLatestError(true);
    }
    setRankingLoading(false);
    setLatestLoading(false);
    setRefreshing(false);
  }, []);

  const handleSearch = useCallback((keyword: string) => {
    const q = keyword.trim();
    resetSearchResults();
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (q) {
          next.set("q", q);
          next.delete("tag");
        } else {
          next.delete("q");
        }
        return next;
      },
      { replace: true }
    );
  }, [resetSearchResults, setSearchParams]);

  useEffect(() => {
    document.title = activeSearchQuery
      ? `搜索 "${activeSearchQuery}"`
      : activeTag
      ? `标签 ${activeTag}`
      : "首页";
  }, [activeSearchQuery, activeTag]);

  useEffect(() => {
    let active = true;
    const requestVersion = homeRequestVersion.current;

    if (cachedRanking === null) {
      setRankingLoading(true);
      setRankingError(false);
      fetchHomeVideos(displayCountRef.current)
        .then((rankingItems) => {
          if (!active || requestVersion !== homeRequestVersion.current) return;
          cachedRanking = rankingItems;
          setRankingVideos(rankingItems);
          setRankingError(false);
        })
        .catch(() => {
          if (!active || requestVersion !== homeRequestVersion.current) return;
          setRankingError(true);
        })
        .finally(() => {
          if (active && requestVersion === homeRequestVersion.current) {
            setRankingLoading(false);
          }
        });
    }

    if (cachedLatestPool === null) {
      setLatestLoading(true);
      setLatestError(false);
      fetchListing(1, LATEST_POOL_SIZE, { sort: "latest", includeTotal: false })
        .then((latestResult) => {
          if (!active || requestVersion !== homeRequestVersion.current) return;
          cachedLatestPool = latestResult.items;
          setLatestVideos(
            cacheNextLatestBatch(
              latestResult.items,
              displayCountRef.current
            )
          );
          setLatestError(false);
        })
        .catch(() => {
          if (!active || requestVersion !== homeRequestVersion.current) return;
          setLatestError(true);
        })
        .finally(() => {
          if (active && requestVersion === homeRequestVersion.current) {
            setLatestLoading(false);
          }
        });
    } else {
      setLatestVideos(
        cachedLatestBatch ??
          cacheNextLatestBatch(cachedLatestPool, displayCountRef.current)
      );
      setLatestLoading(false);
    }

    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!activeSearchQuery && !activeTag) {
      setSearchItems([]);
      setSearchTotal(0);
      setSearchLoading(false);
      setSearchError(false);
      return;
    }

    let active = true;
    setSearchLoading(true);
    setSearchError(false);
    fetchListing(searchPage, searchPageSize, {
      q: activeSearchQuery,
      tag: activeTag,
      sort: searchSort,
    })
      .then((result) => {
        if (!active) return;
        setSearchItems(result.items ?? []);
        setSearchTotal(result.total ?? 0);
      })
      .catch(() => {
        if (!active) return;
        setSearchItems([]);
        setSearchTotal(0);
        setSearchError(true);
      })
      .finally(() => {
        if (active) setSearchLoading(false);
      });
    return () => {
      active = false;
    };
  }, [activeSearchQuery, activeTag, searchPage, searchPageSize, searchSort]);

  useEffect(() => {
    setSearchPage(1);
  }, [searchPageSize]);

  useEffect(() => {
    setSearchPage(1);
    setSearchSort("hot");
  }, [activeSearchQuery, activeTag]);

  const ranking = rankingVideos.slice(0, displayCount);
  const latest = latestVideos.slice(0, displayCount);
  const homeLoading = rankingLoading || latestLoading;
  const hasActiveSearch = activeSearchQuery.length > 0;
  const hasActiveTag = activeTag.length > 0;
  const hasActiveFilter = hasActiveSearch || hasActiveTag;
  const searchTotalPages = Math.max(1, Math.ceil(searchTotal / searchPageSize));
  const hasAnyVideos = ranking.length > 0 || latest.length > 0;
  const hasHomeError = rankingError || latestError;
  const showEmptyHome = !homeLoading && !hasHomeError && !hasAnyVideos;

  return (
    <AppShell mobileAutoHideNav>
      <div className="container page-section home-discovery-section">
        <PromoStrip />
        <SearchPanel
          value={activeSearchQuery}
          onSearch={handleSearch}
          variant="uiverse"
          placeholder=""
          className="search-panel--public search-panel--transparent"
        />
        {!hasActiveSearch && (
          hasAnyVideos || hasActiveTag ? (
            <TagCloud linkBasePath="/" onTagSelect={resetSearchResults} />
          ) : (
            <div className="tag-cloud-container is-reserved" aria-hidden="true" />
          )
        )}
      </div>

      {hasActiveFilter ? (
        <div className="container page-section home-primary-section">
          <SortToolbar
            sort={searchSort}
            view={searchView}
            onSortChange={(nextSort) => {
              setSearchSort(nextSort);
              setSearchPage(1);
              window.scrollTo({ top: 0, behavior: "smooth" });
            }}
            onViewChange={setSearchView}
          />
          {searchLoading ? (
            <VideoGrid videos={searchItems} loading compact={searchView === "compact"} skeletonCount={12} />
          ) : searchError ? (
            <AdminEmptyVisual
              variant="no-results"
              text="视频列表加载失败，请刷新重试"
              className="admin-empty-state admin-empty-state--plain home-empty-state"
            />
          ) : searchItems.length === 0 ? (
            <AdminEmptyVisual
              variant="no-results"
              text="未查询到"
              className="admin-empty-state admin-empty-state--plain home-empty-state"
            />
          ) : (
            <VideoGrid videos={searchItems} compact={searchView === "compact"} skeletonCount={12} />
          )}
          {!searchLoading && searchTotalPages > 1 && (
            <Pagination
              page={searchPage}
              pageSize={searchPageSize}
              total={searchTotal}
              onChange={(p) => {
                setSearchPage(p);
                window.scrollTo({ top: 0, behavior: "smooth" });
              }}
            />
          )}
        </div>
      ) : showEmptyHome ? (
        <div className="container page-section home-primary-section">
          <AdminEmptyVisual
            variant="empty"
            text="当前库中没有视频"
            className="admin-empty-state admin-empty-state--plain home-empty-state"
          />
        </div>
      ) : (
        <>
          <div className="container page-section home-primary-section">
            <SectionHeader title="随机推荐" />
            <VideoGrid
              videos={ranking}
              loading={rankingLoading}
              emptyText={rankingError ? "随机推荐加载失败，请刷新重试" : undefined}
              priorityCount={Math.min(4, displayCount)}
              skeletonCount={displayCount}
            />
          </div>

          <div className="container page-section">
            <SectionHeader title="最新视频" />
            <VideoGrid
              videos={latest}
              loading={latestLoading}
              emptyText={latestError ? "最新视频加载失败，请刷新重试" : undefined}
              skeletonCount={displayCount}
            />
          </div>
        </>
      )}

      {!hasActiveFilter && (
        <button
          type="button"
          className={`home-refresh ${refreshing ? "is-refreshing" : ""}`}
          onClick={refreshHome}
          disabled={refreshing}
          aria-label="刷新首页"
          title="刷新首页"
        >
          <RefreshCw size={18} />
        </button>
      )}
    </AppShell>
  );
}
