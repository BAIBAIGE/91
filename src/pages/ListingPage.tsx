import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { AppShell } from "@/components/AppShell";
import { PromoStrip } from "@/components/PromoStrip";
import { SearchPanel } from "@/components/SearchPanel";
import { TagCloud } from "@/components/TagCloud";
import { SortToolbar, type ViewMode } from "@/components/SortToolbar";
import { VideoGrid } from "@/components/VideoGrid";
import { Pagination } from "@/components/Pagination";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { fetchListing } from "@/data/videos";
import {
  readListingSort,
  withListingSort,
} from "@/lib/listingSearchParams";
import { MOBILE_VIDEO_PAGE_SIZE, useIsMobile } from "@/lib/responsive";
import type { SortKey, VideoItem } from "@/types";

const DESKTOP_PAGE_SIZE = 20;

type ListingSnapshot = {
  key: string;
  page: number;
  view: ViewMode;
  items: VideoItem[];
  total: number;
};

// 只保留 SPA 生命周期内最后一次成功显示的列表。返回详情前的列表时直接
// 恢复；刷新浏览器后模块重载，仍会正常请求最新内容。
let cachedListingSnapshot: ListingSnapshot | null = null;

function listingSnapshotKey(
  keyword: string,
  tag: string,
  pageSize: number,
  sort: SortKey
): string {
  return JSON.stringify([keyword, tag, pageSize, sort]);
}

function listingRequestKey(snapshotKey: string, page: number): string {
  return `${snapshotKey}\n${page}`;
}

type ListingContentProps = {
  keyword: string;
  tag: string;
  pageSize: number;
  sort: SortKey;
  snapshotKey: string;
  onSortChange: (sort: SortKey) => void;
};

export default function ListingPage() {
  const [params, setParams] = useSearchParams();
  const keyword = params.get("q") ?? "";
  const tag = params.get("tag") ?? "";
  const sort = readListingSort(params);
  const isMobile = useIsMobile();
  const pageSize = isMobile ? MOBILE_VIDEO_PAGE_SIZE : DESKTOP_PAGE_SIZE;
  const snapshotKey = listingSnapshotKey(keyword, tag, pageSize, sort);

  return (
    <ListingContent
      key={`${keyword}\n${tag}\n${pageSize}`}
      keyword={keyword}
      tag={tag}
      pageSize={pageSize}
      sort={sort}
      snapshotKey={snapshotKey}
      onSortChange={(nextSort) => {
        setParams(withListingSort(params, nextSort), { replace: true });
      }}
    />
  );
}

function ListingContent({
  keyword,
  tag,
  pageSize,
  sort,
  snapshotKey,
  onSortChange,
}: ListingContentProps) {
  const initialSnapshotRef = useRef(
    cachedListingSnapshot?.key === snapshotKey
      ? cachedListingSnapshot
      : null
  );
  const initialSnapshot = initialSnapshotRef.current;
  const loadedRequestKeyRef = useRef<string | null>(
    initialSnapshot
      ? listingRequestKey(snapshotKey, initialSnapshot.page)
      : null
  );

  const [view, setView] = useState<ViewMode>(initialSnapshot?.view ?? "grid");
  const viewRef = useRef(view);
  viewRef.current = view;
  const [page, setPage] = useState(initialSnapshot?.page ?? 1);
  const [initialLoading, setInitialLoading] = useState(initialSnapshot === null);
  const [listingError, setListingError] = useState(false);
  const [items, setItems] = useState<VideoItem[]>(initialSnapshot?.items ?? []);
  const [total, setTotal] = useState(initialSnapshot?.total ?? 0);
  const hasActiveFilter = keyword.trim().length > 0 || tag.trim().length > 0;

  useEffect(() => {
    document.title = keyword
      ? `搜索 "${keyword}"`
      : tag
      ? `标签 ${tag}`
      : "视频列表";

    const requestKey = listingRequestKey(snapshotKey, page);
    if (loadedRequestKeyRef.current === requestKey) return;

    let active = true;
    setListingError(false);
    fetchListing(page, pageSize, { q: keyword, tag, sort })
      .then((r) => {
        if (!active) return;
        const nextItems = r.items ?? [];
        const nextTotal = r.total ?? 0;
        loadedRequestKeyRef.current = requestKey;
        cachedListingSnapshot = {
          key: snapshotKey,
          page,
          view: viewRef.current,
          items: nextItems,
          total: nextTotal,
        };
        setItems(nextItems);
        setTotal(nextTotal);
      })
      .catch(() => {
        if (active) setListingError(true);
      })
      .finally(() => {
        if (active) setInitialLoading(false);
      });
    return () => {
      active = false;
    };
  }, [keyword, tag, pageSize, sort, snapshotKey, page]);

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
          sort={sort}
          view={view}
          onSortChange={(nextSort) => {
            onSortChange(nextSort);
            setPage(1);
            window.scrollTo({ top: 0, behavior: "smooth" });
          }}
          onViewChange={(nextView) => {
            setView(nextView);
            if (
              cachedListingSnapshot?.key === snapshotKey &&
              cachedListingSnapshot.page === page
            ) {
              cachedListingSnapshot = {
                ...cachedListingSnapshot,
                view: nextView,
              };
            }
          }}
        />
        {initialLoading ? (
          <VideoGrid videos={items} loading compact={view === "compact"} skeletonCount={12} />
        ) : listingError && items.length === 0 ? (
          <AdminEmptyVisual
            variant="no-results"
            text="视频列表加载失败，请刷新重试"
            className="admin-empty-state admin-empty-state--plain listing-empty-state"
          />
        ) : items.length === 0 ? (
          <AdminEmptyVisual
            variant={hasActiveFilter ? "no-results" : "empty"}
            text={hasActiveFilter ? "未查询到" : "当前库中没有视频"}
            className="admin-empty-state admin-empty-state--plain listing-empty-state"
          />
        ) : (
          <VideoGrid videos={items} compact={view === "compact"} skeletonCount={12} />
        )}
        <Pagination
          page={page}
          pageSize={pageSize}
          total={total}
          onChange={(p) => {
            setPage(p);
            window.scrollTo({ top: 0, behavior: "smooth" });
          }}
        />
      </div>
    </AppShell>
  );
}
