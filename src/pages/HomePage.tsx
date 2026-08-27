import { useCallback, useEffect, useMemo, useRef } from "react";
import { RefreshCw } from "lucide-react";
import { useLocation, useSearchParams } from "react-router";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { AppShell } from "@/components/AppShell";
import { HomeFeedTabs } from "@/components/HomeFeedTabs";
import { InfiniteFeedStatus } from "@/components/InfiniteFeedStatus";
import { ListingLoadError } from "@/components/ListingLoadError";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { SortToolbar, type ViewMode } from "@/components/SortToolbar";
import { TagCloud } from "@/components/TagCloud";
import { VideoGrid } from "@/components/VideoGrid";
import { VirtualVideoGrid } from "@/components/VirtualVideoGrid";
import {
  homeLatestFeedSource,
  homeRecommendationFeedSource,
  listingFeedSource,
} from "@/lib/infiniteFeedSource";
import {
  readHomeFeed,
  readListingSort,
  readListingView,
  withHomeFeed,
  withListingNavigation,
  withListingPage,
  withListingView,
  type HomeFeedKey,
} from "@/lib/listingSearchParams";
import { MOBILE_VIDEO_PAGE_SIZE, useIsMobile } from "@/lib/responsive";
import { useInfiniteListing } from "@/lib/useInfiniteListing";
import {
  useListingRestoreTarget,
  useListingScrollRestore,
} from "@/lib/useListingScrollRestore";
import type { SortKey } from "@/types";

const HOME_DESKTOP_BATCH_SIZE = 20;

// 距列表尾部还有两行时就续下一批，滚动到底之前数据已经在路上。
const PREFETCH_ROWS = 2;

export default function HomePage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const location = useLocation();
  const activeSearchQuery = searchParams.get("q")?.trim() ?? "";
  const activeTag = searchParams.get("tag")?.trim() ?? "";
  const hasActiveSearch = activeSearchQuery.length > 0;
  const hasActiveTag = activeTag.length > 0;
  const hasActiveFilter = hasActiveSearch || hasActiveTag;
  const searchSort = readListingSort(searchParams);
  const searchView = readListingView(searchParams);
  const feed = readHomeFeed(searchParams);
  const isMobile = useIsMobile();
  const eagerCount = isMobile ? 2 : 4;

  // 推荐、最新以及任意搜索/标签组合都通过不可变快照无限滚动。source key
  // 完整描述结果集身份，hook 统一负责累积、缓存、取消和返回位置恢复。
  const batchSize = isMobile
    ? MOBILE_VIDEO_PAGE_SIZE
    : HOME_DESKTOP_BATCH_SIZE;
  const feedSource = useMemo(
    () =>
      feed === "latest"
        ? homeLatestFeedSource(batchSize)
        : homeRecommendationFeedSource(),
    [batchSize, feed]
  );
  const filterFeedSource = useMemo(
    () =>
      listingFeedSource({
        q: activeSearchQuery,
        tag: activeTag,
        sort: searchSort,
        pageSize: batchSize,
      }),
    [activeSearchQuery, activeTag, batchSize, searchSort]
  );
  const activeFeedSource = hasActiveFilter ? filterFeedSource : feedSource;
  const isRandomRecommendationFeed =
    !hasActiveFilter && feed === "recommend";
  const restoreTarget = useListingRestoreTarget({
    historyKey: location.key,
    queryKey: activeFeedSource.key,
    pageSize: activeFeedSource.batchSize,
    // 随机快照只在当前 Document 内恢复：进详情再后退仍保持现场，浏览器
    // 刷新生成新 Document 后则从空 token 开始重新随机。
    feedSnapshotScope: isRandomRecommendationFeed ? "document" : "session",
  });
  const homeFeed = useInfiniteListing(activeFeedSource, {
    restoreCount: restoreTarget.count,
    restoreFeedToken: restoreTarget.feedToken,
  });
  useListingScrollRestore({
    target: restoreTarget,
    queryKey: activeFeedSource.key,
    requestedCount: homeFeed.requestedCount,
    feedToken: homeFeed.feedToken,
    itemCount: homeFeed.items.length,
  });

  const feedItems = homeFeed.items;
  const feedHasContent = feedItems.length > 0;
  const previousFeedKeyRef = useRef(activeFeedSource.key);

  useEffect(() => {
    document.title = activeSearchQuery
      ? `搜索 "${activeSearchQuery}"`
      : activeTag
      ? `标签 ${activeTag}`
      : "首页";
  }, [activeSearchQuery, activeTag]);

  useEffect(() => {
    // 无限滚动没有页码；清理旧书签或外部链接遗留的 page 参数。
    if (!searchParams.has("page")) return;
    setSearchParams((current) => withListingPage(current, 1), { replace: true });
  }, [searchParams, setSearchParams]);

  // 换 tab、排序、搜索或标签都会生成一个新结果集，直接回到顶部再累积。
  // 平滑滚动会被虚拟列表的行高补偿打断而停在半路。
  useEffect(() => {
    if (previousFeedKeyRef.current === activeFeedSource.key) return;
    previousFeedKeyRef.current = activeFeedSource.key;
    window.scrollTo({ top: 0, behavior: "auto" });
  }, [activeFeedSource.key]);

  const reloadFeed = homeFeed.reload;
  const refreshHome = useCallback(() => {
    window.scrollTo({ top: 0, behavior: "auto" });
    reloadFeed();
  }, [reloadFeed]);

  const showRefresh = isRandomRecommendationFeed;
  const refreshing = showRefresh && homeFeed.initialLoading;

  const handleSearchSortChange = useCallback(
    (nextSort: SortKey) => {
      setSearchParams(
        (current) =>
          withListingNavigation(current, { sort: nextSort, page: 1 }),
        { replace: true }
      );
    },
    [setSearchParams]
  );

  const handleSearchViewChange = useCallback(
    (nextView: ViewMode) => {
      setSearchParams((current) => withListingView(current, nextView), {
        replace: true,
      });
    },
    [setSearchParams]
  );

  const handleFeedChange = useCallback(
    (nextFeed: HomeFeedKey) => {
      setSearchParams((current) => withHomeFeed(current, nextFeed), {
        replace: true,
      });
    },
    [setSearchParams]
  );

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
        {feedHasContent || hasActiveFilter ? (
          <TagCloud linkBasePath="/" />
        ) : (
          <div className="tag-cloud-container is-reserved" aria-hidden="true" />
        )}
      </div>

      <div className="container page-section home-primary-section">
        {hasActiveFilter ? (
          <SortToolbar
            sort={searchSort}
            view={searchView}
            sortDisabled={homeFeed.initialLoading}
            onSortChange={handleSearchSortChange}
            onViewChange={handleSearchViewChange}
          />
        ) : (
          <HomeFeedTabs
            feed={feed}
            onChange={handleFeedChange}
          />
        )}

        {homeFeed.initialLoading && !feedHasContent ? (
          <VideoGrid
            videos={[]}
            loading
            compact={hasActiveFilter && searchView === "compact"}
            skeletonCount={activeFeedSource.batchSize}
          />
        ) : homeFeed.failed && !feedHasContent ? (
          <ListingLoadError
            hasContent={false}
            onRetry={homeFeed.retry}
            emptyClassName="admin-empty-state admin-empty-state--plain home-empty-state"
          />
        ) : !feedHasContent ? (
          <AdminEmptyVisual
            variant={hasActiveFilter ? "no-results" : "empty"}
            text={hasActiveFilter ? "未查询到" : "当前库中没有视频"}
            className="admin-empty-state admin-empty-state--plain home-empty-state"
          />
        ) : (
          <>
            <VirtualVideoGrid
              videos={feedItems}
              compact={hasActiveFilter && searchView === "compact"}
              eagerCount={eagerCount}
              highPriorityCount={1}
              key={`${activeFeedSource.key}:${homeFeed.feedToken}`}
              hasMore={homeFeed.hasMore}
              loadingMore={homeFeed.loadingMore}
              prefetchRows={PREFETCH_ROWS}
              tailContent={
                homeFeed.loadingMore ? (
                  <InfiniteFeedStatus state="loading" />
                ) : undefined
              }
              onLoadMore={homeFeed.loadMore}
            />

            {homeFeed.failed ? (
              <ListingLoadError hasContent onRetry={homeFeed.retry} />
            ) : homeFeed.exhausted ? (
              <InfiniteFeedStatus state="end" />
            ) : null}
          </>
        )}
      </div>

      {showRefresh && (
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
