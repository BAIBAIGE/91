// 短视频播放的缓冲水位与视频缓存窗口判定。全部是纯函数，只读取
// media element 的少量字段，因此可以在 Node 测试里用普通对象驱动。

// 当前视频至少有这么多秒的前向缓冲后，才允许后续视频开始预加载。
export const ACTIVE_PRELOAD_BUFFER_SECONDS = 12;

// 预加载授权一旦发出，只有当前视频前向缓冲跌破这个秒数（或发生 stall）
// 才收回。高低水位之间不动作，避免缓冲量在 12s 附近波动时
// 反复绑定/剥离后续视频的 src、丢弃已预加载的数据。
export const ACTIVE_PRELOAD_KEEP_SECONDS = 4;

// 维护一个固定大小的视频窗口：窗口内才 mount 真实 <video> 壳。
// 当前屏先绑定 src；后续预加载要等当前屏缓冲健康后才开始。
// 窗口内只要已经产生过可复用缓冲，就保留 src 复用浏览器缓存。
export const VIDEO_WINDOW_SIZE = 6;

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

/** 前向缓冲健康（达到高水位或已缓冲到结尾），可以放心预加载后续视频 */
export function videoHasComfortableBuffer(video: BufferedMediaProbe) {
  if (video.readyState < 3) return false;
  if (videoBufferedToEnd(video)) return true;
  return bufferedAheadSeconds(video) >= ACTIVE_PRELOAD_BUFFER_SECONDS;
}

/** 前向缓冲告急（跌破低水位且没缓冲到结尾），应收回预加载授权 */
export function videoBufferIsCritical(video: BufferedMediaProbe) {
  if (video.readyState < 3) return true;
  if (videoBufferedToEnd(video)) return false;
  return bufferedAheadSeconds(video) < ACTIVE_PRELOAD_KEEP_SECONDS;
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
