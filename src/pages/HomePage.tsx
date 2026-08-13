import { useCallback, useEffect, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useSearchParams } from "react-router";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { AppShell } from "@/components/AppShell";
import { ListingLoadError } from "@/components/ListingLoadError";
import { Pagination } from "@/components/Pagination";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { SectionHeader } from "@/components/SectionHeader";
import { SortToolbar } from "@/components/SortToolbar";
import { TagCloud } from "@/components/TagCloud";
import { VideoGrid } from "@/components/VideoGrid";
import { fetchHomeVideos, fetchLatestHomeVideos } from "@/data/videos";
import {
  readListingPage,
  readListingSort,
  readListingView,
  withListingNavigation,
  withListingPage,
  withListingView,
} from "@/lib/listingSearchParams";
import { MOBILE_VIDEO_PAGE_SIZE, useIsMobile } from "@/lib/responsive";
import { useListingQuery } from "@/lib/useListingQuery";
import type { VideoItem } from "@/types";

const DESKTOP_COUNT = 12;
const MOBILE_COUNT = 8;
const HOME_SEARCH_DESKTOP_PAGE_SIZE = 20;

// 首页推荐接口每次请求都会推进会话轮换游标。模块级快照因此在 SPA 会话内
// 保持稳定，只有用户主动刷新、浏览器整页刷新或响应式布局需要补足卡片时才更新。
let cachedRanking: VideoItem[] | null = null;
let cachedLatest: VideoItem[] | null = null;

export default function HomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeSearchQuery = searchParams.get("q")?.trim() ?? "";
  const activeTag = searchParams.get("tag")?.trim() ?? "";
  const hasActiveSearch = activeSearchQuery.length > 0;
  const hasActiveTag = activeTag.length > 0;
  const hasActiveFilter = hasActiveSearch || hasActiveTag;
  const searchPage = readListingPage(searchParams);
  const searchSort = readListingSort(searchParams);
  const searchView = readListingView(searchParams);
  const isMobile = useIsMobile();
  const displayCount = isMobile ? MOBILE_COUNT : DESKTOP_COUNT;
  const searchPageSize = isMobile
    ? MOBILE_VIDEO_PAGE_SIZE
    : HOME_SEARCH_DESKTOP_PAGE_SIZE;
  const searchResult = useListingQuery(
    {
      q: activeSearchQuery,
      tag: activeTag,
      sort: searchSort,
      page: searchPage,
      pageSize: searchPageSize,
    },
    { enabled: hasActiveFilter }
  );
  const searchSnapshot = searchResult.snapshot;
  const searchItems = searchSnapshot?.items ?? [];
  const searchHasContent = searchItems.length > 0;
  const searchShowSkeleton =
    searchResult.initialLoading ||
    (searchResult.transitioning && !searchHasContent);
  const searchShowContentError =
    searchResult.phase === "error" && searchHasContent;
  const searchShowEmptyError =
    searchResult.phase === "error" && !searchHasContent;
  const eagerCount = isMobile ? 2 : 4;

  const [rankingVideos, setRankingVideos] = useState<VideoItem[]>(cachedRanking ?? []);
  const [latestVideos, setLatestVideos] = useState<VideoItem[]>(cachedLatest ?? []);
  const [rankingLoading, setRankingLoading] = useState(cachedRanking === null);
  const [rankingError, setRankingError] = useState(false);
  const [rankingRevalidating, setRankingRevalidating] = useState(false);
  const [latestLoading, setLatestLoading] = useState(cachedLatest === null);
  const [latestError, setLatestError] = useState(false);
  const [latestRevalidating, setLatestRevalidating] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const homeRequestVersion = useRef(0);
  const rankingRequestVersion = useRef(0);
  const latestRequestVersion = useRef(0);
  const displayCountRef = useRef(displayCount);
  const previousDisplayCountRef = useRef(displayCount);
  const previousSearchPageSizeRef = useRef(searchPageSize);
  const searchScrollOnCommitRef = useRef(false);
  displayCountRef.current = displayCount;

  const loadRanking = useCallback(async (background: boolean) => {
    const requestVersion = ++rankingRequestVersion.current;
    const hasCachedContent = cachedRanking !== null;
    setRankingLoading(!hasCachedContent);
    setRankingRevalidating(background && hasCachedContent);
    setRankingError(false);
    try {
      const rankingItems = await fetchHomeVideos(DESKTOP_COUNT);
      if (requestVersion !== rankingRequestVersion.current) return;
      cachedRanking = rankingItems;
      setRankingVideos(rankingItems);
      setRankingError(false);
    } catch {
      if (requestVersion !== rankingRequestVersion.current) return;
      setRankingError(true);
    } finally {
      if (requestVersion === rankingRequestVersion.current) {
        setRankingLoading(false);
        setRankingRevalidating(false);
      }
    }
  }, []);

  const loadLatest = useCallback(async (background: boolean) => {
    const requestVersion = ++latestRequestVersion.current;
    const hasCachedContent = cachedLatest !== null;
    setLatestLoading(!hasCachedContent);
    setLatestRevalidating(background && hasCachedContent);
    setLatestError(false);
    try {
      const latestItems = await fetchLatestHomeVideos(displayCountRef.current);
      if (requestVersion !== latestRequestVersion.current) return;
      cachedLatest = latestItems;
      setLatestVideos(latestItems);
      setLatestError(false);
    } catch {
      if (requestVersion !== latestRequestVersion.current) return;
      setLatestError(true);
    } finally {
      if (requestVersion === latestRequestVersion.current) {
        setLatestLoading(false);
        setLatestRevalidating(false);
      }
    }
  }, []);

  const refreshRanking = useCallback(() => loadRanking(true), [loadRanking]);
  const refreshLatest = useCallback(() => loadLatest(true), [loadLatest]);

  const refreshHome = useCallback(async () => {
    const requestVersion = ++homeRequestVersion.current;
    setRefreshing(true);
    await Promise.allSettled([loadRanking(false), loadLatest(false)]);
    if (requestVersion !== homeRequestVersion.current) return;
    setRefreshing(false);
  }, [loadLatest, loadRanking]);

  useEffect(() => {
    document.title = activeSearchQuery
      ? `搜索 "${activeSearchQuery}"`
      : activeTag
      ? `标签 ${activeTag}`
      : "首页";
  }, [activeSearchQuery, activeTag]);

  useEffect(() => {
    if (cachedRanking === null) {
      void loadRanking(false);
    }

    if (cachedLatest === null) {
      void loadLatest(false);
    } else if (cachedLatest.length < displayCountRef.current) {
      void loadLatest(true);
    } else {
      setLatestVideos(cachedLatest);
      setLatestLoading(false);
    }

    return () => {
      homeRequestVersion.current += 1;
      rankingRequestVersion.current += 1;
      latestRequestVersion.current += 1;
    };
  }, [loadLatest, loadRanking]);

  useEffect(() => {
    if (hasActiveFilter) return;
    const previousCount = previousDisplayCountRef.current;
    if (displayCount <= previousCount) {
      previousDisplayCountRef.current = displayCount;
      return;
    }
    if (latestVideos.length >= displayCount) {
      previousDisplayCountRef.current = displayCount;
      return;
    }
    if (refreshing || latestLoading || latestRevalidating) return;

    previousDisplayCountRef.current = displayCount;
    void refreshLatest();
  }, [
    displayCount,
    hasActiveFilter,
    latestLoading,
    latestRevalidating,
    latestVideos.length,
    refreshLatest,
    refreshing,
  ]);

  useEffect(() => {
    if (previousSearchPageSizeRef.current === searchPageSize) return;
    previousSearchPageSizeRef.current = searchPageSize;
    if (!hasActiveFilter || searchPage === 1) return;
    setSearchParams((current) => withListingPage(current, 1), { replace: true });
  }, [hasActiveFilter, searchPage, searchPageSize, setSearchParams]);

  useEffect(() => {
    if (
      !searchScrollOnCommitRef.current ||
      searchSnapshot?.key !== searchResult.key
    ) {
      return;
    }
    searchScrollOnCommitRef.current = false;
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, [searchResult.key, searchSnapshot?.key]);

  const ranking = rankingVideos.slice(0, displayCount);
  const latest = latestVideos.slice(0, displayCount);
  const homeLoading = rankingLoading || latestLoading;
  const hasAnyVideos = ranking.length > 0 || latest.length > 0;
  const hasHomeError = rankingError || latestError;
  const showEmptyHome = !homeLoading && !hasHomeError && !hasAnyVideos;
  const displayedSearchSort =
    searchResult.phase === "error" && searchSnapshot
      ? searchSnapshot.query.sort
      : searchSort;
  const displayedSearchPage = searchSnapshot?.query.page ?? searchPage;

  return (
    <AppShell mobileAutoHideNav>
      <div className="container page-section home-discovery-section">
        <PromoStrip />
        <SearchPanel
          navigationPath="/"
          variant="uiverse"
          placeholder=""
          className="search-panel--public search-panel--transparent"
        />
        {hasAnyVideos || hasActiveFilter ? (
          <TagCloud linkBasePath="/" />
        ) : (
          <div className="tag-cloud-container is-reserved" aria-hidden="true" />
        )}
      </div>

      {hasActiveFilter ? (
        <div className="container page-section home-primary-section">
          <SortToolbar
            sort={displayedSearchSort}
            view={searchView}
            sortDisabled={searchResult.initialLoading || searchResult.transitioning}
            onSortChange={(nextSort) => {
              searchScrollOnCommitRef.current = true;
              setSearchParams(
                withListingNavigation(searchParams, {
                  sort: nextSort,
                  page: 1,
                }),
                { replace: true }
              );
            }}
            onViewChange={(nextView) => {
              setSearchParams(withListingView(searchParams, nextView), {
                replace: true,
              });
            }}
          />

          {searchShowSkeleton ? (
            <VideoGrid
              videos={[]}
              loading
              compact={searchView === "compact"}
              skeletonCount={searchPageSize}
            />
          ) : searchShowEmptyError ? (
            <ListingLoadError
              hasContent={false}
              onRetry={searchResult.retry}
              emptyClassName="admin-empty-state admin-empty-state--plain home-empty-state"
            />
          ) : searchSnapshot && searchItems.length === 0 ? (
            <AdminEmptyVisual
              variant="no-results"
              text="未查询到"
              className="admin-empty-state admin-empty-state--plain home-empty-state"
            />
          ) : (
            <>
              {searchShowContentError && (
                <ListingLoadError
                  hasContent
                  displayedPage={displayedSearchPage}
                  onRetry={searchResult.retry}
                />
              )}
              <VideoGrid
                videos={searchItems}
                compact={searchView === "compact"}
                refreshMode={
                  searchResult.transitioning
                    ? "blocking"
                    : searchResult.revalidating
                    ? "background"
                    : undefined
                }
                eagerCount={eagerCount}
                highPriorityCount={1}
              />
            </>
          )}

          {searchSnapshot && (
            <Pagination
              page={displayedSearchPage}
              pageSize={searchSnapshot.query.pageSize}
              total={searchSnapshot.total}
              disabled={searchResult.transitioning}
              pendingPage={searchResult.transitioning ? searchPage : undefined}
              onChange={(nextPage) => {
                searchScrollOnCommitRef.current = true;
                setSearchParams(withListingPage(searchParams, nextPage));
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
            {rankingError && ranking.length > 0 && (
              <ListingLoadError
                hasContent
                onRetry={() => void refreshRanking()}
              />
            )}
            <VideoGrid
              videos={ranking}
              loading={rankingLoading}
              refreshMode={
                refreshing && !rankingLoading
                  ? "blocking"
                  : rankingRevalidating
                  ? "background"
                  : undefined
              }
              emptyText={rankingError ? "随机推荐加载失败，请刷新重试" : undefined}
              eagerCount={eagerCount}
              highPriorityCount={1}
              skeletonCount={displayCount}
            />
          </div>

          <div className="container page-section">
            <SectionHeader title="最新视频" />
            {latestError && latest.length > 0 && (
              <ListingLoadError
                hasContent
                onRetry={() => void refreshLatest()}
              />
            )}
            <VideoGrid
              videos={latest}
              loading={latestLoading}
              refreshMode={
                refreshing && !latestLoading
                  ? "blocking"
                  : latestRevalidating
                  ? "background"
                  : undefined
              }
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
