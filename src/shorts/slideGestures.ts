import { clamp } from "./mediaBuffer";

const SHORTS_SEEK_ACTIVATION_PX = 12;
const SHORTS_SEEK_DIRECTION_LOCK_RATIO = 1.2;
export type TouchSeekIntent = "pending" | "seek" | "vertical";

/**
 * 手势方向判定：横纵位移都小于激活阈值时继续观望；横向没有明显
 * 大于纵向时交还给纵向滚动，否则进入相对快进/快退。
 */
export function classifyTouchSeekIntent(dx: number, dy: number): TouchSeekIntent {
  const absX = Math.abs(dx);
  const absY = Math.abs(dy);
  if (absX < SHORTS_SEEK_ACTIVATION_PX && absY < SHORTS_SEEK_ACTIVATION_PX) {
    return "pending";
  }
  return absX < absY * SHORTS_SEEK_DIRECTION_LOCK_RATIO ? "vertical" : "seek";
}

/** 相对快进：横向位移按视频宽度折算成时长偏移，并夹在 [0, duration]。 */
export function computeTouchSeekTime(input: {
  startTime: number;
  dx: number;
  width: number;
  duration: number;
}): number {
  return clamp(
    input.startTime + (input.dx / Math.max(1, input.width)) * input.duration,
    0,
    input.duration
  );
}

// 从抬手开始计时；第二次按下就锁定候选，避免等第二次抬手时单击已经执行。
export const SHORTS_DOUBLE_TAP_MS = 240;
const SHORTS_DOUBLE_TAP_DISTANCE_PX = 32;
const SHORTS_LONG_PRESS_MS = 400;
const INTERACTIVE_SELECTOR =
  'button, a, input, select, textarea, [role="button"], [contenteditable], [data-shorts-no-swipe], .shorts-slide__actions';

type SurfaceGestureHost = {
  surface: HTMLElement;
  video: HTMLVideoElement;
  isEnabled: () => boolean;
  onSingleTap: () => void;
  onDoubleTap: (x: number, y: number) => void;
  shouldResumeImmediately: () => boolean;
  onImmediateResume: () => void;
  onFastChange: (fast: boolean) => void;
  getSeekDuration: () => number;
  onSeekStart: () => void;
  onSeekPreview: (time: number) => void;
  onSeekEnd: (time: number) => void;
};

/** 一次 pointer 序列只能成为轻点、长按、横滑或竖滑中的一种。 */
export function createShortsSurfaceGestures(host: SurfaceGestureHost) {
  const { surface, video } = host;
  let press: {
    id: number;
    pointerType: string;
    x: number;
    y: number;
    at: number;
    startTime: number;
    width: number;
    targetTime: number;
    mode: "pending" | "fast" | "seek";
    secondTap: boolean;
  } | null = null;
  let pendingTap: {
    x: number;
    y: number;
    at: number;
    pointerType: string;
    resumed: boolean;
  } | null = null;
  let tapTimer: number | null = null;
  let holdTimer: number | null = null;

  function clearTapTimer() {
    if (tapTimer !== null) window.clearTimeout(tapTimer);
    tapTimer = null;
  }

  function cancelTap() {
    clearTapTimer();
    pendingTap = null;
  }

  function finishTap() {
    const tap = pendingTap;
    cancelTap();
    if (tap && !tap.resumed && host.isEnabled()) host.onSingleTap();
  }

  function clearHoldTimer() {
    if (holdTimer !== null) window.clearTimeout(holdTimer);
    holdTimer = null;
  }

  function endPress() {
    clearHoldTimer();
    if (press?.mode === "fast") {
      video.playbackRate = 1;
      host.onFastChange(false);
    } else if (press?.mode === "seek") {
      host.onSeekEnd(press.targetTime);
    }
    press = null;
  }

  function cancel() {
    cancelTap();
    endPress();
  }

  function isSurfaceTarget(target: EventTarget | null) {
    const element = target as Element | null;
    return Boolean(element && surface.contains(element) && !element.closest(INTERACTIVE_SELECTOR));
  }

  // 另一根手指、进度条或按钮接管输入时，挂起的单击不能随后改变播放状态。
  function handleGlobalDown(event: PointerEvent) {
    if (!isSurfaceTarget(event.target) || !event.isPrimary || (press && press.id !== event.pointerId)) {
      cancel();
    }
  }

  function handleDown(event: PointerEvent) {
    if (!host.isEnabled() || !event.isPrimary || event.button !== 0 || !isSurfaceTarget(event.target)) return;
    endPress();
    const now = performance.now();
    const secondTap = Boolean(
      pendingTap && pendingTap.pointerType === event.pointerType &&
      now - pendingTap.at <= SHORTS_DOUBLE_TAP_MS &&
      Math.hypot(event.clientX - pendingTap.x, event.clientY - pendingTap.y) <= SHORTS_DOUBLE_TAP_DISTANCE_PX
    );
    if (secondTap) clearTapTimer();
    else finishTap();
    press = {
      id: event.pointerId,
      pointerType: event.pointerType,
      x: event.clientX,
      y: event.clientY,
      at: now,
      startTime: video.currentTime || 0,
      width: Math.max(1, video.getBoundingClientRect().width),
      targetTime: video.currentTime || 0,
      mode: "pending",
      secondTap,
    };
    holdTimer = window.setTimeout(() => {
      holdTimer = null;
      if (!press) return;
      cancelTap();
      // 暂停中的长按也不是轻点，但不启动倍速播放。
      if (!host.isEnabled() || video.paused || video.ended) {
        endPress();
        return;
      }
      press.mode = "fast";
      video.playbackRate = 2;
      host.onFastChange(true);
    }, SHORTS_LONG_PRESS_MS);
  }

  function handleMove(event: PointerEvent) {
    if (!press || event.pointerId !== press.id) return;
    if (!host.isEnabled()) return cancel();
    const dx = event.clientX - press.x;
    const dy = event.clientY - press.y;
    if (press.mode !== "seek") {
      const intent = classifyTouchSeekIntent(dx, dy);
      if (intent === "pending") return;
      clearHoldTimer();
      cancelTap();
      if (press.mode === "fast") {
        video.playbackRate = 1;
        host.onFastChange(false);
        press.mode = "pending";
      }
      // 竖滑留给翻页控制器，鼠标横拖也不能变成轻点。
      if (intent === "vertical" || event.pointerType === "mouse") return endPress();
      press.mode = "seek";
      host.onSeekStart();
    }
    if (event.cancelable) event.preventDefault();
    const duration = host.getSeekDuration();
    if (!duration) return;
    press.targetTime = computeTouchSeekTime({
      startTime: press.startTime,
      dx,
      width: press.width,
      duration,
    });
    host.onSeekPreview(press.targetTime);
  }

  function handleUp(event: PointerEvent) {
    if (!press || event.pointerId !== press.id) return;
    // 最终坐标也参与判定，避免漏发最后一次 move 时把拖动算成轻点。
    handleMove(event);
    const ended = press;
    endPress();
    if (!ended || ended.mode !== "pending" || !host.isEnabled()) return;
    if (performance.now() - ended.at >= SHORTS_LONG_PRESS_MS) return cancelTap();
    if (ended.secondTap && pendingTap) {
      cancelTap();
      const rect = surface.getBoundingClientRect();
      host.onDoubleTap(event.clientX - rect.left, event.clientY - rect.top);
      return;
    }
    pendingTap = {
      x: event.clientX,
      y: event.clientY,
      at: performance.now(),
      pointerType: event.pointerType,
      resumed: host.shouldResumeImmediately(),
    };
    // 仅自动播放被浏览器拒绝时直接恢复，保留 Safari 的用户激活权限。
    if (pendingTap.resumed) host.onImmediateResume();
    tapTimer = window.setTimeout(finishTap, SHORTS_DOUBLE_TAP_MS);
  }

  function handleCancel(event: PointerEvent) {
    if (press?.id === event.pointerId) cancel();
  }

  function handleClick(event: MouseEvent) {
    if (!isSurfaceTarget(event.target) || !host.isEnabled()) return;
    // 触摸和鼠标都已经在 pointerup 处理；只为无指针的辅助技术激活保留 click。
    if (event.detail !== 0 || (event as PointerEvent).pointerType) return;
    cancel();
    host.onSingleTap();
  }

  function handlePause() {
    clearHoldTimer();
    if (press?.mode === "fast") endPress();
  }

  surface.addEventListener("pointerdown", handleDown);
  surface.addEventListener("click", handleClick);
  window.addEventListener("pointerdown", handleGlobalDown, true);
  window.addEventListener("pointermove", handleMove, { passive: false });
  window.addEventListener("pointerup", handleUp);
  window.addEventListener("pointercancel", handleCancel);
  window.addEventListener("blur", cancel);
  video.addEventListener("pause", handlePause);
  video.addEventListener("ended", handlePause);
  return () => {
    cancel();
    surface.removeEventListener("pointerdown", handleDown);
    surface.removeEventListener("click", handleClick);
    window.removeEventListener("pointerdown", handleGlobalDown, true);
    window.removeEventListener("pointermove", handleMove);
    window.removeEventListener("pointerup", handleUp);
    window.removeEventListener("pointercancel", handleCancel);
    window.removeEventListener("blur", cancel);
    video.removeEventListener("pause", handlePause);
    video.removeEventListener("ended", handlePause);
  };
}
