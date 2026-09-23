import { useEffect, useRef } from "react";
import { clamp } from "./mediaBuffer";
import { createShortsSurfaceGestures } from "./slideGestures";

// 视觉进度按帧更新，媒体管线最多约 12.5Hz seek，松手再精确提交。
export const SHORTS_MEDIA_SEEK_INTERVAL_MS = 80;

export type ShortsSlideGesturesOptions = {
  getVideoElement: () => HTMLVideoElement | null;
  surfaceRef: React.RefObject<HTMLElement>;
  shouldMount: boolean;
  /** 非活跃视频、隐藏遮罩、播放失败等状态下不响应媒体手势 */
  disabled: boolean;
  /** 拖动中标记；媒体事件回调靠它忽略拖动期间的进度更新 */
  scrubbingRef: React.MutableRefObject<boolean>;
  setScrubbing: (scrubbing: boolean) => void;
  setFastActive: (fast: boolean) => void;
  setCurrentTime: (time: number) => void;
  getSeekDuration: (video: HTMLVideoElement | null) => number;
  /** 单击（240ms 内无第二次按下）：切换播放/暂停 */
  onSingleTap: () => void;
  /** 双击：点赞（相对 slide 左上角的坐标） */
  onDoubleTap: (x: number, y: number) => void;
  /** 自动播放被拒后的首次点击必须在原始抬手回调内直接恢复播放 */
  shouldResumeImmediately: () => boolean;
  onImmediateResume: () => void;
  onClearScreenChange: (clear: boolean) => void;
};

/**
 * 一屏短视频的手势输入：
 * - 长按 ≥400ms 进入 2 倍速，松手恢复（与详情页 VideoPlayer 行为一致）
 * - 横向滑动按当前播放点相对快进 / 快退，纵向滑动仍用于切换上下视频
 * - 单击切换播放 / 暂停，双击点赞；浏览器补发的 click 不重复执行手势
 * - 底部进度条按 pointer capture 拖动
 * - 双指扩张清屏、捏合恢复，画面跟手缩放后回弹
 */
export function useShortsSlideGestures(options: ShortsSlideGesturesOptions) {
  // 拖动开始时是否在播：用于拖完后判断要不要 resume
  const wasPlayingRef = useRef(true);
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const previewFrameRef = useRef<number | null>(null);
  const pendingPreviewTimeRef = useRef<number | null>(null);
  const mediaSeekTimerRef = useRef<number | null>(null);
  const pendingMediaSeekRef = useRef<{
    video: HTMLVideoElement;
    time: number;
  } | null>(null);
  const lastMediaSeekAtRef = useRef(-Infinity);
  const progressTargetTimeRef = useRef<number | null>(null);

  function writeMediaSeek(
    video: HTMLVideoElement,
    time: number,
    exact: boolean
  ) {
    try {
      if (!exact && typeof video.fastSeek === "function") {
        video.fastSeek(time);
      } else {
        video.currentTime = time;
      }
      lastMediaSeekAtRef.current = performance.now();
    } catch {
      // 部分 ready state 下 seek 会抛错；后续 move / 最终提交仍会重试。
    }
  }

  function scheduleProgressPreview(time: number) {
    pendingPreviewTimeRef.current = time;
    if (previewFrameRef.current !== null) return;
    previewFrameRef.current = window.requestAnimationFrame(() => {
      previewFrameRef.current = null;
      const pending = pendingPreviewTimeRef.current;
      pendingPreviewTimeRef.current = null;
      if (pending !== null) optionsRef.current.setCurrentTime(pending);
    });
  }

  function flushProgressPreview(time: number) {
    if (previewFrameRef.current !== null) {
      window.cancelAnimationFrame(previewFrameRef.current);
      previewFrameRef.current = null;
    }
    pendingPreviewTimeRef.current = null;
    optionsRef.current.setCurrentTime(time);
  }

  function scheduleMediaSeek(video: HTMLVideoElement, time: number) {
    pendingMediaSeekRef.current = { video, time };
    if (mediaSeekTimerRef.current !== null) return;

    const elapsed = performance.now() - lastMediaSeekAtRef.current;
    const delay = Math.max(0, SHORTS_MEDIA_SEEK_INTERVAL_MS - elapsed);
    if (delay === 0) {
      pendingMediaSeekRef.current = null;
      writeMediaSeek(video, time, false);
      return;
    }

    mediaSeekTimerRef.current = window.setTimeout(() => {
      mediaSeekTimerRef.current = null;
      const pending = pendingMediaSeekRef.current;
      pendingMediaSeekRef.current = null;
      if (pending) writeMediaSeek(pending.video, pending.time, false);
    }, delay);
  }

  function flushMediaSeek(video: HTMLVideoElement, time: number) {
    if (mediaSeekTimerRef.current !== null) {
      window.clearTimeout(mediaSeekTimerRef.current);
      mediaSeekTimerRef.current = null;
    }
    pendingMediaSeekRef.current = null;
    writeMediaSeek(video, time, true);
  }

  function cancelScheduledSeeks() {
    if (previewFrameRef.current !== null) {
      window.cancelAnimationFrame(previewFrameRef.current);
      previewFrameRef.current = null;
    }
    pendingPreviewTimeRef.current = null;
    if (mediaSeekTimerRef.current !== null) {
      window.clearTimeout(mediaSeekTimerRef.current);
      mediaSeekTimerRef.current = null;
    }
    pendingMediaSeekRef.current = null;
    progressTargetTimeRef.current = null;
  }

  // 整个 slide 共用一个输入入口。iOS 的共享 video 换屏时也随活跃状态重绑，
  // 不让后台 slide 的监听器或延迟单击操作当前正在播放的元素。
  useEffect(() => {
    if (options.disabled) return;
    const surface = options.surfaceRef.current;
    const video = options.getVideoElement();
    if (!surface || !video) return;
    let pinchReturnTimer: number | null = null;
    const clearPinchFeedback = () => {
      if (pinchReturnTimer !== null) window.clearTimeout(pinchReturnTimer);
      pinchReturnTimer = null;
      surface.removeAttribute("data-pinch");
      surface.style.removeProperty("--shorts-pinch-scale");
    };
    const destroy = createShortsSurfaceGestures({
      surface,
      video,
      isEnabled: () => !optionsRef.current.disabled,
      onSingleTap: () => optionsRef.current.onSingleTap(),
      onDoubleTap: (x, y) => optionsRef.current.onDoubleTap(x, y),
      shouldResumeImmediately: () => optionsRef.current.shouldResumeImmediately(),
      onImmediateResume: () => optionsRef.current.onImmediateResume(),
      onClearScreenChange: (clear) => optionsRef.current.onClearScreenChange(clear),
      onPinchScale: (scale) => {
        if (pinchReturnTimer !== null) window.clearTimeout(pinchReturnTimer);
        pinchReturnTimer = null;
        surface.dataset.pinch = scale === null ? "returning" : "tracking";
        surface.style.setProperty("--shorts-pinch-scale", String(scale ?? 1));
        // 动画结束后移除 transform；iOS 共享 video 换屏时也不会带走缩放。
        if (scale === null) pinchReturnTimer = window.setTimeout(clearPinchFeedback, 220);
      },
      onFastChange: (fast) => optionsRef.current.setFastActive(fast),
      getSeekDuration: () => optionsRef.current.getSeekDuration(video),
      onSeekStart: () => {
        optionsRef.current.scrubbingRef.current = true;
        optionsRef.current.setScrubbing(true);
      },
      onSeekPreview: (time) => {
        scheduleProgressPreview(time);
        scheduleMediaSeek(video, time);
      },
      onSeekEnd: (time) => {
        // iOS 共享元素可能已经交给下一屏，不能把旧手势的落点写入新视频。
        if (!optionsRef.current.disabled && optionsRef.current.getVideoElement() === video) {
          flushProgressPreview(time);
          flushMediaSeek(video, time);
        }
        optionsRef.current.scrubbingRef.current = false;
        optionsRef.current.setScrubbing(false);
      },
    });
    return () => {
      destroy();
      clearPinchFeedback();
      cancelScheduledSeeks();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [options.getVideoElement, options.surfaceRef, options.shouldMount, options.disabled]);

  useEffect(() => () => cancelScheduledSeeks(), []);

  // ---- 进度条拖动 ----
  // 触摸进度条时：暂停 → 跟随手指更新 currentTime → 松手 resume
  function handleProgressPointerDown(e: React.PointerEvent<HTMLDivElement>) {
    e.preventDefault();
    e.stopPropagation();
    if (options.disabled) return;
    const video = options.getVideoElement();
    const seekDuration = options.getSeekDuration(video);
    if (!video || !seekDuration) return;
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      // ignore
    }
    wasPlayingRef.current = !video.paused;
    if (!video.paused) {
      try {
        video.pause();
      } catch {
        // ignore
      }
    }
    options.scrubbingRef.current = true;
    options.setScrubbing(true);
    progressTargetTimeRef.current = null;
    applyProgressFromEvent(e, seekDuration);
  }
  function handleProgressPointerMove(e: React.PointerEvent<HTMLDivElement>) {
    if (!options.scrubbingRef.current) return;
    e.preventDefault();
    e.stopPropagation();
    applyProgressFromEvent(e);
  }
  function handleProgressPointerEnd(e: React.PointerEvent<HTMLDivElement>) {
    if (!options.scrubbingRef.current) return;
    e.preventDefault();
    e.stopPropagation();
    try {
      (e.currentTarget as HTMLElement).releasePointerCapture(e.pointerId);
    } catch {
      // ignore
    }
    const video = options.getVideoElement();
    const targetTime = progressTargetTimeRef.current;
    if (video && targetTime !== null) {
      flushProgressPreview(targetTime);
      flushMediaSeek(video, targetTime);
    }
    progressTargetTimeRef.current = null;
    options.scrubbingRef.current = false;
    options.setScrubbing(false);
    if (video && wasPlayingRef.current) {
      video.play().catch(() => undefined);
    }
  }
  function applyProgressFromEvent(
    e: React.PointerEvent<HTMLDivElement>,
    knownDuration?: number
  ) {
    const video = options.getVideoElement();
    const seekDuration = knownDuration ?? options.getSeekDuration(video);
    if (!video || !seekDuration) return;
    const rect = e.currentTarget.getBoundingClientRect();
    const ratio = clamp((e.clientX - rect.left) / rect.width, 0, 1);
    const next = ratio * seekDuration;
    progressTargetTimeRef.current = next;
    scheduleProgressPreview(next);
    scheduleMediaSeek(video, next);
  }

  return {
    handleProgressPointerDown,
    handleProgressPointerMove,
    handleProgressPointerEnd,
  };
}
