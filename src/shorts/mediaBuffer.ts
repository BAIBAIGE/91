// 短视频播放的缓冲水位与视频缓存窗口判定。全部是纯函数，只读取
// media element 的少量字段，因此可以在 Node 测试里用普通对象驱动。

// 当前视频至少有这么多秒的前向缓冲后，才允许后续视频开始预加载。
// 这同时是自适应门槛的上限：低码率视频完全保持这个原有行为。
export const ACTIVE_PRELOAD_BUFFER_SECONDS = 12;

// 预加载授权一旦发出，只有当前视频前向缓冲跌破这个秒数（或发生 stall）
// 才收回。高低水位之间不动作，避免缓冲量在 12s 附近波动时
// 反复绑定/剥离后续视频的 src、丢弃已预加载的数据。
export const ACTIVE_PRELOAD_KEEP_SECONDS = 4;

// 门槛按"秒"定，带宽却是按字节付的。本库平均码率约 10 Mbps——12 秒就是
// ~15 MB，网速跟不上时这个门槛永远达不到，预载授权一直发不出去，整页
// 退化成没有预加载的串行加载（HUD 上表现为 preload 长期 held）。所以
// 真正的预算是这份字节数，秒数只是它在某个码率下的换算结果。
export const ACTIVE_PRELOAD_BUFFER_BYTES = 4 * 1024 * 1024;

// 换算结果的下限。字节预算再紧也要留出够播一会儿的余量，否则授权发得
// 太早，预载会直接和当前视频抢带宽。
export const ACTIVE_PRELOAD_MIN_BUFFER_SECONDS = 4;

// 低水位始终是高水位的这个比例（现状 4/12 正好是 1/3，低码率下行为不变），
// 并且有自己的地板，保证高低水位永远拉开距离、滞回不会退化成单一阈值。
export const ACTIVE_PRELOAD_KEEP_RATIO = 1 / 3;
export const ACTIVE_PRELOAD_MIN_KEEP_SECONDS = 1.5;

// 维护一个固定大小的视频窗口：窗口内才 mount 真实 <video> 壳。
// 当前屏先绑定 src；后续预加载要等当前屏缓冲健康后才开始。
// 窗口内只要已经产生过可复用缓冲，就保留 src 复用浏览器缓存。
export const VIDEO_WINDOW_SIZE = 4;

/** 判定所需的最小视频元素切面；HTMLVideoElement 天然满足。 */
export type BufferedMediaProbe = {
  currentTime: number;
  duration: number;
  readyState: number;
  buffered: {
    length: number;
    start(index: number): number;
    end(index: number): number;
  };
};

export function clamp(n: number, min: number, max: number) {
  return n < min ? min : n > max ? max : n;
}

export function getVideoWindowBounds(highestViewedIndex: number, itemCount: number) {
  const size = Math.min(VIDEO_WINDOW_SIZE, itemCount);
  if (size <= 0 || highestViewedIndex < 0) return { start: 0, end: -1 };

  const end = clamp(highestViewedIndex, 0, itemCount - 1);
  const start = Math.max(0, end - size + 1);
  return { start, end };
}

/** 已经缓冲到片尾（含误差余量），不会再因网络卡顿 */
export function videoBufferedToEnd(video: BufferedMediaProbe) {
  const duration = Number.isFinite(video.duration) ? video.duration : 0;
  if (duration <= 0) return false;
  const remaining = Math.max(0, duration - (video.currentTime || 0));
  return bufferedAheadSeconds(video) >= remaining - 0.25;
}

export function videoHasBufferedData(video: BufferedMediaProbe) {
  for (let i = 0; i < video.buffered.length; i += 1) {
    if (video.buffered.end(i) > video.buffered.start(i)) {
      return true;
    }
  }
  return false;
}

/**
 * 把字节预算换算成这条视频的高水位秒数。
 *
 * bytesPerSecond <= 0（元数据缺失）时退回固定的 12 秒，也就是改动前的行为。
 * 结果夹在 [4, 12] 秒：低码率片子算出来超过 12 秒，被上限压回原值；高码率
 * 片子算出来不足 4 秒，被下限托住。换算的分界点在 4MB/12s ≈ 2.7 Mbps——
 * 普通网络视频在这条线以下，行为完全不变；本库那些 ~10 Mbps 的原片在线
 * 以上，门槛从 12 秒（~15 MB）降到 4 秒（~5 MB）。
 */
export function preloadBufferSecondsFor(bytesPerSecond: number) {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) {
    return ACTIVE_PRELOAD_BUFFER_SECONDS;
  }
  return clamp(
    ACTIVE_PRELOAD_BUFFER_BYTES / bytesPerSecond,
    ACTIVE_PRELOAD_MIN_BUFFER_SECONDS,
    ACTIVE_PRELOAD_BUFFER_SECONDS
  );
}

/** 低水位跟着高水位等比缩放，保证滞回区间不会被压没。 */
export function preloadKeepSecondsFor(bufferSeconds: number) {
  return Math.max(
    ACTIVE_PRELOAD_MIN_KEEP_SECONDS,
    bufferSeconds * ACTIVE_PRELOAD_KEEP_RATIO
  );
}

/** 由 sizeBytes / durationSeconds 得到平均码率；任一缺失时返回 0。 */
export function averageBytesPerSecond(input: {
  sizeBytes?: number;
  durationSeconds?: number;
}) {
  const size = input.sizeBytes ?? 0;
  const duration = input.durationSeconds ?? 0;
  if (size <= 0 || duration <= 0) return 0;
  return size / duration;
}

/** 前向缓冲健康（达到高水位或已缓冲到结尾），可以放心预加载后续视频 */
export function videoHasComfortableBuffer(
  video: BufferedMediaProbe,
  bufferSeconds = ACTIVE_PRELOAD_BUFFER_SECONDS
) {
  if (video.readyState < 3) return false;
  if (videoBufferedToEnd(video)) return true;
  return bufferedAheadSeconds(video) >= bufferSeconds;
}

/** 前向缓冲告急（跌破低水位且没缓冲到结尾），应收回预加载授权 */
export function videoBufferIsCritical(
  video: BufferedMediaProbe,
  keepSeconds = ACTIVE_PRELOAD_KEEP_SECONDS
) {
  if (video.readyState < 3) return true;
  if (videoBufferedToEnd(video)) return false;
  return bufferedAheadSeconds(video) < keepSeconds;
}

export function bufferedAheadSeconds(video: BufferedMediaProbe) {
  const current = video.currentTime || 0;
  for (let i = 0; i < video.buffered.length; i += 1) {
    const start = video.buffered.start(i);
    const end = video.buffered.end(i);
    if (start <= current + 0.25 && end > current) {
      return Math.max(0, end - current);
    }
  }
  return 0;
}
