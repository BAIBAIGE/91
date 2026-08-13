import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";

type Props = {
  hasContent: boolean;
  displayedPage?: number;
  onRetry: () => void;
  emptyClassName?: string;
};

export function ListingLoadError({
  hasContent,
  displayedPage,
  onRetry,
  emptyClassName = "",
}: Props) {
  if (hasContent) {
    return (
      <div className="listing-load-error" role="alert">
        <span>
          视频列表加载失败
          {displayedPage ? `，当前仍显示第 ${displayedPage} 页` : "，当前仍显示旧内容"}
        </span>
        <button type="button" onClick={onRetry}>
          重试
        </button>
      </div>
    );
  }

  return (
    <div
      className={`listing-load-error-empty${emptyClassName ? ` ${emptyClassName}` : ""}`}
      role="alert"
    >
      <AdminEmptyVisual variant="no-results" text="视频列表加载失败" />
      <button type="button" onClick={onRetry}>
        重新加载
      </button>
    </div>
  );
}
