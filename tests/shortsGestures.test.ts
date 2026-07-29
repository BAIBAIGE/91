import assert from "node:assert/strict";
import test from "node:test";
import {
  classifyTouchSeekIntent,
  computeTouchSeekTime,
} from "../src/shorts/useShortsSlideGestures";
import {
  isLegacyShortsVideoTransitionEnabled,
  shouldUsePassiveShortsTouchMove,
} from "../src/shorts/platform";

function withWindowSearch<T>(search: string, run: () => T): T {
  const original = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", {
    value: { location: { search } },
    configurable: true,
    writable: true,
  });
  try {
    return run();
  } finally {
    if (original) {
      Object.defineProperty(globalThis, "window", original);
    } else {
      delete (globalThis as { window?: unknown }).window;
    }
  }
}

test("touchmove stays passive by default with an explicit legacy fallback", () => {
  withWindowSearch("", () => {
    assert.equal(shouldUsePassiveShortsTouchMove(), true);
  });
  withWindowSearch("?shortsPassiveTouch=0", () => {
    assert.equal(shouldUsePassiveShortsTouchMove(), false);
  });
});

test("playing video transition is disabled by default with an A/B fallback", () => {
  withWindowSearch("", () => {
    assert.equal(isLegacyShortsVideoTransitionEnabled(), false);
  });
  withWindowSearch("?shortsVideoTransition=1", () => {
    assert.equal(isLegacyShortsVideoTransitionEnabled(), true);
  });
});

test("touch seek stays pending until movement passes the activation threshold", () => {
  // 横纵位移都在 12px 内继续观望，长按倍速不被打断
  assert.equal(classifyTouchSeekIntent(0, 0), "pending");
  assert.equal(classifyTouchSeekIntent(11, -11), "pending");
  assert.equal(classifyTouchSeekIntent(-11.9, 0), "pending");
  // 任一方向达到阈值就必须定性
  assert.notEqual(classifyTouchSeekIntent(12, 0), "pending");
  assert.equal(classifyTouchSeekIntent(0, 12), "vertical");
});

test("touch seek direction lock hands diagonal swipes back to scrolling", () => {
  // 纵向明显更大：交还给上下滑切换视频
  assert.equal(classifyTouchSeekIntent(10, 30), "vertical");
  assert.equal(classifyTouchSeekIntent(-14, 40), "vertical");
  // 横向足够显著（≥ 纵向 × 1.2）才进入快进
  assert.equal(classifyTouchSeekIntent(30, 10), "seek");
  assert.equal(classifyTouchSeekIntent(-24, 20), "seek");
  // 恰好在锁定比例边界：absX < absY * 1.2 判给纵向
  assert.equal(classifyTouchSeekIntent(23, 20), "vertical");
  assert.equal(classifyTouchSeekIntent(24, 20), "seek");
});

test("touch seek time is relative to the start point and clamped", () => {
  // 滑过整幅宽度 = 拖动整段时长
  assert.equal(
    computeTouchSeekTime({ startTime: 30, dx: 100, width: 200, duration: 60 }),
    60
  );
  assert.equal(
    computeTouchSeekTime({ startTime: 30, dx: -100, width: 200, duration: 60 }),
    0
  );
  assert.equal(
    computeTouchSeekTime({ startTime: 10, dx: 50, width: 500, duration: 100 }),
    20
  );
  // 目标不会越过片头/片尾
  assert.equal(
    computeTouchSeekTime({ startTime: 55, dx: 300, width: 300, duration: 60 }),
    60
  );
  assert.equal(
    computeTouchSeekTime({ startTime: 5, dx: -300, width: 300, duration: 60 }),
    0
  );
  // 宽度异常时按 1px 兜底，不产生除零
  assert.equal(
    computeTouchSeekTime({ startTime: 0, dx: 1, width: 0, duration: 10 }),
    10
  );
});
