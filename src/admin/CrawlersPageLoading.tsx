import { Plus } from "lucide-react";
import { useAdminFloatingActionSpace } from "./useAdminFloatingActionSpace";

const CRAWLER_LIST_SKELETON_COUNT = 3;

export function CrawlerListControlsPlaceholder() {
  return (
    <div
      className="admin-crawler-list__controls admin-crawler-list__controls--placeholder"
      aria-hidden="true"
    >
      <div className="admin-crawler-global-teaser">
        <span>预览视频</span>
        <button className="toggle-switch" type="button" disabled tabIndex={-1}>
          <span className="toggle-switch__dot" />
        </button>
      </div>
    </div>
  );
}

export function CrawlerListSkeleton() {
  return (
    <div
      className="admin-crawler-table admin-crawler-table--skeleton"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <span className="sr-only">正在加载爬虫列表</span>
      {Array.from({ length: CRAWLER_LIST_SKELETON_COUNT }, (_, index) => (
        <div
          key={index}
          className="admin-crawler-card-skeleton admin-card-skeleton-surface"
          aria-hidden="true"
        />
      ))}
    </div>
  );
}

export function CrawlersPageLoading() {
  const floatingActionPageRef = useAdminFloatingActionSpace<HTMLElement>();

  return (
    <section
      ref={floatingActionPageRef}
      className="admin-page admin-page--with-floating-actions admin-crawlers-page"
    >
      <div className="admin-crawler-console">
        <div className="admin-card admin-crawler-list" aria-busy="true">
          <CrawlerListControlsPlaceholder />
          <CrawlerListSkeleton />
        </div>
      </div>

      <button
        data-admin-floating-actions
        type="button"
        className="admin-btn admin-create-fab"
        disabled
      >
        <Plus size="1em" aria-hidden="true" />
        添加爬虫
      </button>
    </section>
  );
}
