import assert from "node:assert/strict";
import test from "node:test";
import {
  createShortsSurfaceGestures,
  SHORTS_DOUBLE_TAP_MS,
} from "../src/shorts/slideGestures";

function createHarness() {
  let now = 1_000;
  let nextTimer = 0;
  const timers = new Map<number, { at: number; run: () => void }>();
  function eventTarget() {
    const listeners = new Map<string, Set<(event: any) => void>>();
    return {
      addEventListener(type: string, handler: (event: any) => void) {
        if (!listeners.has(type)) listeners.set(type, new Set());
        listeners.get(type)!.add(handler);
      },
      removeEventListener(type: string, handler: (event: any) => void) {
        listeners.get(type)?.delete(handler);
      },
      emit(type: string, event: object = {}) {
        for (const handler of listeners.get(type) ?? []) handler(event);
      },
      get listenerCount() {
        return [...listeners.values()].reduce((sum, handlers) => sum + handlers.size, 0);
      },
    };
  }
  const surfaceTarget = { closest: () => null };
  const buttonTarget = { closest: () => ({ tagName: "BUTTON" }) };
  const outsideTarget = { closest: () => null };
  const surface = {
    ...eventTarget(),
    contains: (target: unknown) => target === surfaceTarget || target === buttonTarget,
    getBoundingClientRect: () => ({ left: 10, top: 20 }),
  };
  const video = {
    ...eventTarget(),
    paused: false,
    ended: false,
    playbackRate: 1,
    currentTime: 30,
    getBoundingClientRect: () => ({ width: 300 }),
  };
  const browser = {
    ...eventTarget(),
    setTimeout(run: () => void, delay: number) {
      const id = ++nextTimer;
      timers.set(id, { at: now + delay, run });
      return id;
    },
    clearTimeout(id: number) { timers.delete(id); },
  };
  const originalWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const originalPerformance = Object.getOwnPropertyDescriptor(globalThis, "performance");
  Object.defineProperty(globalThis, "window", { configurable: true, value: browser });
  Object.defineProperty(globalThis, "performance", { configurable: true, value: { now: () => now } });
  const state = {
    enabled: true,
    blocked: false,
    singles: 0,
    doubles: [] as Array<[number, number]>,
    resumes: 0,
    fast: false,
    seeking: false,
    seekPreviews: [] as number[],
    seekEnds: [] as number[],
  };
  const destroy = createShortsSurfaceGestures({
    surface: surface as unknown as HTMLElement,
    video: video as unknown as HTMLVideoElement,
    isEnabled: () => state.enabled,
    onSingleTap() { state.singles++; video.paused = !video.paused; },
    onDoubleTap(x, y) { state.doubles.push([x, y]); },
    shouldResumeImmediately: () => state.blocked,
    onImmediateResume() { state.resumes++; state.blocked = false; video.paused = false; },
    onFastChange(fast) { state.fast = fast; },
    getSeekDuration: () => 120,
    onSeekStart() { state.seeking = true; },
    onSeekPreview(time) { state.seekPreviews.push(time); },
    onSeekEnd(time) { state.seeking = false; state.seekEnds.push(time); },
  });
  function pointer(type: string, x = 100, y = 200, extra: Record<string, unknown> = {}) {
    const event = {
      pointerId: 1, pointerType: "touch", isPrimary: true, button: 0,
      clientX: x, clientY: y, target: surfaceTarget, cancelable: true,
      preventDefault() {}, ...extra,
    };
    // window 的 down 监听器在捕获阶段运行；move/up 则从翻页容器冒泡过来。
    browser.emit(type, event);
    if (type === "pointerdown") surface.emit(type, event);
  }
  function advance(ms: number) {
    const end = now + ms;
    for (;;) {
      const next = [...timers].filter(([, timer]) => timer.at <= end)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!next) break;
      now = next[1].at;
      timers.delete(next[0]);
      next[1].run();
    }
    now = end;
  }
  return {
    state, video, surface, browser, buttonTarget, outsideTarget, pointer, advance, destroy,
    tap(x = 100, y = 200, extra: Record<string, unknown> = {}) {
      pointer("pointerdown", x, y, extra);
      advance(40);
      pointer("pointerup", x, y, extra);
    },
    click(detail = 1, pointerType = "touch") {
      surface.emit("click", { detail, pointerType, target: surfaceTarget });
    },
    restore() {
      destroy();
      if (originalWindow) Object.defineProperty(globalThis, "window", originalWindow);
      else delete (globalThis as { window?: unknown }).window;
      if (originalPerformance) Object.defineProperty(globalThis, "performance", originalPerformance);
      assert.equal(timers.size, 0);
      assert.equal(surface.listenerCount + browser.listenerCount + video.listenerCount, 0);
    },
  };
}

type Harness = ReturnType<typeof createHarness>;
function withHarness(run: (h: Harness) => void) {
  const h = createHarness();
  try { run(h); } finally { h.restore(); }
}

test("a tap pauses without a browser click and duplicate clicks do not toggle again", () => withHarness(h => {
  h.tap();
  h.advance(SHORTS_DOUBLE_TAP_MS - 1);
  assert.equal(h.video.paused, false);
  h.advance(1);
  assert.equal(h.video.paused, true);
  h.click();
  h.click(0, "touch");
  h.advance(1_000);
  assert.equal(h.state.singles, 1);
}));

test("a second press reserves the double tap before the single deadline expires", () => withHarness(h => {
  h.tap();
  h.advance(220);
  h.pointer("pointerdown", 110, 205);
  h.advance(100);
  assert.equal(h.state.singles, 0);
  h.pointer("pointerup", 110, 205);
  h.advance(1_000);
  assert.deepEqual(h.state.doubles, [[100, 185]]);
  assert.equal(h.state.singles, 0);
  assert.equal(h.video.paused, false);
}));

test("a double tap on a user-paused video likes without starting playback", () => withHarness(h => {
  h.video.paused = true;
  h.tap(); h.advance(80); h.tap(); h.advance(500);
  assert.equal(h.state.doubles.length, 1);
  assert.equal(h.state.singles + h.state.resumes, 0);
  assert.equal(h.video.paused, true);
}));

test("distant taps and taps outside the time window remain separate singles", () => withHarness(h => {
  h.tap(); h.advance(30); h.tap(180, 200); h.advance(500);
  assert.equal(h.state.singles, 2);
  assert.equal(h.state.doubles.length, 0);
  h.tap(); h.advance(300); h.tap(); h.advance(500);
  assert.equal(h.state.singles, 4);
  assert.equal(h.state.doubles.length, 0);
}));

test("finger jitter stays a tap and final movement is checked even without move events", () => withHarness(h => {
  h.pointer("pointerdown");
  h.pointer("pointermove", 108, 206);
  h.pointer("pointerup", 109, 207);
  h.advance(300);
  assert.equal(h.state.singles, 1);
  h.pointer("pointerdown"); h.pointer("pointerup", 100, 240); h.advance(300);
  assert.equal(h.state.singles, 1);
}));

test("long press resets speed and the very next tap works when no synthetic click arrives", () => withHarness(h => {
  h.pointer("pointerdown"); h.advance(410);
  assert.equal(h.video.playbackRate, 2);
  h.pointer("pointerup");
  assert.equal(h.video.playbackRate, 1);
  assert.equal(h.state.fast, false);
  h.tap(); h.advance(300);
  assert.equal(h.state.singles, 1);
  assert.equal(h.video.paused, true);
}));

test("holding a second press cancels the pending single and does not like", () => withHarness(h => {
  h.tap(); h.advance(100); h.pointer("pointerdown"); h.advance(500); h.pointer("pointerup");
  h.click(); h.advance(500);
  assert.equal(h.state.singles + h.state.doubles.length, 0);
  assert.equal(h.video.playbackRate, 1);
}));

test("a long press while paused does not start playback on release", () => withHarness(h => {
  h.video.paused = true;
  h.pointer("pointerdown"); h.advance(500); h.pointer("pointerup"); h.click(); h.advance(500);
  assert.equal(h.video.paused, true);
  assert.equal(h.video.playbackRate, 1);
  assert.equal(h.state.singles, 0);
}));

test("horizontal seeking commits the release position and the next tap still works", () => withHarness(h => {
  h.pointer("pointerdown"); h.pointer("pointermove", 130, 200);
  assert.equal(h.state.seeking, true);
  assert.equal(h.state.seekPreviews.at(-1), 42);
  h.pointer("pointerup", 160, 200);
  assert.deepEqual(h.state.seekEnds, [54]);
  assert.equal(h.state.seeking, false);
  h.click(); h.tap(); h.advance(300);
  assert.equal(h.state.singles, 1);
}));

test("vertical swipes cancel a pending tap, including when release is captured by the pager", () => withHarness(h => {
  h.tap(); h.advance(80); h.pointer("pointerdown");
  h.pointer("pointermove", 100, 240);
  h.pointer("pointerup", 100, 300, { target: h.outsideTarget });
  h.click(); h.advance(500);
  assert.equal(h.state.singles + h.state.doubles.length, 0);
  h.tap(); h.advance(300);
  assert.equal(h.state.singles, 1);
}));

test("pressing a control cancels a pending tap and does not become a surface gesture", () => withHarness(h => {
  h.tap(); h.advance(60);
  h.pointer("pointerdown", 100, 200, { target: h.buttonTarget });
  h.pointer("pointerup", 100, 200, { target: h.buttonTarget });
  h.advance(500);
  assert.equal(h.state.singles + h.state.doubles.length, 0);
  h.tap(); h.advance(300);
  assert.equal(h.state.singles, 1);
}));

test("multi-touch and pointer cancellation discard the whole tap sequence", () => withHarness(h => {
  h.tap(); h.advance(60); h.pointer("pointerdown");
  h.pointer("pointerdown", 130, 200, { pointerId: 2, isPrimary: false });
  h.pointer("pointerup", 130, 200, { pointerId: 2, isPrimary: false });
  h.pointer("pointerup"); h.advance(500);
  assert.equal(h.state.singles + h.state.doubles.length, 0);
  h.tap(); h.pointer("pointerdown"); h.pointer("pointercancel"); h.advance(500);
  assert.equal(h.state.singles + h.state.doubles.length, 0);
}));

test("deactivation and disposal prevent delayed taps from affecting the next video", () => withHarness(h => {
  h.tap(); h.state.enabled = false; h.advance(500);
  assert.equal(h.state.singles, 0);
  h.state.enabled = true; h.tap(); h.destroy(); h.advance(500);
  assert.equal(h.state.singles, 0);
}));

test("disposal restores playback speed and removes all listeners and timers", () => withHarness(h => {
  h.pointer("pointerdown"); h.advance(410); h.destroy();
  assert.equal(h.video.playbackRate, 1);
  assert.equal(h.state.fast, false);
}));

test("autoplay recovery happens inside pointerup and double tap never toggles it back", () => withHarness(h => {
  h.state.blocked = true; h.video.paused = true;
  h.tap();
  assert.equal(h.state.resumes, 1);
  assert.equal(h.video.paused, false);
  h.advance(80); h.tap(); h.advance(500);
  assert.equal(h.state.doubles.length, 1);
  assert.equal(h.state.singles, 0);
}));

test("mouse double clicks use the same exclusive dispatch and drag cancels tapping", () => withHarness(h => {
  const mouse = { pointerType: "mouse" };
  h.tap(100, 200, mouse); h.click(1, "mouse");
  h.advance(80); h.tap(100, 200, mouse); h.click(2, "mouse"); h.advance(500);
  assert.equal(h.state.doubles.length, 1);
  assert.equal(h.state.singles, 0);
  h.pointer("pointerdown", 100, 200, mouse); h.pointer("pointermove", 150, 200, mouse);
  h.pointer("pointerup", 150, 200, mouse); h.click(1, "mouse"); h.advance(500);
  assert.equal(h.state.singles, 0);
  assert.equal(h.state.seekPreviews.length, 0);
}));

test("a third tap starts a fresh single after a double tap", () => withHarness(h => {
  h.tap(); h.advance(60); h.tap(); h.advance(60); h.tap(); h.advance(500);
  assert.equal(h.state.doubles.length, 1);
  assert.equal(h.state.singles, 1);
}));

test("assistive click activation still works without a pointer sequence", () => withHarness(h => {
  h.click(0, "");
  assert.equal(h.state.singles, 1);
  assert.equal(h.video.paused, true);
}));
