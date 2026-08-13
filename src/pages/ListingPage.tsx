import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { AppShell } from "@/components/AppShell";
import { ListingLoadError } from "@/components/ListingLoadError";
import { Pagination } from "@/components/Pagination";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { SortToolbar } from "@/components/SortToolbar";
import { TagCloud } from "@/components/TagCloud";
import { VideoGrid } from "@/components/VideoGrid";
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

const DESKTOP_PAGE_SIZE = 20;

export default function ListingPage() {
  const [params, setParams] = useSearchParams();
  const keyword = params.get("q") ?? "";
  const tag = params.get("tag") ?? "";
  const sort = readListingSort(params);
  const page = readListingPage(params);
  const view = readListingView(params);
  const isMobile = useIsMobile();
  const pageSize = isMobile ? MOBILE_VIDEO_PAGE_SIZE : DESKTOP_PAGE_SIZE;
  const result = useListingQuery({
    q: keyword,
    tag,
    sort,
    page,
    pageSize,
  });
  const snapshot = result.snapshot;
  const items = snapshot?.items ?? [];
  const hasContent = items.length > 0;
  const showSkeleton =
    result.initialLoading || (result.transitioning && !hasContent);
  const showContentError = result.phase === "error" && hasContent;
  const showEmptyError = result.phase === "error" && !hasContent;
  const hasActiveFilter = keyword.trim().length > 0 || tag.trim().length > 0;
  const eagerCount = isMobile ? 2 : 4;
  const scrollOnCommitRef = useRef(false);
  const previousPageSizeRef = useRef(pageSize);

  useEffect(() => {
    document.title = keyword
      ? `搜索 "${keyword}"`
      : tag
      ? `标签 ${tag}`
      : "视频列表";
  }, [keyword, tag]);

  useEffect(() => {
    if (previousPageSizeRef.current === pageSize) return;
    previousPageSizeRef.current = pageSize;
    if (page === 1) return;
    setParams((current) => withListingPage(current, 1), { replace: true });
  }, [page, pageSize, setParams]);

  useEffect(() => {
    if (
      !scrollOnCommitRef.current ||
      snapshot?.key !== result.key
    ) {
      return;
    }
    scrollOnCommitRef.current = false;
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, [result.key, snapshot?.key]);

  const displayedSort =
    result.phase === "error" && snapshot ? snapshot.query.sort : sort;
  const displayedPage = snapshot?.query.page ?? page;

  return (
    <AppShell>
      <div className="container page-section listing-discovery-section">
        <PromoStrip />
        <SearchPanel
          variant="uiverse"
          placeholder=""
          className="search-panel--public search-panel--transparent"
        />
        <TagCloud />
      </div>

      <div className="container page-section listing-primary-section">
        <SortToolbar
          sort={displayedSort}
          view={view}
          sortDisabled={result.initialLoading || result.transitioning}
          onSortChange={(nextSort) => {
            scrollOnCommitRef.current = true;
            setParams(
              withListingNavigation(params, { sort: nextSort, page: 1 }),
              { replace: true }
            );
          }}
          onViewChange={(nextView) => {
            setParams(withListingView(params, nextView), { replace: true });
          }}
        />

        {showSkeleton ? (
          <VideoGrid
            videos={[]}
            loading
            compact={view === "compact"}
            skeletonCount={pageSize}
          />
        ) : showEmptyError ? (
          <ListingLoadError
            hasContent={false}
            onRetry={result.retry}
            emptyClassName="admin-empty-state admin-empty-state--plain listing-empty-state"
          />
        ) : snapshot && items.length === 0 ? (
          <AdminEmptyVisual
            variant={hasActiveFilter ? "no-results" : "empty"}
            text={hasActiveFilter ? "未查询到" : "当前库中没有视频"}
            className="admin-empty-state admin-empty-state--plain listing-empty-state"
          />
        ) : (
          <>
            {showContentError && (
              <ListingLoadError
                hasContent
                displayedPage={displayedPage}
                onRetry={result.retry}
              />
            )}
            <VideoGrid
              videos={items}
              compact={view === "compact"}
              refreshMode={
                result.transitioning
                  ? "blocking"
                  : result.revalidating
                  ? "background"
                  : undefined
              }
              eagerCount={eagerCount}
              highPriorityCount={1}
            />
          </>
        )}

        {snapshot && (
          <Pagination
            page={displayedPage}
            pageSize={snapshot.query.pageSize}
            total={snapshot.total}
            disabled={result.transitioning}
            pendingPage={result.transitioning ? page : undefined}
            onChange={(nextPage) => {
              scrollOnCommitRef.current = true;
              setParams(withListingPage(params, nextPage));
            }}
          />
        )}
      </div>
    </AppShell>
  );
}
