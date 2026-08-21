import {
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { Link, useLocation } from "react-router";
import { Eye } from "lucide-react";
import type {
  PreviewState,
  VideoCollectionItem,
  VideoCollectionSummary,
  VideoItem,
} from "@/types";
import { formatCount } from "@/lib/format";
import { previewController } from "@/lib/previewController";
import {
  shouldInterceptPreviewTap,
  shouldStartInstantPreview,
} from "@/lib/previewIntent";
import { useInViewport } from "@/lib/useInViewport";
import { useLazyVideoCollection } from "@/lib/useLazyVideoCollection";
import { resolveVideoReturnPath, routeToPath } from "@/lib/videoReturnPath";
import { PreviewVideo } from "./PreviewVideo";
import { VideoThumbnail } from "./VideoThumbnail";

type Props = {
  videos: VideoItem[];
  videoId: string;
  collection?: VideoCollectionSummary;
};

const HOVER_DELAY_MS = 300;

function useActivePreviewId(): string | null {
  return useSyncExternalStore(
    previewController.subscribe,
    previewController.getActiveId,
    () => null
  );
}

type RailView = "recommended" | "collection";

/**
 * 详情页右侧 / 移动端下方的视频列表。桌面端有合集时可在推荐与同目录
 * 合集之间切换；移动端继续只显示推荐，合集由专用底部浮窗负责。
 *
 * 不直接复用 VideoCard：那个组件结构是上下两段（缩略图 + 标题/meta），而这里需要
 * 左右横排的紧凑布局，覆盖样式会很乱。推荐项继续复用同一套预览基础设施。
 */
export function RecommendedRail({ videos, videoId, collection }: Props) {
  const hasRecommendations = Array.isArray(videos) && videos.length > 0;
  const hasCollection = !!collection && collection.total > 1;
  const [activeView, setActiveView] = useState<RailView>(() =>
    hasRecommendations ? "recommended" : "collection"
  );
  const [desktop, setDesktop] = useState(() =>
    typeof window !== "undefined"
      ? window.matchMedia("(min-width: 769px)").matches
      : false
  );
  const { data, error, retry } = useLazyVideoCollection(
    videoId,
    desktop && hasCollection && activeView === "collection",
    { includePreview: true }
  );
  const tabGroupId = useId();
  const recommendedTabRef = useRef<HTMLButtonElement | null>(null);
  const collectionTabRef = useRef<HTMLButtonElement | null>(null);
  const collectionListRef = useRef<HTMLUListElement | null>(null);
  const currentCollectionItemRef = useRef<HTMLLIElement | null>(null);
  const location = useLocation();
  const locationState = location.state as { from?: unknown } | null;
  const returnPath =
    typeof locationState?.from === "string"
      ? resolveVideoReturnPath(locationState.from)
      : resolveVideoReturnPath(routeToPath(location));
  const recommendedTabId = `${tabGroupId}-recommended-tab`;
  const collectionTabId = `${tabGroupId}-collection-tab`;
  const recommendedPanelId = `${tabGroupId}-recommended-panel`;
  const collectionPanelId = `${tabGroupId}-collection-panel`;

  useEffect(() => {
    if (!hasCollection && activeView === "collection") {
      setActiveView("recommended");
    } else if (
      !hasRecommendations &&
      hasCollection &&
      activeView === "recommended"
    ) {
      setActiveView("collection");
    }
  }, [activeView, hasCollection, hasRecommendations]);

  useEffect(() => {
    const media = window.matchMedia("(min-width: 769px)");
    const handleChange = () => {
      setDesktop(media.matches);
      if (!media.matches && hasRecommendations) {
        setActiveView("recommended");
      }
    };
    handleChange();
    media.addEventListener("change", handleChange);
    return () => media.removeEventListener("change", handleChange);
  }, [hasRecommendations]);

  useEffect(() => {
    if (
      !desktop ||
      activeView !== "collection" ||
      !data ||
      data.items.length === 0
    ) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const list = collectionListRef.current;
      const current = currentCollectionItemRef.current;
      if (!list || !current) return;
      list.scrollTop = Math.max(
        0,
        current.offsetTop - list.clientHeight / 2 + current.clientHeight / 2
      );
    });
    return () => window.cancelAnimationFrame(frame);
  }, [activeView, data, desktop]);

  if (!hasRecommendations && !hasCollection) return null;

  function selectView(nextView: RailView) {
    if (nextView === "recommended" && !hasRecommendations) return;
    setActiveView(nextView);
  }

  function handleTabKeyDown(event: React.KeyboardEvent<HTMLButtonElement>) {
    if (!hasRecommendations) return;
    let nextView: RailView | null = null;
    if (event.key === "ArrowLeft" || event.key === "Home") {
      nextView = "recommended";
    } else if (event.key === "ArrowRight" || event.key === "End") {
      nextView = "collection";
    }
    if (!nextView) return;
    event.preventDefault();
    setActiveView(nextView);
    const nextRef =
      nextView === "recommended" ? recommendedTabRef : collectionTabRef;
    nextRef.current?.focus();
  }

  const showCollection = hasCollection && activeView === "collection";

  return (
    <aside
      className={`vd-rail${
        hasRecommendations ? "" : " vd-rail--collection-only"
      }`}
      aria-label={hasCollection ? "视频推荐与相关合集" : "推荐视频"}
    >
      {hasCollection && (
        <div className="vd-rail__tabs" role="tablist" aria-label="视频列表">
          <button
            ref={recommendedTabRef}
            id={recommendedTabId}
            type="button"
            className="vd-rail__tab"
            role="tab"
            aria-selected={activeView === "recommended"}
            aria-controls={recommendedPanelId}
            tabIndex={activeView === "recommended" ? 0 : -1}
            disabled={!hasRecommendations}
            onClick={() => selectView("recommended")}
            onKeyDown={handleTabKeyDown}
          >
            推荐视频
          </button>
          <button
            ref={collectionTabRef}
            id={collectionTabId}
            type="button"
            className="vd-rail__tab"
            role="tab"
            aria-selected={showCollection}
            aria-controls={collectionPanelId}
            tabIndex={showCollection ? 0 : -1}
            onClick={() => selectView("collection")}
            onKeyDown={handleTabKeyDown}
          >
            相关合集
          </button>
        </div>
      )}

      <header
        className={`vd-rail__head${hasCollection ? " vd-rail__head--mobile-only" : ""}`}
      >
        <span className="vd-rail__head-icon" aria-hidden="true">
          <span />
          <span />
        </span>
        <h2 className="vd-rail__head-title">推荐视频</h2>
      </header>

      {showCollection ? (
        <div
          id={collectionPanelId}
          className="vd-rail__tabpanel vd-rail__tabpanel--collection"
          role="tabpanel"
          aria-labelledby={collectionTabId}
        >
          {!data && !error ? (
            <CollectionRailState />
          ) : error && !data ? (
            <div className="vd-rail__state" role="alert">
              <span>{error}</span>
              <button type="button" onClick={retry}>
                重新加载
              </button>
            </div>
          ) : !data || data.items.length === 0 ? (
            <div className="vd-rail__state" role="status">
              当前合集暂无视频
            </div>
          ) : (
            <ul
              ref={collectionListRef}
              className="vd-rail__list vd-rail__collection-list"
              aria-label={`${data.name}，共 ${data.total} 个视频`}
            >
              {data.items.map((video) => {
                const current = video.id === videoId;
                return (
                  <RecommendedItem
                    key={video.id}
                    ref={current ? currentCollectionItemRef : undefined}
                    video={video}
                    current={current}
                    returnPath={returnPath}
                    variant="collection"
                  />
                );
              })}
            </ul>
          )}
        </div>
      ) : (
        <div
          id={hasCollection ? recommendedPanelId : undefined}
          className="vd-rail__tabpanel vd-rail__tabpanel--recommended"
          role={hasCollection ? "tabpanel" : undefined}
          aria-labelledby={hasCollection ? recommendedTabId : undefined}
        >
          <ul className="vd-rail__list">
            {videos.map((video) => (
              <RecommendedItem
                key={video.id}
                video={video}
                returnPath={returnPath}
              />
            ))}
          </ul>
        </div>
      )}
    </aside>
  );
}

export function VideoRailSkeleton() {
  return (
    <aside className="vd-rail" aria-label="视频列表加载中" aria-busy="true">
      <div
        className="vd-rail__tabs vd-rail__tabs--loading"
        aria-hidden="true"
      >
        <span className="vd-rail__tab" aria-selected="true">
          推荐视频
        </span>
        <span className="vd-rail__tab">相关合集</span>
      </div>
      <header className="vd-rail__head vd-rail__head--mobile-only">
        <span className="vd-rail__head-icon" aria-hidden="true">
          <span />
          <span />
        </span>
        <h2 className="vd-rail__head-title">推荐视频</h2>
      </header>
      <CollectionRailState label="正在加载视频列表" />
    </aside>
  );
}

function CollectionRailState({
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
            <span />
          </span>
        </div>
      ))}
    </div>
  );
}

type RailItemProps = {
  video: VideoItem | VideoCollectionItem;
  returnPath: string;
  current?: boolean;
  variant?: "recommended" | "collection";
};

const RecommendedItem = forwardRef<HTMLLIElement, RailItemProps>(
  RecommendedItemContent
);

function RecommendedItemContent(
  {
    video,
    returnPath,
    current = false,
    variant = "recommended",
  }: RailItemProps,
  forwardedRef: React.ForwardedRef<HTMLLIElement>
) {
  const [previewState, setPreviewState] = useState<PreviewState>("idle");
  const [shouldRenderPreview, setShouldRenderPreview] = useState(false);
  const [progress, setProgress] = useState(0);

  const rootRef = useRef<HTMLLIElement | null>(null);
  const hoverTimerRef = useRef<number | null>(null);
  const lastPointerTypeRef = useRef<string>("");
  const canHoverRef = useRef(true);
  const videoRef = useRef<HTMLVideoElement | null>(null);

  const activeId = useActivePreviewId();
  const inView = useInViewport(rootRef);
  const setRootRef = useCallback(
    (node: HTMLLIElement | null) => {
      rootRef.current = node;
      if (typeof forwardedRef === "function") {
        forwardedRef(node);
      } else if (forwardedRef) {
        forwardedRef.current = node;
      }
    },
    [forwardedRef]
  );

  // 全局预览换卡时立即清理
  useEffect(() => {
    if (activeId !== video.id && shouldRenderPreview) {
      cleanup();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeId, video.id]);

  // 离开视口立即停
  useEffect(() => {
    if (!inView && shouldRenderPreview) {
      cleanup();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inView]);

  // 卸载清理
  useEffect(() => {
    return () => {
      cleanup();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 检测当前设备是否支持 hover（鼠标 vs 触屏）
  useEffect(() => {
    const media = window.matchMedia("(hover: hover) and (pointer: fine)");
    const update = () => {
      canHoverRef.current = media.matches;
    };
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  function cleanup() {
    if (hoverTimerRef.current) {
      window.clearTimeout(hoverTimerRef.current);
      hoverTimerRef.current = null;
    }

    const el = videoRef.current;
    if (el) {
      try {
        el.pause();
        el.removeAttribute("src");
        el.load();
      } catch {
        // noop
      }
    }

    setShouldRenderPreview(false);
    setPreviewState("idle");
    setProgress(0);

    if (previewController.getActiveId() === video.id) {
      previewController.setActiveId(null);
    }
  }

  function startPreviewIntent() {
    if (!video.previewSrc || !inView) return;
    if (hoverTimerRef.current) return;
    setPreviewState("intent");
    hoverTimerRef.current = window.setTimeout(() => {
      hoverTimerRef.current = null;
      startPreviewNow({ requireInView: true });
    }, HOVER_DELAY_MS);
  }

  function startPreviewNow(options: { requireInView: boolean }) {
    if (!video.previewSrc) return;
    if (options.requireInView && !inView) return;
    if (hoverTimerRef.current) {
      window.clearTimeout(hoverTimerRef.current);
      hoverTimerRef.current = null;
    }
    previewController.setActiveId(video.id);
    setShouldRenderPreview(true);
    setPreviewState("loading");
  }

  function stopPreview() {
    cleanup();
  }

  function handlePointerEnter(event: React.PointerEvent<HTMLLIElement>) {
    lastPointerTypeRef.current = event.pointerType;
    if (shouldStartInstantPreview({ pointerType: event.pointerType })) return;
    startPreviewIntent();
  }

  function handlePointerLeave(event: React.PointerEvent<HTMLLIElement>) {
    if (shouldStartInstantPreview({ pointerType: event.pointerType })) return;
    stopPreview();
  }

  function handlePointerDown(event: React.PointerEvent<HTMLLIElement>) {
    lastPointerTypeRef.current = event.pointerType;
  }

  function handleClickCapture(event: React.MouseEvent<HTMLAnchorElement>) {
    const previewActive = activeId === video.id && shouldRenderPreview;
    if (
      !shouldInterceptPreviewTap({
        pointerType: lastPointerTypeRef.current,
        canHover: canHoverRef.current,
        previewActive,
      })
    ) {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
    startPreviewNow({ requireInView: false });
  }

  const author = "author" in video ? video.author : "";
  const quality = "quality" in video ? video.quality : undefined;

  return (
    <li
      ref={setRootRef}
      className={`vd-rail__item${
        variant === "collection" ? " vd-rail__collection-item" : ""
      }`}
      onPointerEnter={handlePointerEnter}
      onPointerLeave={handlePointerLeave}
      onPointerDown={handlePointerDown}
      onFocus={startPreviewIntent}
      onBlur={stopPreview}
    >
      <Link
        to={video.href}
        state={{ from: returnPath }}
        className="vd-rail__link"
        aria-current={current ? "page" : undefined}
        onClickCapture={handleClickCapture}
        onClick={current ? (event) => event.preventDefault() : undefined}
      >
        <div className="vd-rail__thumb">
          <VideoThumbnail src={video.thumbnail} />
          {shouldRenderPreview && video.previewSrc && (
            <PreviewVideo
              ref={videoRef}
              src={video.previewSrc}
              state={previewState}
              onCanPlay={() => setPreviewState("playing")}
              onError={() => setPreviewState("error")}
              onTimeUpdate={(p) => setProgress(p)}
            />
          )}
          {previewState === "loading" && (
            <span className="preview-loader" />
          )}
          {previewState === "error" && (
            <span className="preview-error">预览加载失败</span>
          )}
          {previewState === "playing" && (
            <div className="preview-progress" aria-hidden="true">
              <div
                className="preview-progress__bar"
                style={{ width: `${Math.min(100, progress * 100)}%` }}
              />
            </div>
          )}
          {video.duration && previewState !== "playing" && (
            <span className="vd-rail__duration">{video.duration}</span>
          )}
          {quality === "HD" && previewState !== "playing" && (
            <span className="vd-rail__hd">HD</span>
          )}
          {current && previewState !== "playing" && (
            <span className="vd-rail__current">当前视频</span>
          )}
        </div>
        <div className="vd-rail__body">
          <h3 className="vd-rail__title" title={video.title}>
            {video.title}
          </h3>
          <div className="vd-rail__meta">
            {author && <span className="vd-rail__author">{author}</span>}
            {variant === "collection" ? (
              <span className="vd-rail__views">
                <Eye size={12} aria-hidden="true" />
                {formatCount(video.views)} 观看
              </span>
            ) : (
              <span>{formatCount(video.views)} 观看</span>
            )}
            {video.publishedAt && <span>{video.publishedAt}</span>}
          </div>
        </div>
      </Link>
    </li>
  );
}
