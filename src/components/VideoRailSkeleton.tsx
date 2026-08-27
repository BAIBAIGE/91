import { VideoRailMobileHeading } from "./VideoRailMobileHeading";

export function VideoRailSkeleton() {
  return (
    <aside className="vd-rail" aria-label="视频列表加载中" aria-busy="true">
      <div
        className="content-tabs vd-rail__tabs vd-rail__tabs--loading"
        aria-hidden="true"
      >
        <span className="content-tabs__tab vd-rail__tab" aria-selected="true">
          推荐视频
        </span>
        <span className="content-tabs__tab vd-rail__tab">相关合集</span>
      </div>
      <VideoRailMobileHeading />
      <VideoRailRowsSkeleton label="正在加载视频列表" />
    </aside>
  );
}

export function VideoRailRowsSkeleton({
  label = "正在加载相关合集",
}: {
  label?: string;
}) {
  return (
    <div
      className="vd-rail__collection-loading"
      role="status"
      aria-label={label}
      aria-busy="true"
    >
      {Array.from({ length: 6 }).map((_, index) => (
        <div key={index} className="vd-rail__loading-row" aria-hidden="true">
          <span className="vd-rail__loading-thumb" />
          <span className="vd-rail__loading-body">
            <span />
            <span />
          </span>
        </div>
      ))}
    </div>
  );
}
