import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  ChevronLeft,
  Heart,
  Play,
  Volume2,
  VolumeX,
  EyeOff,
  Info,
  AlertCircle,
  Share2,
} from "lucide-react";
import { hideVideo, type ShortsItem } from "@/data/videos";
import {
  clamp,
  getVideoWindowBounds,
  videoBufferIsCritical,
  videoHasBufferedData,
  videoHasComfortableBuffer,
} from "@/shorts/mediaBuffer";
import {
  isWindowsPlatform,
  shouldUseDocumentScrollForShorts,
  shouldUseIOSSharedVideo,
} from "@/shorts/platform";
import {
  isShortsDebugEnabled,
  ShortsDebugHud,
} from "@/shorts/ShortsDebugHud";
import { useShortsFeed } from "@/shorts/useShortsFeed";
import {
  useShortsKeyboard,
  type ShortsKeyboardSeekPreview,
} from "@/shorts/useShortsKeyboard";
import { useShortsSlideGestures } from "@/shorts/useShortsSlideGestures";
import { AdminEmptyVisual } from "@/admin/AdminEmptyVisual";
import { useAuth } from "@/admin/AuthContext";
import {
  copyExistingVideoShareURL,
  createAndCopyVideoShare,
} from "@/lib/videoShareClipboard";
import "@/styles/shorts.css";

// 当前视频流畅播放后，向后预加载多少条视频。
const PRELOAD_AHEAD_COUNT = 2;

// iOS 的 AVPlayer 在向后 seek 以开始下一轮时，偶尔会保持“逻辑上正在播放”
// 但迟迟没有新画面。先走普通 seek；超过这个时间仍未呈现首帧时，才对同一
// media element 做一次 load() 自救，避免每轮都重新请求视频。
const IOS_LOOP_FRAME_WATCHDOG_MS = 1200;
const IOS_LOOP_RELOAD_TIMEOUT_MS = 6000;
// WebKit 会在极短的解码抖动中发 waiting。延迟一点再展示，避免视频画面
// 仍在连续推进时闪出或残留加载图标。
const SHORTS_BUFFERING_INDICATOR_DELAY_MS = 180;

export default function ShortsPage() {
  const { isAdmin } = useAuth();
  const navigate = useNavigate();
  // 当前在视口里的视频索引
  const [activeIndex, setActiveIndex] = useState(0);
  // 队列因空库被丢弃时回到第一屏
  const handleQueueReset = useCallback(() => setActiveIndex(0), []);
  // 已加入页面的视频队列（按出现顺序）与拉取状态
  const { items, loading, empty, loadError, loadMore } = useShortsFeed(
    activeIndex,
    handleQueueReset
  );
  // 是否静音；首次必须静音才能 autoplay，用户点击后切换
  const [muted, setMuted] = useState(true);
  // 全局 Toast / HUD 提醒文字
  const [hudText, setHudText] = useState<{ id: number; text: string; icon?: React.ReactNode } | null>(null);
  const hudTimeoutRef = useRef<number | null>(null);

  const showHud = useCallback((text: string, icon?: React.ReactNode) => {
    if (hudTimeoutRef.current) window.clearTimeout(hudTimeoutRef.current);
    setHudText({ id: Date.now(), text, icon });
    hudTimeoutRef.current = window.setTimeout(() => {
      setHudText(null);
    }, 1500);
  }, []);

  const stopHeaderControlPropagation = useCallback((e: React.SyntheticEvent) => {
    e.stopPropagation();
  }, []);

  const handleMuteButtonClick = useCallback(() => {
    const activeVideo = getVideoAtIndex(activeIndex);
    const canResumeActiveVideo = () =>
      Boolean(activeVideo) &&
      getVideoAtIndex(activeIndexRef.current) === activeVideo &&
      userPausedIndexRef.current !== activeIndexRef.current;
    const next = !muted;
    if (activeVideo) {
      normalizeVideoPlaybackRate(activeVideo);
      applyVideoMutedState(activeVideo, next);
      // 必须直接发生在这个 click 回调中：这一次 play() 给 iOS 的持久
      // media element 授予有声播放权限，之后切 src 仍复用同一元素。
      if (canResumeActiveVideo()) {
        activeVideo.play().catch(() => undefined);
      }
      stabilizeVideoAfterAudioToggle(
        activeVideo,
        canResumeActiveVideo
      );
    }
    setMuted(next);
    showHud(
      next ? "已静音" : "音量已开启",
      next ? <VolumeX size={16} /> : <Volume2 size={16} />
    );
  }, [activeIndex, muted, showHud]);

  // 组件卸载时清理 HUD 定时器
  useEffect(() => {
    return () => {
      if (hudTimeoutRef.current) window.clearTimeout(hudTimeoutRef.current);
    };
  }, []);

  const containerRef = useRef<HTMLDivElement | null>(null);
  const itemsLengthRef = useRef(items.length);
  itemsLengthRef.current = items.length;
  // index → video element，用来精确控制播放/暂停
  const videoRefs = useRef<Map<number, HTMLVideoElement>>(new Map());
  const videoRefCallbacks = useRef<
    Map<number, (el: HTMLVideoElement | null) => void>
  >(new Map());
  const iosSharedVideoRef = useRef<HTMLVideoElement | null>(null);
  const iosSharedVideoSlots = useRef<Map<number, HTMLDivElement>>(new Map());
  const iosSharedVideoSlotCallbacks = useRef<
    Map<number, (el: HTMLDivElement | null) => void>
  >(new Map());
  const activeIndexRef = useRef(0);
  // Windows 退出浏览器全屏时视口高度会改变。调整滚动位置期间锁住当前
  // slide，避免 IntersectionObserver 把新的像素位置误判成后续视频。
  const viewportResizeAnchorIndexRef = useRef<number | null>(null);
  const userPausedIndexRef = useRef<number | null>(null);
  const [activeReadyForPreload, setActiveReadyForPreload] = useState(false);
  const [, setUserPausedIndexState] = useState<number | null>(null);
  const [cacheableSourceIds, setCacheableSourceIds] = useState<Set<string>>(
    () => new Set()
  );
  const [cacheWindowHighIndex, setCacheWindowHighIndex] = useState(-1);

  // ?debug=1 时叠加只读观测面板；开关在页面生命周期内固定。
  const [debugHudEnabled] = useState(isShortsDebugEnabled);
  // iPhone 浏览器里改用页面滚动，让 Safari 工具栏能随刷动收起。
  const useDocumentScroll = shouldUseDocumentScrollForShorts();
  // Windows 短视频页只保留静音图标；不挂载桌面 hover 音量条，避免点击
  // 图标时因鼠标仍停留在按钮上而展开滑杆。
  const isWindowsShortsPlatform = isWindowsPlatform();
  // iOS/WebKit 的有声播放授权按 media element 管理。iOS 分支始终复用
  // 同一个真实 <video>，滑动时只移动节点并更换 src。
  const useIOSSharedVideo = shouldUseIOSSharedVideo();

  const handleBackToHomeClick = useCallback(
    (event: React.MouseEvent<HTMLAnchorElement>) => {
      // 主页导航点击 documentElement 后进入的是“文档全屏”，SPA 路由切换
      // 不会自动退出。先等待 Fullscreen API 完成，再渲染首页，避免首页继承
      // 短视频的全屏状态或先以全屏闪现。
      const exitRequest = exitDocumentFullscreen();
      if (!exitRequest) return;

      event.preventDefault();
      const returnHome = () => navigate("/");
      void exitRequest.then(returnHome, returnHome);
    },
    [navigate]
  );

  function getVideoAtIndex(index: number) {
    if (useIOSSharedVideo && index === activeIndexRef.current) {
      return iosSharedVideoRef.current ?? undefined;
    }
    return videoRefs.current.get(index);
  }

  // 本次会话内已经点过赞的视频 id 集合。
  // 与后端的真实 likes 字段同步——后端是单纯计数器，前端在这里防重避免连发。
  // 用户在操作栏点取消时会从这里移除，允许之后再次点赞。
  const likedIdsRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    activeIndexRef.current = activeIndex;
  }, [activeIndex]);

  const updateUserPausedIndex = useCallback((index: number | null) => {
    userPausedIndexRef.current = index;
    setUserPausedIndexState(index);
  }, []);

  const setUserPausedForIndex = useCallback(
    (index: number, isPaused: boolean) => {
      if (isPaused) {
        updateUserPausedIndex(index);
      } else if (userPausedIndexRef.current === index) {
        updateUserPausedIndex(null);
      }
    },
    [updateUserPausedIndex]
  );

  const isVideoPausedByUser = useCallback(
    (index: number) => userPausedIndexRef.current === index,
    []
  );

  // 键盘快捷键：↑↓切换、空格播放/暂停（双空格点赞）、←→累计快进、M/L
  const { keyboardSeekPreview, registerKeyboardLikeHandler } =
    useShortsKeyboard({
      containerRef,
      activeIndexRef,
      itemsLengthRef,
      getVideoAtIndex,
      isVideoPausedByUser,
      setUserPausedForIndex,
      onToggleMute: handleMuteButtonClick,
      showHud,
      isWindowsShortsPlatform,
    });

  useEffect(() => {
    updateUserPausedIndex(null);
  }, [activeIndex, updateUserPausedIndex]);

  const handleActiveReadyForPreload = useCallback((index: number) => {
    if (index === activeIndexRef.current) {
      setActiveReadyForPreload(true);
    }
  }, []);

  const handleActiveNeedsPriority = useCallback((index: number) => {
    if (index === activeIndexRef.current) {
      setActiveReadyForPreload(false);
    }
  }, []);

  // 标记某条视频"浏览器里已有可复用的缓冲"。之后只要它还在缓存窗口内，
  // 就保留 src 不剥离，回滑/再前滑时直接续用已缓冲数据，秒开不卡顿。
  const handleSourceCached = useCallback((videoId: string) => {
    setCacheableSourceIds((prev) => {
      if (prev.has(videoId)) return prev;
      const next = new Set(prev);
      next.add(videoId);
      return next;
    });
  }, []);

  /**
   * 切换点赞状态。
   * - liked=true：发 POST /api/video/:id/like
   * - liked=false：发 DELETE /api/video/:id/like
   * 返回服务端最新 likes 值；请求失败返回 null（调用方可回滚 UI）。
   */
  const handleLikeToggle = useCallback(
    async (videoId: string, liked: boolean): Promise<number | null> => {
      // 维护本地集合以保持双击去重逻辑（已经在集合里就不会重复点赞）
      if (liked) {
        likedIdsRef.current.add(videoId);
      } else {
        likedIdsRef.current.delete(videoId);
      }
      try {
        const res = await fetch(
          `/api/video/${encodeURIComponent(videoId)}/like`,
          {
            method: liked ? "POST" : "DELETE",
            credentials: "include",
          }
        );
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = (await res.json()) as { likes?: number };
        return typeof data.likes === "number" ? data.likes : null;
      } catch {
        // 请求失败：回滚集合，让 Slide 自己回滚 UI
        if (liked) {
          likedIdsRef.current.delete(videoId);
        } else {
          likedIdsRef.current.add(videoId);
        }
        return null;
      }
    },
    []
  );

  /** 当前 id 是否已经在本次会话内点过赞（供 Slide 切换 active 时同步状态） */
  const hasLiked = useCallback(
    (videoId: string) => likedIdsRef.current.has(videoId),
    []
  );

  // 记录实际到达过的最远索引，驱动固定大小的视频缓存窗口。
  useEffect(() => {
    if (!items[activeIndex]) return;
    setCacheWindowHighIndex((prev) => Math.max(prev, activeIndex));
  }, [activeIndex, items]);

  // 全屏与窗口模式的可用高度不同。Chrome/Edge 退出全屏后会保留原来的
  // scrollTop 像素值，而每条 slide 的 100svh 已经变矮；索引越靠后，误差
  // 累积越大，最终会露出下一条并触发切源。视口 resize 期间始终用当前索引
  // 的新 offsetTop 重新对齐，待尺寸稳定后再交还给正常的滑动观察器。
  useEffect(() => {
    if (!isWindowsShortsPlatform) return;
    const root = containerRef.current;
    if (!root) return;

    let alignmentFrame: number | null = null;
    let settleTimer: number | null = null;

    const alignAnchoredSlide = () => {
      const anchorIndex = viewportResizeAnchorIndexRef.current;
      if (anchorIndex === null) return;
      const activeSlide = root.querySelector<HTMLElement>(
        `[data-shorts-slide][data-index="${anchorIndex}"]`
      );
      if (!activeSlide) return;
      root.scrollTop = activeSlide.offsetTop;
    };

    const handleViewportResize = () => {
      if (viewportResizeAnchorIndexRef.current === null) {
        viewportResizeAnchorIndexRef.current = activeIndexRef.current;
      }

      // resize 事件触发时 viewport unit 通常已经更新，先同步对齐一次；下一帧
      // 再对齐可覆盖浏览器工具栏完成布局后的第二次尺寸计算。
      alignAnchoredSlide();
      if (alignmentFrame !== null) {
        window.cancelAnimationFrame(alignmentFrame);
      }
      alignmentFrame = window.requestAnimationFrame(() => {
        alignmentFrame = null;
        alignAnchoredSlide();
      });

      if (settleTimer !== null) window.clearTimeout(settleTimer);
      settleTimer = window.setTimeout(() => {
        settleTimer = null;
        alignAnchoredSlide();
        viewportResizeAnchorIndexRef.current = null;
      }, 240);
    };

    window.addEventListener("resize", handleViewportResize);
    document.addEventListener("fullscreenchange", handleViewportResize);
    return () => {
      window.removeEventListener("resize", handleViewportResize);
      document.removeEventListener("fullscreenchange", handleViewportResize);
      if (alignmentFrame !== null) {
        window.cancelAnimationFrame(alignmentFrame);
      }
      if (settleTimer !== null) window.clearTimeout(settleTimer);
      viewportResizeAnchorIndexRef.current = null;
    };
  }, [isWindowsShortsPlatform]);

  // 用 IntersectionObserver 找出当前进入视口的 item。
  // root 直接用 viewport：普通模式和 iPhone 页面滚动模式都能正确观测。
  useEffect(() => {
    const root = containerRef.current;
    if (!root) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (viewportResizeAnchorIndexRef.current !== null) return;
        let bestIndex = -1;
        let bestRatio = 0.6;
        for (const entry of entries) {
          if (entry.intersectionRatio > bestRatio) {
            bestRatio = entry.intersectionRatio;
            const idx = Number(
              (entry.target as HTMLElement).dataset.index ?? -1
            );
            if (!Number.isNaN(idx)) bestIndex = idx;
          }
        }
        if (bestIndex >= 0 && bestIndex !== activeIndexRef.current) {
          activeIndexRef.current = bestIndex;
          const sharedVideo = iosSharedVideoRef.current;
          if (sharedVideo && !sharedVideo.paused) sharedVideo.pause();
          setActiveReadyForPreload(false);
          setActiveIndex(bestIndex);
        }
      },
      {
        root: null,
        threshold: [0.6, 0.85],
      }
    );

    const slides = root.querySelectorAll<HTMLElement>("[data-shorts-slide]");
    slides.forEach((el) => observer.observe(el));
    return () => observer.disconnect();
  }, [items.length]);

  // 先停掉所有非当前屏。当前屏的 play() 由 ShortsSlide 负责，
  // 那里能在 Safari 拒绝/中断播放时同步 UI，并在 canplay 后安全重试。
  useEffect(() => {
    videoRefs.current.forEach((video, idx) => {
      if (idx !== activeIndex && !video.paused) video.pause();
    });
  }, [activeIndex, items.length]);

  // 只同步静音属性。页面不读写 video.volume，实际响度完全交给系统音量。
  // 这里不做 play/pause，避免手机端切换静音时打断播放节奏。
  useEffect(() => {
    const sharedVideo = iosSharedVideoRef.current;
    if (sharedVideo) applyVideoMutedState(sharedVideo, muted);
    videoRefs.current.forEach((video) => {
      applyVideoMutedState(video, muted);
    });
  }, [muted, items.length, useIOSSharedVideo]);

  // 页面卸载时暂停所有
  useEffect(() => {
    return () => {
      const sharedVideo = iosSharedVideoRef.current;
      if (sharedVideo) {
        try {
          sharedVideo.pause();
        } catch {
          // ignore
        }
      }
      videoRefs.current.forEach((v) => {
        try {
          v.pause();
        } catch {
          // ignore
        }
      });
    };
  }, []);

  const setVideoRef = useCallback((index: number) => {
    const existing = videoRefCallbacks.current.get(index);
    if (existing) return existing;
    const callback = (el: HTMLVideoElement | null) => {
      if (el) videoRefs.current.set(index, el);
      else videoRefs.current.delete(index);
    };
    videoRefCallbacks.current.set(index, callback);
    return callback;
  }, []);

  const setIOSSharedVideoSlotRef = useCallback((index: number) => {
    const existing = iosSharedVideoSlotCallbacks.current.get(index);
    if (existing) return existing;
    const callback = (el: HTMLDivElement | null) => {
      if (el) iosSharedVideoSlots.current.set(index, el);
      else iosSharedVideoSlots.current.delete(index);
    };
    iosSharedVideoSlotCallbacks.current.set(index, callback);
    return callback;
  }, []);

  // iOS 只创建一次真实 video，并在切屏时把同一个 DOM 节点移动到当前 slide。
  // 不给节点设置 React key，也不在切屏时 remove/recreate，保留 WebKit 已授予
  // 这个 media element 的有声播放权限。
  useLayoutEffect(() => {
    if (!useIOSSharedVideo) return;
    const item = items[activeIndex];
    const slot = iosSharedVideoSlots.current.get(activeIndex);
    if (!item || !slot) return;

    let video = iosSharedVideoRef.current;
    if (!video) {
      video = document.createElement("video");
      video.className =
        "shorts-slide__video shorts-slide__video--ios-shared";
      video.autoplay = true;
      // WebKit 原生 loop 会在内部做一次不可观察的 backward seek；媒体时钟
      // 可能已经开始下一轮，但新帧尚未送到合成器。iOS 改由 ShortsSlide
      // 在 ended 后受控重播，桌面/Android 的 JSX video 仍保留原生 loop。
      video.loop = false;
      video.playsInline = true;
      video.preload = "auto";
      video.disablePictureInPicture = true;
      video.setAttribute("autoplay", "");
      video.setAttribute("playsinline", "");
      video.setAttribute("webkit-playsinline", "");
      video.setAttribute("controlslist", "nodownload");
      video.setAttribute("aria-hidden", "true");
      video.addEventListener("contextmenu", preventMediaContextMenu);
      iosSharedVideoRef.current = video;
    }

    slot.appendChild(video);
    applyVideoMutedState(video, muted);
    try {
      video.defaultMuted = muted;
    } catch {
      // ignore
    }

    if (video.dataset.shortsVideoId !== item.id) {
      try {
        video.pause();
      } catch {
        // ignore
      }
      video.dataset.shortsVideoId = item.id;
      video.poster = item.poster;
      video.src = item.videoSrc;
      video.load();
    } else if (video.getAttribute("poster") !== item.poster) {
      video.poster = item.poster;
    }
  }, [activeIndex, items, muted, useIOSSharedVideo]);

  useLayoutEffect(() => {
    return () => {
      const video = iosSharedVideoRef.current;
      if (!video) return;
      video.removeEventListener("contextmenu", preventMediaContextMenu);
      releaseVideoSource(video);
      video.remove();
      iosSharedVideoRef.current = null;
    };
  }, []);

  useEffect(() => {
    document.title = "短视频";
  }, []);

  // 沉浸式：默认锁住 body 滚动；iPhone 浏览器里放开根页面滚动，让 Safari 工具栏能随刷动收起。
  useEffect(() => {
    const html = document.documentElement;
    const body = document.body;
    const prevHtmlOverflow = html.style.overflow;
    const prevBodyOverflow = body.style.overflow;
    const prevBodyBg = body.style.background;
    if (useDocumentScroll) {
      html.classList.add("shorts-document-scroll");
      body.classList.add("shorts-document-scroll");
    } else {
      html.style.overflow = "hidden";
      body.style.overflow = "hidden";
      body.style.background = "#000";
    }

    let prevThemeColor: string | null = null;
    let themeMeta = document.querySelector<HTMLMetaElement>(
      'meta[name="theme-color"]'
    );
    const createdMeta = !themeMeta;
    if (!themeMeta) {
      themeMeta = document.createElement("meta");
      themeMeta.name = "theme-color";
      document.head.appendChild(themeMeta);
    } else {
      prevThemeColor = themeMeta.content;
    }
    themeMeta.content = "#000000";

    return () => {
      html.classList.remove("shorts-document-scroll");
      body.classList.remove("shorts-document-scroll");
      html.style.overflow = prevHtmlOverflow;
      body.style.overflow = prevBodyOverflow;
      body.style.background = prevBodyBg;
      if (themeMeta) {
        if (createdMeta) {
          themeMeta.remove();
        } else if (prevThemeColor !== null) {
          themeMeta.content = prevThemeColor;
        }
      }
    };
  }, [useDocumentScroll]);

  const handleHideSuccess = useCallback((idx: number) => {
    const nextIdx = idx + 1;
    if (nextIdx < items.length) {
      setTimeout(() => {
        const nextSlide = containerRef.current?.querySelector(`[data-index="${nextIdx}"]`);
        if (nextSlide) {
          nextSlide.scrollIntoView({ behavior: "smooth" });
        }
      }, 700);
    }
  }, [items.length]);

  const videoWindow = getVideoWindowBounds(cacheWindowHighIndex, items.length);

  return (
    <div
      className={`shorts-page${useDocumentScroll ? " is-document-scroll" : ""}`}
    >
      <header className="shorts-header">
        <Link
          to="/"
          className="shorts-header__back"
          aria-label="返回首页"
          onClick={handleBackToHomeClick}
        >
          <ChevronLeft size={22} />
        </Link>
        <div className="shorts-header__actions">
          {items.length > 0 && (
            <button
              type="button"
              className="shorts-header__icon-btn"
              aria-label={muted ? "取消静音" : "静音"}
              onPointerDownCapture={stopHeaderControlPropagation}
              onTouchStartCapture={stopHeaderControlPropagation}
              onMouseDownCapture={stopHeaderControlPropagation}
              onPointerDown={stopHeaderControlPropagation}
              onTouchStart={stopHeaderControlPropagation}
              onMouseDown={stopHeaderControlPropagation}
              onClick={(e) => {
                e.stopPropagation();
                handleMuteButtonClick();
              }}
            >
              {muted ? <VolumeX size={20} /> : <Volume2 size={20} />}
            </button>
          )}
        </div>
      </header>

      {hudText && (
        <div key={hudText.id} className="shorts-hud-toast">
          {hudText.icon}
          <span>{hudText.text}</span>
        </div>
      )}

      {isWindowsShortsPlatform &&
        keyboardSeekPreview?.videoIndex === activeIndex && (
        <div className="shorts-keyboard-seek-time" aria-live="polite">
          {formatClock(keyboardSeekPreview.currentTime)} / {formatClock(keyboardSeekPreview.duration)}
        </div>
      )}

      {debugHudEnabled && (
        <ShortsDebugHud
          activeIndex={activeIndex}
          itemCount={items.length}
          itemId={items[activeIndex]?.id ?? null}
          getActiveVideo={() => getVideoAtIndex(activeIndexRef.current) ?? null}
          windowStart={videoWindow.start}
          windowEnd={videoWindow.end}
          activeReadyForPreload={activeReadyForPreload}
          cachedSourceCount={cacheableSourceIds.size}
          muted={muted}
          usesIOSSharedVideo={useIOSSharedVideo}
          usesDocumentScroll={useDocumentScroll}
        />
      )}

      <div className="shorts-feed" ref={containerRef}>
        {loading && items.length === 0 && !empty && !loadError && (
          <div className="shorts-empty shorts-loading" aria-live="polite">
            <div className="shorts-empty__content">
              <ShortsLoadingSpinner size={30} />
              <p>正在加载短视频</p>
            </div>
          </div>
        )}

        {loadError && items.length === 0 && (
          <div className="shorts-empty" role="alert">
            <div className="shorts-empty__content">
              <p>短视频加载失败，请检查网络后重试</p>
              <button
                type="button"
                className="shorts-empty__link"
                onClick={() => void loadMore()}
              >
                重新加载
              </button>
            </div>
          </div>
        )}

        {empty && items.length === 0 && (
          <div className="shorts-empty">
            <AdminEmptyVisual
              variant="empty"
              text="当前库中没有视频"
              className="shorts-empty__visual"
            />
          </div>
        )}

        {items.map((item, index) => {
          const isActiveSlide = index === activeIndex;
          const isInCacheWindow =
            index >= videoWindow.start && index <= videoWindow.end;
          const preloadOffset = index - activeIndex;
          const shouldPreload =
            !useIOSSharedVideo &&
            activeReadyForPreload &&
            preloadOffset > 0 &&
            preloadOffset <= PRELOAD_AHEAD_COUNT;
          const shouldMount =
            isActiveSlide ||
            (!useIOSSharedVideo && (isInCacheWindow || shouldPreload));
          // 视频窗口内已经缓冲过的视频保留 src：
          // 在窗口内来回切换时，直接复用浏览器已缓冲数据。
          const shouldRetainCached =
            !useIOSSharedVideo &&
            isInCacheWindow &&
            !isActiveSlide &&
            cacheableSourceIds.has(item.id);
          const shouldLoad = isActiveSlide || shouldPreload || shouldRetainCached;
          const shouldEagerLoad = isActiveSlide || shouldPreload;
          return (
            <ShortsSlide
              key={`${item.feedToken}:${item.feedCursor}`}
              item={item}
              index={index}
              isActive={isActiveSlide}
              // 固定 6 条视频窗口内才挂载 <video> 壳；
              // 当前屏先绑定 src；后两个视频等当前屏缓冲健康后再预加载；
              // 已缓冲过的窗口内视频保留 src，便于来回切换复用缓存。
              shouldMount={shouldMount}
              shouldLoad={shouldLoad}
              shouldEagerLoad={shouldEagerLoad}
              keyboardSeekPreview={
                keyboardSeekPreview?.videoIndex === index
                  ? keyboardSeekPreview
                  : undefined
              }
              sharedVideoRef={
                useIOSSharedVideo ? iosSharedVideoRef : undefined
              }
              sharedVideoSlotRef={
                useIOSSharedVideo
                  ? setIOSSharedVideoSlotRef(index)
                  : undefined
              }
              muted={muted}
              videoRef={setVideoRef(index)}
              onLikeToggle={handleLikeToggle}
              hasLiked={hasLiked}
              registerKeyboardLikeHandler={registerKeyboardLikeHandler}
              canHide={isAdmin}
              onHideSuccess={handleHideSuccess}
              onActiveReadyForPreload={handleActiveReadyForPreload}
              onActiveNeedsPriority={handleActiveNeedsPriority}
              onSourceCached={handleSourceCached}
              onUserPausedChange={setUserPausedForIndex}
              isVideoPausedByUser={isVideoPausedByUser}
              showHud={showHud}
            />
          );
        })}

        {loadError && items.length > 0 && (
          <div className="shorts-empty" role="alert">
            <div className="shorts-empty__content">
              <p>后续视频加载失败</p>
              <button
                type="button"
                className="shorts-empty__link"
                onClick={() => void loadMore()}
              >
                重新加载
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

type SlideProps = {
  item: ShortsItem;
  index: number;
  isActive: boolean;
  shouldMount: boolean;
  shouldLoad: boolean;
  shouldEagerLoad: boolean;
  /** 键盘长按左右键期间的累计目标，用来稳定驱动底部进度条。 */
  keyboardSeekPreview?: Pick<
    ShortsKeyboardSeekPreview,
    "currentTime" | "duration"
  >;
  /** iOS 所有 slide 共用的同一个持久 video DOM 节点 */
  sharedVideoRef?: React.RefObject<HTMLVideoElement>;
  /** 持久 video 当前应移动到的 slide 插槽 */
  sharedVideoSlotRef?: (el: HTMLDivElement | null) => void;
  muted: boolean;
  videoRef: (el: HTMLVideoElement | null) => void;
  /**
   * 切换点赞。第二参数 true 表示点赞，false 表示取消。
   * 返回服务端最新 likes 值；null 表示请求失败，调用方应回滚 UI。
   */
  onLikeToggle: (videoId: string, liked: boolean) => Promise<number | null>;
  /** 父组件查询某 id 是否已经在本次会话内点过赞 */
  hasLiked: (videoId: string) => boolean;
  /** 注册该 slide 的键盘点赞入口，供父级双空格快捷键直接调用。 */
  registerKeyboardLikeHandler: (
    index: number,
    handler: (() => void) | null
  ) => void;
  canHide: boolean;
  onHideSuccess: (index: number) => void;
  onActiveReadyForPreload: (index: number) => void;
  onActiveNeedsPriority: (index: number) => void;
  /** 本条视频在浏览器里已有可复用缓冲，之后在视频窗口内保留 src */
  onSourceCached: (videoId: string) => void;
  onUserPausedChange: (index: number, isPaused: boolean) => void;
  isVideoPausedByUser: (index: number) => boolean;
  showHud: (text: string, icon?: React.ReactNode) => void;
};

/**
 * 一屏短视频。
 *
 * - 长按 ≥400ms 进入 2 倍速，松手恢复（与详情页 VideoPlayer 行为一致）
 * - 横向滑动按当前播放点相对快进 / 快退，纵向滑动仍用于切换上下视频
 * - 单击切换播放 / 暂停
 * - 长按弹出的下载/分享菜单通过 contextmenu + CSS 屏蔽
 */
function ShortsSlide({
  item,
  index,
  isActive,
  shouldMount,
  shouldLoad,
  shouldEagerLoad,
  keyboardSeekPreview,
  sharedVideoRef,
  sharedVideoSlotRef,
  muted,
  videoRef,
  onLikeToggle,
  hasLiked,
  registerKeyboardLikeHandler,
  canHide,
  onHideSuccess,
  onActiveReadyForPreload,
  onActiveNeedsPriority,
  onSourceCached,
  onUserPausedChange,
  isVideoPausedByUser,
  showHud,
}: SlideProps) {
  const slideRef = useRef<HTMLElement | null>(null);
  const localRef = useRef<HTMLVideoElement | null>(null);
  const keyboardLikeHandlerRef = useRef<() => void>(() => undefined);
  const isActiveRef = useRef(isActive);
  const shouldLoadRef = useRef(shouldLoad);
  const shouldMountRef = useRef(shouldMount);
  const mutedRef = useRef(muted);
  const hasStartedPlayingRef = useRef(false);
  const loopRestartPendingRef = useRef(false);
  const loopRestartAwaitingFrameRef = useRef(false);
  const loopRestartReloadedRef = useRef(false);
  const loopRestartAttemptRef = useRef(0);
  const loopRestartTimerRef = useRef<number | null>(null);
  const loopFrameBarrierRef = useRef<number | null>(null);
  const lastObservedMediaTimeRef = useRef<number | null>(null);
  const lastPresentedMediaTimeRef = useRef<number | null>(null);
  const playbackMotionFrameCountRef = useRef(0);
  const bufferingIndicatorTimerRef = useRef<number | null>(null);
  isActiveRef.current = isActive;
  shouldLoadRef.current = shouldLoad;
  shouldMountRef.current = shouldMount;
  mutedRef.current = muted;
  const usesSharedVideo = Boolean(sharedVideoRef);
  const getVideoElement = useCallback(() => {
    if (sharedVideoRef) {
      return isActiveRef.current ? sharedVideoRef.current : null;
    }
    return localRef.current;
  }, [sharedVideoRef]);
  const [paused, setPaused] = useState(false);
  const [fastActive, setFastActive] = useState(false);

  // 视频缓冲状态
  const [isBuffering, setIsBufferingState] = useState(false);
  const isBufferingRef = useRef(false);
  // 是否已经被隐藏/拉黑
  const [isMarkedHidden, setIsMarkedHidden] = useState(false);
  const [isSharing, setIsSharing] = useState(false);
  const pendingShareURLRef = useRef("");

  useEffect(() => {
    pendingShareURLRef.current = "";
  }, [item.id]);

  // 进度状态。iOS 由实际呈现帧更新，其他平台由 timeupdate 更新；
  // 拖动期间则以用户输入为准。
  const [duration, setDuration] = useState(0);
  const [currentTime, setCurrentTime] = useState(0);
  const [scrubbing, setScrubbing] = useState(false);
  const scrubbingRef = useRef(false);
  const lastKeyboardSeekPreviewTimeRef = useRef<number | null>(null);

  useLayoutEffect(() => {
    if (keyboardSeekPreview) {
      lastKeyboardSeekPreviewTimeRef.current = keyboardSeekPreview.currentTime;
      return;
    }

    const lastPreviewTime = lastKeyboardSeekPreviewTimeRef.current;
    if (lastPreviewTime === null) return;
    lastKeyboardSeekPreviewTimeRef.current = null;
    // 松键提示消失时 seek 可能还在等待网络。先保留累计目标，避免底部进度条
    // 回跳到长按前的 timeupdate；之后由真实媒体时间自然接管。
    setCurrentTime(lastPreviewTime);
  }, [keyboardSeekPreview]);

  const clearBufferingIndicatorTimer = useCallback(() => {
    if (bufferingIndicatorTimerRef.current === null) return;
    window.clearTimeout(bufferingIndicatorTimerRef.current);
    bufferingIndicatorTimerRef.current = null;
  }, []);

  const setIsBuffering = useCallback(
    (next: boolean) => {
      if (!next) clearBufferingIndicatorTimer();
      if (next) playbackMotionFrameCountRef.current = 0;
      isBufferingRef.current = next;
      setIsBufferingState(next);
    },
    [clearBufferingIndicatorTimer]
  );

  const clearLoopRestartWatchdog = useCallback(() => {
    if (loopRestartTimerRef.current === null) return;
    window.clearTimeout(loopRestartTimerRef.current);
    loopRestartTimerRef.current = null;
  }, []);

  const resetLoopRestartState = useCallback(() => {
    clearLoopRestartWatchdog();
    loopRestartAttemptRef.current += 1;
    loopRestartPendingRef.current = false;
    loopRestartAwaitingFrameRef.current = false;
    loopRestartReloadedRef.current = false;
    loopFrameBarrierRef.current = null;
    lastObservedMediaTimeRef.current = null;
    lastPresentedMediaTimeRef.current = null;
    playbackMotionFrameCountRef.current = 0;
  }, [clearLoopRestartWatchdog]);

  const confirmPresentedPlayback = useCallback(
    (mediaTime?: number) => {
      clearLoopRestartWatchdog();
      loopRestartPendingRef.current = false;
      loopRestartAwaitingFrameRef.current = false;
      loopRestartReloadedRef.current = false;
      hasStartedPlayingRef.current = true;
      playbackMotionFrameCountRef.current = 0;
      setPaused(false);
      setIsBuffering(false);
      if (
        mediaTime !== undefined &&
        Number.isFinite(mediaTime) &&
        !scrubbingRef.current
      ) {
        setCurrentTime(mediaTime);
      }
    },
    [clearLoopRestartWatchdog, setIsBuffering]
  );

  useEffect(() => clearBufferingIndicatorTimer, [clearBufferingIndicatorTimer]);

  // 是否已点过赞。真正的防重在父组件 likedIdsRef 里，
  // 这里仅控制视觉态并依据父组件返回的回执处理失败回滚。
  const [isLiked, setIsLiked] = useState(false);
  // 屏幕中央的心形飞起动画（双击点赞时显示）
  const [heartBurst, setHeartBurst] = useState<{
    key: number;
    x: number;
    y: number;
  } | null>(null);

  // isLiked 取自父组件的全局集合，这样切走再切回 / 同一 id 重复出现仍能保持视觉态。
  useEffect(() => {
    setIsLiked(hasLiked(item.id));
  }, [item.id, hasLiked]);

  const setRef = useCallback(
    (el: HTMLVideoElement | null) => {
      const previous = localRef.current;
      if (!el && previous && !shouldMountRef.current) {
        releaseVideoSource(previous);
      }
      localRef.current = el;
      videoRef(el);
    },
    [videoRef]
  );

  // 非当前屏/后续预加载/视频窗口内缓存视频不保留媒体源，确保离开窗口后浏览器中止原始网盘流。
  useEffect(() => {
    if (usesSharedVideo) {
      if (!shouldLoad) {
        resetLoopRestartState();
        hasStartedPlayingRef.current = false;
        setDuration(0);
        setCurrentTime(0);
        setIsBuffering(false);
      }
      return;
    }
    if (shouldLoad) return;
    const video = localRef.current;
    if (!video) return;
    releaseVideoSource(video);
    hasStartedPlayingRef.current = false;
    setDuration(0);
    setCurrentTime(0);
    setIsBuffering(false);
  }, [item.id, resetLoopRestartState, shouldLoad, usesSharedVideo]);

  // 每次成为当前屏都明确发起播放。Safari 可能在 src/load
  // 切换时以 AbortError 中断第一次请求，因此在 canplay/loadeddata
  // 后会再试；NotAllowedError 则立即显示可点击的播放态。
  useEffect(() => {
    const video = getVideoElement();
    if (!video || !isActive || !shouldLoad) return;

    let disposed = false;
    let retryCount = 0;
    let retryTimer: number | null = null;

    const canContinue = () =>
      !disposed &&
      getVideoElement() === video &&
      isActiveRef.current &&
      shouldLoadRef.current &&
      (!usesSharedVideo || video.dataset.shortsVideoId === item.id) &&
      !isVideoPausedByUser(index);

    const markPlayBlocked = () => {
      if (!canContinue()) return;
      setIsBuffering(false);
      setPaused(true);
      onActiveNeedsPriority(index);
    };

    const attemptPlay = () => {
      if (!canContinue() || !video.paused) return;
      applyVideoMutedState(video, mutedRef.current);
      video.playsInline = true;
      try {
        video.defaultMuted = mutedRef.current;
      } catch {
        // ignore
      }

      setPaused(false);
      if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
        setIsBuffering(true);
      }

      let request: Promise<void> | undefined;
      try {
        request = video.play();
      } catch {
        markPlayBlocked();
        return;
      }

      request?.catch((error: unknown) => {
        if (!canContinue()) return;
        const errorName = getMediaErrorName(error);
        if (errorName === "AbortError" && retryCount < 2) {
          retryCount += 1;
          if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
            setIsBuffering(true);
          }
          retryTimer = window.setTimeout(attemptPlay, retryCount * 120);
          return;
        }
        markPlayBlocked();
      });
    };

    const retryWhenReady = () => {
      if (canContinue() && video.paused) attemptPlay();
    };

    video.addEventListener("loadeddata", retryWhenReady);
    video.addEventListener("canplay", retryWhenReady);
    if (isVideoPausedByUser(index)) {
      video.pause();
      setPaused(true);
      setIsBuffering(false);
    } else if (video.paused) {
      attemptPlay();
    } else {
      setPaused(false);
      setIsBuffering(video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA);
    }

    return () => {
      disposed = true;
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      video.removeEventListener("loadeddata", retryWhenReady);
      video.removeEventListener("canplay", retryWhenReady);
    };
  }, [
    getVideoElement,
    index,
    isActive,
    isVideoPausedByUser,
    item.id,
    onActiveNeedsPriority,
    shouldLoad,
    usesSharedVideo,
  ]);

  // iOS 不使用 WebKit 原生 loop：片尾后显式 seek 到 0，并等待下一轮首帧
  // 真正进入合成器。普通 seek 迟迟不出帧时，只对同一个持久 video 做一次
  // load() 自救；media element 不重建，因此不会丢失用户授予的有声播放权限。
  useEffect(() => {
    if (!usesSharedVideo || !isActive || !shouldLoad) return;
    const video = getVideoElement();
    if (!video || video.dataset.shortsVideoId !== item.id) return;

    let disposed = false;
    video.loop = false;
    resetLoopRestartState();
    const canObservePresentedFrames =
      typeof video.requestVideoFrameCallback === "function" &&
      typeof video.cancelVideoFrameCallback === "function";

    const belongsToCurrentSlide = () =>
      !disposed &&
      getVideoElement() === video &&
      isActiveRef.current &&
      shouldLoadRef.current &&
      video.dataset.shortsVideoId === item.id;

    const canContinueRestart = (attempt: number) =>
      belongsToCurrentSlide() &&
      loopRestartPendingRef.current &&
      loopRestartAttemptRef.current === attempt &&
      !scrubbingRef.current &&
      !isVideoPausedByUser(index);

    const failRestart = (attempt: number) => {
      if (!canContinueRestart(attempt)) return;
      try {
        video.pause();
      } catch {
        // ignore
      }
      resetLoopRestartState();
      setIsBuffering(false);
      setPaused(true);
      onActiveNeedsPriority(index);
    };

    const attemptRestart = (attempt: number) => {
      if (!canContinueRestart(attempt)) return;
      normalizeVideoPlaybackRate(video);
      applyVideoMutedState(video, mutedRef.current);

      let request: Promise<void> | undefined;
      try {
        request = video.play();
      } catch {
        failRestart(attempt);
        return;
      }
      request?.catch((error: unknown) => {
        if (!canContinueRestart(attempt)) return;
        // currentTime=0 / load() 都可能中断前一次 play。seeked/canplay
        // 会再次调用 attemptRestart，因此 AbortError 不应暴露成暂停态。
        if (getMediaErrorName(error) === "AbortError") return;
        failRestart(attempt);
      });
    };

    const startFrameWatchdog = (attempt: number) => {
      clearLoopRestartWatchdog();
      const timeoutMs = loopRestartReloadedRef.current
        ? IOS_LOOP_RELOAD_TIMEOUT_MS
        : IOS_LOOP_FRAME_WATCHDOG_MS;
      loopRestartTimerRef.current = window.setTimeout(() => {
        loopRestartTimerRef.current = null;
        if (!canContinueRestart(attempt) || !loopRestartAwaitingFrameRef.current) {
          return;
        }

        // 同一节点已经重建过一次播放管线仍没有任何呈现帧，就退出永久
        // buffering，展示可点击的暂停态，让用户可以主动重试。
        if (loopRestartReloadedRef.current) {
          failRestart(attempt);
          return;
        }

        loopRestartReloadedRef.current = true;
        loopFrameBarrierRef.current = null;
        try {
          video.pause();
          // 保留 src 和同一个 DOM 节点，只重建 WebKit 内部播放管线。
          video.load();
        } catch {
          failRestart(attempt);
          return;
        }
        attemptRestart(attempt);
        startFrameWatchdog(attempt);
      }, timeoutMs);
    };

    const handleIOSLoopEnded = () => {
      if (
        !belongsToCurrentSlide() ||
        scrubbingRef.current ||
        isVideoPausedByUser(index)
      ) {
        return;
      }

      // 已经在等上一轮的呈现帧却再次跑到 ended，说明只有媒体时钟在走，
      // 不能把它当成一次全新的循环并反复 load。
      if (loopRestartPendingRef.current) {
        failRestart(loopRestartAttemptRef.current);
        return;
      }

      clearLoopRestartWatchdog();
      const attempt = loopRestartAttemptRef.current + 1;
      loopRestartAttemptRef.current = attempt;
      loopRestartPendingRef.current = true;
      loopRestartAwaitingFrameRef.current = true;
      loopRestartReloadedRef.current = false;
      loopFrameBarrierRef.current = null;
      lastObservedMediaTimeRef.current = null;
      hasStartedPlayingRef.current = false;
      normalizeVideoPlaybackRate(video);
      setFastActive(false);
      setCurrentTime(0);
      setPaused(false);
      setIsBuffering(true);
      onActiveNeedsPriority(index);

      if (canObservePresentedFrames) {
        try {
          video.currentTime = 0;
        } catch {
          // 某些 WebKit readyState 下不能直接 seek，watchdog 会用 load() 恢复。
        }
      } else {
        // Safari 15.3 及更早没有可观察的呈现帧信号，无法可靠区分
        // “媒体时钟前进”和“画面已更新”。这类版本每轮直接重建同一节点
        // 的内部播放管线，避免再次走容易卡住的 backward seek。
        loopRestartReloadedRef.current = true;
        try {
          video.load();
        } catch {
          failRestart(attempt);
          return;
        }
      }
      startFrameWatchdog(attempt);
      attemptRestart(attempt);
    };

    const retryRestartWhenReady = () => {
      if (!loopRestartPendingRef.current) return;
      attemptRestart(loopRestartAttemptRef.current);
    };

    const handleRestartSeeked = () => {
      if (
        loopRestartPendingRef.current &&
        loopFrameBarrierRef.current === null
      ) {
        // presentationTime 与 performance.now() 使用同一个高精度时间轴。
        // 只有在本轮 seek 完成后提交给合成器的帧才可以结束重启态。
        loopFrameBarrierRef.current = performance.now();
      }
      retryRestartWhenReady();
    };

    const handleRestartCanPlay = () => {
      if (
        loopRestartPendingRef.current &&
        loopRestartReloadedRef.current &&
        loopFrameBarrierRef.current === null
      ) {
        // load() 会建立新的媒体时间线，不一定再发对应的 seeked。
        loopFrameBarrierRef.current = performance.now();
      }
      retryRestartWhenReady();
    };

    const handleIOSLoopPlay = () => {
      if (
        !loopRestartPendingRef.current ||
        !belongsToCurrentSlide() ||
        isVideoPausedByUser(index)
      ) {
        return;
      }
      setPaused(false);
      setIsBuffering(true);
      startFrameWatchdog(loopRestartAttemptRef.current);
    };

    video.addEventListener("ended", handleIOSLoopEnded);
    video.addEventListener("seeked", handleRestartSeeked);
    video.addEventListener("loadeddata", handleRestartCanPlay);
    video.addEventListener("canplay", handleRestartCanPlay);
    video.addEventListener("play", handleIOSLoopPlay);

    return () => {
      disposed = true;
      video.removeEventListener("ended", handleIOSLoopEnded);
      video.removeEventListener("seeked", handleRestartSeeked);
      video.removeEventListener("loadeddata", handleRestartCanPlay);
      video.removeEventListener("canplay", handleRestartCanPlay);
      video.removeEventListener("play", handleIOSLoopPlay);
      resetLoopRestartState();
    };
  }, [
    clearLoopRestartWatchdog,
    getVideoElement,
    index,
    isActive,
    isVideoPausedByUser,
    item.id,
    onActiveNeedsPriority,
    resetLoopRestartState,
    shouldLoad,
    usesSharedVideo,
  ]);

  // 离开活跃后清掉本地的暂停状态，避免回来时 UI 还显示着 paused
  useEffect(() => {
    if (!isActive) {
      if (usesSharedVideo) resetLoopRestartState();
      hasStartedPlayingRef.current = false;
      setPaused(false);
      setScrubbing(false);
      scrubbingRef.current = false;
      setIsBuffering(false);
    }
  }, [isActive, resetLoopRestartState, usesSharedVideo]);

  // 只同步静音；媒体音量保持浏览器默认值，由系统控制实际响度。
  useEffect(() => {
    const video = getVideoElement();
    if (video && isActive) {
      applyVideoMutedState(video, muted);
    }
  }, [getVideoElement, muted, isActive]);

  // 离开活跃或者被隐藏时暂停视频
  useEffect(() => {
    const video = getVideoElement();
    if (isMarkedHidden && video) {
      try {
        video.pause();
      } catch {
        // ignore
      }
    }
  }, [getVideoElement, isMarkedHidden]);

  // 监听 video 的时长 / 进度 / 缓冲状态 / 音量物理键变化。
  // VIDEO_WINDOW_SIZE 会让窗口外的 slide 先以海报占位，之后才挂载 video 壳；
  // 只有 shouldLoad=true 的当前屏/后续预加载/缓存窗口视频会绑定 src，因此不会一次拉完整队列。
  // 因此这里必须跟随 shouldMount 重新绑定，否则后续视频没有 timeupdate 事件。
  useEffect(() => {
    if (!shouldMount) {
      setDuration(0);
      setCurrentTime(0);
      setIsBuffering(false);
      return;
    }
    const video = getVideoElement();
    if (!video) return;
    const usesPresentedFrameProgress =
      usesSharedVideo &&
      typeof video.requestVideoFrameCallback === "function" &&
      typeof video.cancelVideoFrameCallback === "function";
    const belongsToSlide = () =>
      !usesSharedVideo ||
      (isActiveRef.current && video.dataset.shortsVideoId === item.id);
    const handleLoaded = () => {
      if (!belongsToSlide()) return;
      if (Number.isFinite(video.duration) && video.duration > 0) {
        setDuration(video.duration);
      } else {
        setDuration(0);
      }
      if (
        !usesPresentedFrameProgress &&
        !loopRestartPendingRef.current &&
        !video.seeking &&
        !scrubbingRef.current
      ) {
        setCurrentTime(video.currentTime || 0);
      }
    };
    const handleTime = () => {
      if (!belongsToSlide()) return;
      const mediaTime = video.currentTime || 0;
      const previousMediaTime = lastObservedMediaTimeRef.current;
      lastObservedMediaTimeRef.current = mediaTime;
      const mediaTimeAdvanced =
        previousMediaTime !== null &&
        (mediaTime > previousMediaTime + 0.001 ||
          (!usesSharedVideo && mediaTime + 0.25 < previousMediaTime));

      // iOS 上 currentTime/timeupdate 可先于真正绘制的视频帧前进。
      // 有 rVFC 时只让帧回调写进度；旧版浏览器则在非 seek/非循环
      // 重启阶段退化为媒体时钟，并用时间确实推进来自愈残留 spinner。
      if (
        !usesPresentedFrameProgress &&
        !loopRestartPendingRef.current &&
        !video.seeking &&
        !scrubbingRef.current
      ) {
        setCurrentTime(mediaTime);
      }
      if (
        !usesPresentedFrameProgress &&
        mediaTimeAdvanced &&
        !video.paused &&
        !video.ended &&
        !video.seeking &&
        !isVideoPausedByUser(index) &&
        (loopRestartPendingRef.current ||
          !hasStartedPlayingRef.current ||
          isBufferingRef.current)
      ) {
        confirmPresentedPlayback(mediaTime);
      }
      syncActivePreloadReadiness(video);
    };
    const handleWaiting = () => {
      if (!belongsToSlide()) return;
      if (video.paused || isVideoPausedByUser(index)) {
        setIsBuffering(false);
        return;
      }
      hasStartedPlayingRef.current = false;
      playbackMotionFrameCountRef.current = 0;
      if (isActive) onActiveNeedsPriority(index);
      if (
        !isBufferingRef.current &&
        bufferingIndicatorTimerRef.current === null
      ) {
        bufferingIndicatorTimerRef.current = window.setTimeout(() => {
          bufferingIndicatorTimerRef.current = null;
          if (
            !belongsToSlide() ||
            hasStartedPlayingRef.current ||
            video.paused ||
            video.ended ||
            isVideoPausedByUser(index)
          ) {
            return;
          }
          setIsBuffering(true);
        }, SHORTS_BUFFERING_INDICATOR_DELAY_MS);
      }
    };
    const cacheAvailableSource = () => {
      if (!belongsToSlide()) return;
      // 已经能解码播放，说明浏览器里有了值得复用的数据。
      if (shouldLoad) onSourceCached(item.id);
    };
    const handleCanPlay = () => {
      if (!belongsToSlide()) return;
      if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) return;
      cacheAvailableSource();
      if (isActive && isVideoPausedByUser(index)) {
        video.pause();
        setPaused(true);
        setIsBuffering(false);
        return;
      }
      // canplay 只代表已有可解码数据，不代表播放真正开始。
      // 激活播放 effect 会在这个事件后重试 play()；iOS spinner
      // 必须等实际帧/媒体时间继续推进后才能清掉。
      syncActivePreloadReadiness(video);
    };
    const handlePlaying = () => {
      if (!belongsToSlide()) return;
      if (video.paused) return;
      cacheAvailableSource();
      if (isActive && isVideoPausedByUser(index)) {
        video.pause();
        setPaused(true);
        setIsBuffering(false);
        return;
      }
      const waitForIOSPlaybackMotion = usesSharedVideo;
      if (isActive) {
        if (waitForIOSPlaybackMotion) {
          // iOS 的 playing 只表示媒体管线准备继续，并不保证画面已经推进。
          // spinner 必须等 rVFC/timeupdate 观察到新的媒体时间后才能清掉。
          setPaused(false);
        } else {
          confirmPresentedPlayback();
        }
      } else {
        setIsBuffering(false);
      }
      syncActivePreloadReadiness(video);
    };
    const handleProgress = () => {
      if (!belongsToSlide()) return;
      syncActivePreloadReadiness(video);
      // 窗口内视频只要已经产生缓冲，就标记为可复用；
      // 之后预加载授权被收回时不再丢弃它的 src 和已缓冲数据。
      if (shouldLoad && videoHasBufferedData(video)) {
        onSourceCached(item.id);
      }
    };
    const handlePlay = () => {
      if (!belongsToSlide()) return;
      if (!isActive) return;
      if (isVideoPausedByUser(index)) {
        video.pause();
        setPaused(true);
        setIsBuffering(false);
        return;
      }
      setPaused(false);
      if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
        hasStartedPlayingRef.current = false;
        setIsBuffering(true);
      }
    };
    const handlePause = () => {
      if (!belongsToSlide()) return;
      if (loopRestartPendingRef.current) {
        // watchdog 的 pause()+load() 不应把自救流程显示成用户暂停；但用户
        // 在循环重启期间主动暂停时，仍要立刻隐藏 spinner 并显示播放按钮。
        if (isVideoPausedByUser(index)) {
          clearLoopRestartWatchdog();
          hasStartedPlayingRef.current = false;
          setPaused(true);
          setIsBuffering(false);
        }
        return;
      }
      hasStartedPlayingRef.current = false;
      if (!isActive || video.ended) return;
      setPaused(true);
      setIsBuffering(false);
      onActiveNeedsPriority(index);
    };
    const handleLoadStart = () => {
      if (!belongsToSlide()) return;
      if (!isActive || isVideoPausedByUser(index)) return;
      hasStartedPlayingRef.current = false;
      setPaused(false);
      setIsBuffering(true);
      onActiveNeedsPriority(index);
    };
    const handleStalled = () => {
      if (!belongsToSlide()) return;
      if (!isActive || video.paused || isVideoPausedByUser(index)) return;
      // stalled 只表示暂时没有新的网络数据到达；已有缓冲足够时画面仍会
      // 正常播放，也不会再发 playing。不能据此永久展示 spinner。
      onActiveNeedsPriority(index);
    };
    const handleError = () => {
      if (!belongsToSlide()) return;
      if (usesSharedVideo && !video.error) return;
      if (!isActive) return;
      if (usesSharedVideo) resetLoopRestartState();
      hasStartedPlayingRef.current = false;
      setIsBuffering(false);
      setPaused(true);
      onActiveNeedsPriority(index);
    };

    function syncActivePreloadReadiness(currentVideo: HTMLVideoElement) {
      if (!isActive) return;
      // 有缓冲不等于已经播放。iOS 上预加载视频可能停在首帧，
      // 此时继续给后两条绑定 src 会加重媒体资源竞争。
      if (
        currentVideo.paused ||
        currentVideo.ended ||
        !hasStartedPlayingRef.current ||
        isVideoPausedByUser(index)
      ) {
        onActiveNeedsPriority(index);
        return;
      }
      if (videoHasComfortableBuffer(currentVideo)) {
        onActiveReadyForPreload(index);
      } else if (videoBufferIsCritical(currentVideo)) {
        // 高低水位滞回：只有缓冲真正告急才收回预加载授权，
        // 在两个水位之间维持现状，避免阈值附近来回抖动。
        onActiveNeedsPriority(index);
      }
    }

    handleLoaded();
    handleTime();
    video.addEventListener("loadedmetadata", handleLoaded);
    video.addEventListener("durationchange", handleLoaded);
    video.addEventListener("timeupdate", handleTime);
    video.addEventListener("waiting", handleWaiting);
    video.addEventListener("playing", handlePlaying);
    video.addEventListener("canplay", handleCanPlay);
    video.addEventListener("progress", handleProgress);
    video.addEventListener("play", handlePlay);
    video.addEventListener("pause", handlePause);
    video.addEventListener("loadstart", handleLoadStart);
    video.addEventListener("stalled", handleStalled);
    video.addEventListener("error", handleError);

    // 挂载时如果已经在播放但是状态不到 ready 则置 buffering
    if (video.readyState < 3 && !video.paused) {
      setIsBuffering(true);
    }

    return () => {
      video.removeEventListener("loadedmetadata", handleLoaded);
      video.removeEventListener("durationchange", handleLoaded);
      video.removeEventListener("timeupdate", handleTime);
      video.removeEventListener("waiting", handleWaiting);
      video.removeEventListener("playing", handlePlaying);
      video.removeEventListener("canplay", handleCanPlay);
      video.removeEventListener("progress", handleProgress);
      video.removeEventListener("play", handlePlay);
      video.removeEventListener("pause", handlePause);
      video.removeEventListener("loadstart", handleLoadStart);
      video.removeEventListener("stalled", handleStalled);
      video.removeEventListener("error", handleError);
    };
  }, [
    clearBufferingIndicatorTimer,
    clearLoopRestartWatchdog,
    confirmPresentedPlayback,
    getVideoElement,
    index,
    isActive,
    isVideoPausedByUser,
    item.id,
    onActiveNeedsPriority,
    onActiveReadyForPreload,
    onSourceCached,
    resetLoopRestartState,
    setIsBuffering,
    shouldLoad,
    shouldMount,
    usesSharedVideo,
  ]);

  // Safari 15.4+ 可以在视频帧真正送到合成器时回调。iOS 共享 video 的
  // 进度以这里的 mediaTime 为准，而不是可能先行的 currentTime；同一个
  // 信号也负责在 waiting/loadstart 恢复后清掉过期的缓冲状态。
  useEffect(() => {
    if (!usesSharedVideo || !isActive || !shouldLoad || !shouldMount) return;
    const video = getVideoElement();
    if (
      !video ||
      video.dataset.shortsVideoId !== item.id ||
      typeof video.requestVideoFrameCallback !== "function" ||
      typeof video.cancelVideoFrameCallback !== "function"
    ) {
      return;
    }

    let disposed = false;
    let frameCallbackId: number | null = null;
    let lastProgressUpdateAt = -Infinity;

    const belongsToSlide = () =>
      !disposed &&
      getVideoElement() === video &&
      isActiveRef.current &&
      shouldLoadRef.current &&
      video.dataset.shortsVideoId === item.id;

    const requestNextFrame = () => {
      if (!belongsToSlide()) return;
      frameCallbackId = video.requestVideoFrameCallback(handlePresentedFrame);
    };

    const handlePresentedFrame: VideoFrameRequestCallback = (
      now,
      metadata
    ) => {
      frameCallbackId = null;
      if (!belongsToSlide()) return;

      const mediaTime = metadata.mediaTime;
      if (loopRestartPendingRef.current) {
        const frameBarrier = loopFrameBarrierRef.current;
        const isNewLoopFrame =
          frameBarrier !== null && metadata.presentationTime >= frameBarrier;
        // ended 后可能还收到上一轮末帧的迟到回调；它既不能推动进度，
        // 也不能提前撤掉 spinner。
        if (!isNewLoopFrame) {
          requestNextFrame();
          return;
        }
      }

      const previousPresentedMediaTime = lastPresentedMediaTimeRef.current;
      const presentedFrameAdvanced =
        previousPresentedMediaTime !== null &&
        Math.abs(mediaTime - previousPresentedMediaTime) > 0.001;
      lastPresentedMediaTimeRef.current = mediaTime;

      const canConfirmPlaybackMotion =
        presentedFrameAdvanced &&
        !video.paused &&
        !video.ended &&
        !video.seeking &&
        !scrubbingRef.current &&
        !isVideoPausedByUser(index);
      const playbackNeedsMotionConfirmation =
        loopRestartPendingRef.current ||
        !hasStartedPlayingRef.current ||
        isBufferingRef.current;

      if (playbackNeedsMotionConfirmation) {
        if (canConfirmPlaybackMotion) {
          playbackMotionFrameCountRef.current += 1;
        }
        if (playbackMotionFrameCountRef.current >= 2) {
          lastProgressUpdateAt = now;
          confirmPresentedPlayback(mediaTime);
          requestNextFrame();
          return;
        }
      } else {
        playbackMotionFrameCountRef.current = 0;
      }

      const shouldCommitProgress =
        now - lastProgressUpdateAt >= 100;
      if (shouldCommitProgress) {
        lastProgressUpdateAt = now;
        if (!scrubbingRef.current) {
          setCurrentTime(mediaTime);
        }
      }

      requestNextFrame();
    };

    requestNextFrame();
    return () => {
      disposed = true;
      if (frameCallbackId !== null) {
        video.cancelVideoFrameCallback(frameCallbackId);
      }
    };
  }, [
    confirmPresentedPlayback,
    getVideoElement,
    index,
    isActive,
    isVideoPausedByUser,
    item.id,
    shouldLoad,
    shouldMount,
    usesSharedVideo,
  ]);

  function togglePlayInternal() {
    const video = getVideoElement();
    if (!video) return;
    const shouldResume =
      isVideoPausedByUser(index) || (video.paused && !isBuffering);
    if (shouldResume) {
      onUserPausedChange(index, false);
      setPaused(false);
      if (video.readyState < 3) setIsBuffering(true);
      video.play().catch(() => {
        if (getVideoElement() !== video || !isActiveRef.current) return;
        setPaused(true);
        setIsBuffering(false);
      });
    } else {
      onUserPausedChange(index, true);
      video.pause();
      setPaused(true);
      setIsBuffering(false);
    }
  }

  // Safari 的有声播放权限按 media element 授予。自动播放被拒后，用户的
  // 首次点击必须在原始 click 回调内直接 play()；分发时序见 useShortsSlideGestures。
  function shouldResumeImmediatelyOnClick() {
    const video = getVideoElement();
    return Boolean(video?.paused && !isBuffering);
  }

  function handleImmediateResume() {
    const video = getVideoElement();
    if (!video) return;
    onUserPausedChange(index, false);
    setPaused(false);
    if (video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
      setIsBuffering(true);
    }
    video.play().catch(() => {
      if (getVideoElement() !== video || !isActiveRef.current) return;
      setPaused(true);
      setIsBuffering(false);
    });
  }

  // 手势输入：长按倍速、横滑快进、单/双击分发、进度条拖动
  const {
    handleSlideClick,
    handleProgressPointerDown,
    handleProgressPointerMove,
    handleProgressPointerEnd,
  } = useShortsSlideGestures({
    getVideoElement,
    shouldMount,
    disabled: isMarkedHidden,
    scrubbingRef,
    setScrubbing,
    setFastActive,
    setCurrentTime,
    getSeekDuration,
    onSingleTap: togglePlayInternal,
    onDoubleTap: handleDoubleClickLike,
    shouldResumeImmediately: shouldResumeImmediatelyOnClick,
    onImmediateResume: handleImmediateResume,
  });

  function handleDoubleClickLike(x: number, y: number) {
    // 触发飞心动画（每次都给一个新 key 强制重启动画）
    setHeartBurst({ key: Date.now(), x, y });
    window.setTimeout(() => setHeartBurst(null), 700);

    // 双击只表达喜爱：已经点赞了就只播动画不取消，不重复发请求；
    // 真要取消请点右下角心形按钮
    if (isLiked) return;
    setIsLiked(true);
    void onLikeToggle(item.id, true).then((serverLikes) => {
      if (serverLikes === null) {
        // 请求失败：回滚视觉态
        setIsLiked(false);
      }
    });
  }

  keyboardLikeHandlerRef.current = () => {
    if (!isActiveRef.current || isMarkedHidden) return;
    const slideRect = slideRef.current?.getBoundingClientRect();
    if (!slideRect) return;
    handleDoubleClickLike(slideRect.width / 2, slideRect.height / 2);
  };

  useEffect(() => {
    const handleKeyboardLike = () => keyboardLikeHandlerRef.current();
    registerKeyboardLikeHandler(index, handleKeyboardLike);
    return () => registerKeyboardLikeHandler(index, null);
  }, [index, registerKeyboardLikeHandler]);

  /**
   * 点击右下角心形按钮：在"已点赞 / 未点赞"之间切换。
   */
  function handleHeartClick(e: React.MouseEvent<HTMLButtonElement>) {
    e.stopPropagation();
    const willLike = !isLiked;
    if (willLike) {
      // 视觉立即响应 + 飞心动画（让按钮位置发出心形）
      const slideRect = (
        e.currentTarget.closest(".shorts-slide") as HTMLElement | null
      )?.getBoundingClientRect();
      const btnRect = e.currentTarget.getBoundingClientRect();
      if (slideRect) {
        const x = btnRect.left + btnRect.width / 2 - slideRect.left;
        const y = btnRect.top + btnRect.height / 2 - slideRect.top;
        setHeartBurst({ key: Date.now(), x, y });
        window.setTimeout(() => setHeartBurst(null), 700);
      }
      setIsLiked(true);
      void onLikeToggle(item.id, true).then((serverLikes) => {
        if (serverLikes === null) {
          setIsLiked(false);
        }
      });
    } else {
      // 取消点赞：视觉立即响应，请求失败再回滚
      setIsLiked(false);
      void onLikeToggle(item.id, false).then((serverLikes) => {
        if (serverLikes === null) {
          setIsLiked(true);
        }
      });
    }
  }

  /** 创建并复制与详情页相同的一次性分享链接。 */
  async function handleShareClick(e: React.MouseEvent<HTMLButtonElement>) {
    e.stopPropagation();
    if (isSharing) return;
    setIsSharing(true);
    try {
      if (pendingShareURLRef.current) {
        await copyExistingVideoShareURL(pendingShareURLRef.current);
      } else {
        const result = await createAndCopyVideoShare(item.id);
        if (!result.copied) {
          pendingShareURLRef.current = result.url;
          showHud("请再次点击分享按钮");
          return;
        }
      }
      pendingShareURLRef.current = "";
      showHud("一次性分享链接已复制");
    } catch {
      showHud(
        pendingShareURLRef.current ? "复制失败，请重试" : "分享失败，请重试",
        <AlertCircle size={16} />
      );
    } finally {
      setIsSharing(false);
    }
  }



  /**
   * 拉黑并隐藏视频
   */
  function handleHideClick(e: React.MouseEvent<HTMLButtonElement>) {
    e.stopPropagation();
    setIsMarkedHidden(true);
    void hideVideo(item.id)
      .then((res) => {
        if (res.ok) {
          onHideSuccess(index);
        } else {
          setIsMarkedHidden(false);
          showHud("操作失败，请重试", <AlertCircle size={16} />);
        }
      })
      .catch(() => {
        setIsMarkedHidden(false);
        showHud("网络请求出错", <AlertCircle size={16} />);
      });
  }

  function getSeekDuration(video: HTMLVideoElement | null) {
    if (duration > 0) return duration;
    if (video && Number.isFinite(video.duration) && video.duration > 0) {
      setDuration(video.duration);
      return video.duration;
    }
    return 0;
  }

  const progressCurrentTime = keyboardSeekPreview?.currentTime ?? currentTime;
  const progressDuration = keyboardSeekPreview?.duration ?? duration;
  const progressRatio = progressDuration > 0
    ? clamp(progressCurrentTime / progressDuration, 0, 1)
    : 0;

  return (
    <article
      ref={slideRef}
      className="shorts-slide"
      data-shorts-slide=""
      data-index={index}
      data-active={isActive}
      onClick={handleSlideClick}
    >
      {/* 模糊海报背景：避免横屏视频两边出现刺眼黑边 */}
      <div
        className="shorts-slide__bg"
        style={{ backgroundImage: `url(${item.poster})` }}
        aria-hidden="true"
      />

      {sharedVideoSlotRef && (
        <div
          ref={sharedVideoSlotRef}
          className="shorts-slide__ios-video-slot"
        />
      )}

      {!usesSharedVideo && shouldMount ? (
        <video
          ref={setRef}
          className="shorts-slide__video"
          src={shouldLoad ? item.videoSrc : undefined}
          poster={item.poster}
          preload={shouldLoad ? (shouldEagerLoad ? "auto" : "metadata") : "none"}
          autoPlay={isActive}
          playsInline
          loop
          muted={muted}
          controlsList="nodownload"
          disablePictureInPicture
          onContextMenu={(e) => e.preventDefault()}
        />
      ) : (
        <img
          className="shorts-slide__poster"
          src={item.poster}
          alt=""
          aria-hidden="true"
          loading="lazy"
        />
      )}

      {fastActive && (
        <div className="shorts-slide__rate-hint" aria-hidden="true">
          2x 速播放中
        </div>
      )}



      {paused && isActive && !scrubbing && (
        <div className="shorts-slide__paused" aria-hidden="true">
          <span className="shorts-slide__paused-icon">
            <Play size={22} fill="currentColor" strokeWidth={1.75} />
          </span>
        </div>
      )}

      {/* 视频加载/缓冲旋转器 */}
      {isBuffering && isActive && shouldLoad && !isMarkedHidden && (
        <div className="shorts-slide__buffering" aria-hidden="true">
          <ShortsLoadingSpinner size={30} />
        </div>
      )}

      {/* 不再展示屏蔽遮罩 */}
      {isMarkedHidden && (
        <div className="shorts-slide__hidden-overlay" onClick={(e) => e.stopPropagation()}>
          <EyeOff size={38} style={{ color: "#ff4060" }} />
          <div className="shorts-slide__hidden-title">已隐藏该视频</div>
        </div>
      )}

      <div className="shorts-slide__overlay" onClick={(e) => e.stopPropagation()}>
        <h2 className="shorts-slide__title">{item.title}</h2>
        <div className="shorts-slide__meta">
          {item.sourceLabel && (
            <span className="shorts-slide__meta-item">{item.sourceLabel}</span>
          )}
          {item.duration && (
            <span className="shorts-slide__meta-item">{item.duration}</span>
          )}
          {item.tags && item.tags.length > 0 && (
            <span className="shorts-slide__meta-item">
              {item.tags.slice(0, 3).map((t) => `#${t}`).join(" ")}
            </span>
          )}
        </div>
        <Link
          to={`/video/${encodeURIComponent(item.id)}`}
          className="shorts-slide__detail"
        >
          <Info size={13} />
          <span>查看详情</span>
        </Link>
      </div>

      {/* 右下角操作栏 */}
      <aside
        className="shorts-slide__actions"
        onClick={(e) => e.stopPropagation()}
      >
        {/* 云盘来源徽章 */}
        <div className="shorts-drive-badge" title={`来源: ${item.sourceLabel || "本地"}`}>
          {getDriveShortName(item.sourceLabel || "本地")}
        </div>

        {/* 点赞 */}
        <button
          type="button"
          data-shorts-like=""
          className={`shorts-slide__action ${isLiked ? "is-liked" : ""}`}
          aria-label={isLiked ? "取消点赞" : "点赞"}
          aria-pressed={isLiked}
          onClick={handleHeartClick}
        >
          <Heart
            size={24}
            fill={isLiked ? "currentColor" : "none"}
            strokeWidth={2}
          />
        </button>

        {/* 一次性分享 */}
        <button
          type="button"
          className="shorts-slide__action"
          aria-label="生成并复制一次性分享链接"
          aria-busy={isSharing}
          disabled={isSharing}
          onClick={handleShareClick}
        >
          <Share2 size={22} />
        </button>


        {canHide && (
          <button
            type="button"
            className="shorts-slide__action"
            aria-label="不再展示"
            onClick={handleHideClick}
          >
            <EyeOff size={22} />
          </button>
        )}
      </aside>

      {/* 双击点赞时弹起的心形动画 */}
      {heartBurst && (
        <div
          key={heartBurst.key}
          className="shorts-slide__heart-burst"
          style={{ left: heartBurst.x, top: heartBurst.y }}
          aria-hidden="true"
        >
          <Heart size={88} fill="currentColor" strokeWidth={0} />
        </div>
      )}

      {/* 移动端左右滑动 / 拖动进度时的时间提示。独立于底部进度条，
          这样可以在触屏设备上放到页面顶部且不受底部容器定位限制。 */}
      {scrubbing && isActive && shouldLoad && !isMarkedHidden && (
        <div className="shorts-slide__progress-time" aria-live="polite">
          {formatClock(currentTime)} / {formatClock(duration)}
        </div>
      )}

      {/* 进度条 */}
      {isActive && shouldLoad && !isMarkedHidden && (
        <div
          className={`shorts-slide__progress ${
            scrubbing ? "is-scrubbing" : ""
          }`}
          onPointerDown={handleProgressPointerDown}
          onPointerMove={handleProgressPointerMove}
          onPointerUp={handleProgressPointerEnd}
          onPointerCancel={handleProgressPointerEnd}
          onLostPointerCapture={handleProgressPointerEnd}
          onClick={(e) => e.stopPropagation()}
        >
          <div
            className="shorts-slide__progress-track"
            style={{
              "--progress-pct": `${progressRatio * 100}%`,
            } as React.CSSProperties}
          >
            <div
              className="shorts-slide__progress-fill"
              style={{ width: `${progressRatio * 100}%` }}
            />
          </div>
        </div>
      )}
    </article>
  );
}

function ShortsLoadingSpinner({ size }: { size: number }) {
  const ref = useRef<HTMLSpanElement | null>(null);

  useEffect(() => {
    let frame = 0;
    const startedAt = performance.now();
    const tick = (now: number) => {
      const spinner = ref.current;
      if (spinner) {
        const rotation = ((now - startedAt) / 800) * 360;
        spinner.style.transform = `rotate(${rotation}deg)`;
      }
      frame = window.requestAnimationFrame(tick);
    };
    frame = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(frame);
  }, []);

  return (
    <span
      ref={ref}
      className="shorts-slide__loading-spinner"
      style={{
        "--shorts-spinner-size": `${size}px`,
      } as React.CSSProperties}
      aria-hidden="true"
    />
  );
}

function applyVideoMutedState(video: HTMLVideoElement, nextMuted: boolean) {
  try {
    if (video.muted !== nextMuted) {
      video.muted = nextMuted;
    }
  } catch {
    // ignore
  }
}

function releaseVideoSource(video: HTMLVideoElement) {
  try {
    video.pause();
    video.removeAttribute("src");
    video.load();
  } catch {
    // ignore
  }
}

function getMediaErrorName(error: unknown) {
  if (
    typeof error === "object" &&
    error !== null &&
    "name" in error &&
    typeof error.name === "string"
  ) {
    return error.name;
  }
  return "UnknownError";
}

function normalizeVideoPlaybackRate(video: HTMLVideoElement) {
  try {
    if (video.defaultPlaybackRate !== 1) {
      video.defaultPlaybackRate = 1;
    }
    if (video.playbackRate !== 1) {
      video.playbackRate = 1;
    }
  } catch {
    // ignore
  }
}

function stabilizeVideoAfterAudioToggle(
  video: HTMLVideoElement,
  shouldResume: () => boolean
) {
  const stabilize = () => {
    normalizeVideoPlaybackRate(video);
    if (shouldResume() && video.paused && !video.ended) {
      video.play().catch(() => undefined);
    }
  };

  stabilize();
  for (const delay of [80, 240, 600]) {
    window.setTimeout(stabilize, delay);
  }
}

type WebkitFullscreenDocument = Document & {
  webkitFullscreenElement?: Element | null;
  webkitExitFullscreen?: () => Promise<void> | void;
};

function exitDocumentFullscreen(): Promise<void> | null {
  if (typeof document === "undefined") return null;
  const fullscreenDocument = document as WebkitFullscreenDocument;
  const fullscreenElement =
    fullscreenDocument.fullscreenElement ??
    fullscreenDocument.webkitFullscreenElement;
  const exitFullscreen =
    fullscreenDocument.exitFullscreen?.bind(fullscreenDocument) ??
    fullscreenDocument.webkitExitFullscreen?.bind(fullscreenDocument);
  if (!fullscreenElement || !exitFullscreen) return null;

  try {
    return Promise.resolve(exitFullscreen());
  } catch (error) {
    return Promise.reject(error);
  }
}

function preventMediaContextMenu(event: Event) {
  event.preventDefault();
}

function formatClock(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) return "00:00";
  const total = Math.floor(seconds);
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

/** 识别云盘缩写名称 */
function getDriveShortName(source: string): string {
  const s = source.toLowerCase();
  if (s.includes("115")) return "115";
  if (s.includes("123")) return "123";
  if (s.includes("pikpak")) return "PikP";
  if (s.includes("quark") || s.includes("夸克")) return "Quak";
  if (s.includes("onedrive")) return "OneDrive";
  if (s.includes("wopan") || s.includes("沃盘")) return "沃盘";
  if (s.includes("guangyapan") || s.includes("guangya") || s.includes("光鸭")) return "光鸭";
  if (s.includes("webdav") || s.includes("web dav")) return "WebDAV";
  if (s.includes("localstorage") || s.includes("本地")) return "本地";
  if (s.includes("spider") || s.includes("爬虫")) return "爬虫";
  return source.substring(0, 4);
}
