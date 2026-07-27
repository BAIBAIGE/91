import assert from "node:assert/strict";
import test from "node:test";
import {
  ACTIVE_PRELOAD_BUFFER_SECONDS,
  ACTIVE_PRELOAD_KEEP_SECONDS,
  VIDEO_WINDOW_SIZE,
  bufferedAheadSeconds,
  getVideoWindowBounds,
  videoBufferIsCritical,
  videoBufferedToEnd,
  videoHasBufferedData,
  videoHasComfortableBuffer,
  type BufferedMediaProbe,
} from "../src/shorts/mediaBuffer";

function probe(input: {
  currentTime?: number;
  duration?: number;
  readyState?: number;
  ranges?: Array<[number, number]>;
}): BufferedMediaProbe {
  const ranges = input.ranges ?? [];
  return {
    currentTime: input.currentTime ?? 0,
    duration: input.duration ?? 600,
    readyState: input.readyState ?? 4,
    buffered: {
      length: ranges.length,
      start: (index) => ranges[index][0],
      end: (index) => ranges[index][1],
    },
  };
}

test("bufferedAheadSeconds measures the range containing the playhead", () => {
  assert.equal(
    bufferedAheadSeconds(probe({ currentTime: 10, ranges: [[0, 25]] })),
    15
  );
  // 播放点落在缓冲空洞里时没有可用前向缓冲
  assert.equal(
    bufferedAheadSeconds(
      probe({ currentTime: 10, ranges: [[0, 5], [20, 30]] })
    ),
    0
  );
  // 起点在播放点 0.25s 容差内的区间仍然算数
  assert.equal(
    bufferedAheadSeconds(probe({ currentTime: 10, ranges: [[10.2, 22]] })),
    12
  );
  assert.equal(bufferedAheadSeconds(probe({ ranges: [] })), 0);
});

test("comfortable buffer needs the high watermark or buffered-to-end", () => {
  // readyState 不足时无论缓冲多少都不健康
  assert.equal(
    videoHasComfortableBuffer(
      probe({ currentTime: 0, readyState: 2, ranges: [[0, 60]] })
    ),
    false
  );
  assert.equal(
    videoHasComfortableBuffer(
      probe({ currentTime: 10, ranges: [[0, 10 + ACTIVE_PRELOAD_BUFFER_SECONDS]] })
    ),
    true
  );
  assert.equal(
    videoHasComfortableBuffer(probe({ currentTime: 10, ranges: [[0, 21]] })),
    false
  );
  // 临近片尾：不足高水位但已缓冲到结尾，同样视为健康
  const nearEnd = probe({ currentTime: 25, duration: 30, ranges: [[0, 30]] });
  assert.equal(videoBufferedToEnd(nearEnd), true);
  assert.equal(videoHasComfortableBuffer(nearEnd), true);
});

test("critical buffer needs dropping below the low watermark", () => {
  assert.equal(
    videoBufferIsCritical(
      probe({ currentTime: 0, readyState: 2, ranges: [[0, 60]] })
    ),
    true
  );
  assert.equal(
    videoBufferIsCritical(probe({ currentTime: 10, ranges: [[0, 13]] })),
    true
  );
  assert.equal(
    videoBufferIsCritical(
      probe({ currentTime: 10, ranges: [[0, 10 + ACTIVE_PRELOAD_KEEP_SECONDS]] })
    ),
    false
  );
  // 片尾只剩 3s 可播时不算告急：不会再因网络卡顿
  assert.equal(
    videoBufferIsCritical(
      probe({ currentTime: 17, duration: 20, ranges: [[0, 20]] })
    ),
    false
  );
});

test("watermarks form a hysteresis band that holds the current grant", () => {
  // 高低水位之间（例如 8s）既不授权也不收回，避免阈值附近抖动
  const between = probe({ currentTime: 10, ranges: [[0, 18]] });
  assert.equal(videoHasComfortableBuffer(between), false);
  assert.equal(videoBufferIsCritical(between), false);
});

test("videoHasBufferedData requires a non-empty range", () => {
  assert.equal(videoHasBufferedData(probe({ ranges: [] })), false);
  assert.equal(videoHasBufferedData(probe({ ranges: [[5, 5]] })), false);
  assert.equal(videoHasBufferedData(probe({ ranges: [[0, 0.5]] })), true);
});

test("video window slides forward with the highest viewed index", () => {
  // 尚未看过任何视频时窗口为空
  assert.deepEqual(getVideoWindowBounds(-1, 10), { start: 0, end: -1 });
  assert.deepEqual(getVideoWindowBounds(0, 10), { start: 0, end: 0 });
  // 窗口固定为 VIDEO_WINDOW_SIZE 条，尾部对齐最远到达的索引
  assert.deepEqual(getVideoWindowBounds(7, 10), {
    start: 7 - VIDEO_WINDOW_SIZE + 1,
    end: 7,
  });
  // 队列比窗口小则覆盖整个队列
  assert.deepEqual(getVideoWindowBounds(2, 3), { start: 0, end: 2 });
  // 索引越界时收敛到队列末尾（空库后残留的高位索引）
  assert.deepEqual(getVideoWindowBounds(9, 4), { start: 0, end: 3 });
  assert.deepEqual(getVideoWindowBounds(3, 0), { start: 0, end: -1 });
});
