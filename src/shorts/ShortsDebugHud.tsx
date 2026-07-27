import { useEffect, useRef, useState } from "react";
import { bufferedAheadSeconds } from "./mediaBuffer";

/**
 * 短视频调试开关：地址栏带 ?debug=1 时在页面左上角叠一层实时观测面板。
 * 只读取 media element 与页面状态，不参与任何播放控制，供真机排查
 * 缓冲水位 / 预加载授权 / WebKit 呈现帧问题时使用。
 */
export function isShortsDebugEnabled() {
  if (typeof window === "undefined") return false;
  const value = new URLSearchParams(window.location.search).get("debug");
  return value !== null && value !== "0";
}

type ShortsDebugSample = {
  hasVideo: boolean;
  readyState: number;
  networkState: number;
  paused: boolean;
  seeking: boolean;
  ended: boolean;
  playbackRate: number;
  currentTime: number;
  duration: number;
  aheadSeconds: number;
  bufferedRanges: string;
  videoWidth: number;
  videoHeight: number;
  presentedFps: number | null;
  errorCode: number | null;
};

type ShortsDebugHudProps = {
  activeIndex: number;
  itemCount: number;
  itemId: string | null;
  getActiveVideo: () => HTMLVideoElement | null;
  windowStart: number;
  windowEnd: number;
  activeReadyForPreload: boolean;
  cachedSourceCount: number;
  muted: boolean;
  usesIOSSharedVideo: boolean;
  usesDocumentScroll: boolean;
};

const SAMPLE_INTERVAL_MS = 500;

export function ShortsDebugHud(props: ShortsDebugHudProps) {
  const [sample, setSample] = useState<ShortsDebugSample | null>(null);
  const propsRef = useRef(props);
  propsRef.current = props;

  useEffect(() => {
    let disposed = false;
    let observedVideo: HTMLVideoElement | null = null;
    let frameCallbackId: number | null = null;
    let presentedFrameCount = 0;
    let lastSampleAt = performance.now();

    // 用 rVFC 数实际送进合成器的帧：媒体时钟在走但 fps=0 就是"黑屏假播放"。
    const observePresentedFrames = (video: HTMLVideoElement | null) => {
      if (observedVideo === video) return;
      if (
        observedVideo &&
        frameCallbackId !== null &&
        typeof observedVideo.cancelVideoFrameCallback === "function"
      ) {
        observedVideo.cancelVideoFrameCallback(frameCallbackId);
      }
      frameCallbackId = null;
      observedVideo = video;
      presentedFrameCount = 0;
      if (!video || typeof video.requestVideoFrameCallback !== "function") {
        return;
      }
      const onFrame = () => {
        if (disposed || observedVideo !== video) return;
        presentedFrameCount += 1;
        frameCallbackId = video.requestVideoFrameCallback(onFrame);
      };
      frameCallbackId = video.requestVideoFrameCallback(onFrame);
    };

    const collectSample = () => {
      const video = propsRef.current.getActiveVideo();
      observePresentedFrames(video);

      const now = performance.now();
      const elapsedMs = Math.max(1, now - lastSampleAt);
      lastSampleAt = now;
      const fps =
        video && typeof video.requestVideoFrameCallback === "function"
          ? Math.round((presentedFrameCount / elapsedMs) * 1000)
          : null;
      presentedFrameCount = 0;

      if (!video) {
        setSample({
          hasVideo: false,
          readyState: -1,
          networkState: -1,
          paused: true,
          seeking: false,
          ended: false,
          playbackRate: 1,
          currentTime: 0,
          duration: 0,
          aheadSeconds: 0,
          bufferedRanges: "-",
          videoWidth: 0,
          videoHeight: 0,
          presentedFps: null,
          errorCode: null,
        });
        return;
      }

      const ranges: string[] = [];
      for (let i = 0; i < video.buffered.length && i < 3; i += 1) {
        ranges.push(
          `${video.buffered.start(i).toFixed(1)}-${video.buffered.end(i).toFixed(1)}`
        );
      }
      setSample({
        hasVideo: true,
        readyState: video.readyState,
        networkState: video.networkState,
        paused: video.paused,
        seeking: video.seeking,
        ended: video.ended,
        playbackRate: video.playbackRate,
        currentTime: video.currentTime || 0,
        duration: Number.isFinite(video.duration) ? video.duration : 0,
        aheadSeconds: bufferedAheadSeconds(video),
        bufferedRanges: ranges.length > 0 ? ranges.join(" ") : "-",
        videoWidth: video.videoWidth,
        videoHeight: video.videoHeight,
        presentedFps: fps,
        errorCode: video.error ? video.error.code : null,
      });
    };

    collectSample();
    const timer = window.setInterval(collectSample, SAMPLE_INTERVAL_MS);
    return () => {
      disposed = true;
      window.clearInterval(timer);
      if (
        observedVideo &&
        frameCallbackId !== null &&
        typeof observedVideo.cancelVideoFrameCallback === "function"
      ) {
        observedVideo.cancelVideoFrameCallback(frameCallbackId);
      }
    };
  }, []);

  const {
    activeIndex,
    itemCount,
    itemId,
    windowStart,
    windowEnd,
    activeReadyForPreload,
    cachedSourceCount,
    muted,
    usesIOSSharedVideo,
    usesDocumentScroll,
  } = props;

  const lines = [
    `#${activeIndex + 1}/${itemCount} id=${itemId ?? "-"}`,
    `win=[${windowStart},${windowEnd}] cached=${cachedSourceCount} preload=${
      activeReadyForPreload ? "granted" : "held"
    }`,
    sample?.hasVideo
      ? `ready=${sample.readyState} net=${sample.networkState} ` +
        `${sample.paused ? "paused" : "playing"}${sample.seeking ? " seeking" : ""}` +
        `${sample.ended ? " ended" : ""} rate=${sample.playbackRate}` +
        `${sample.errorCode !== null ? ` err=${sample.errorCode}` : ""}`
      : "video=none",
    sample?.hasVideo
      ? `t=${sample.currentTime.toFixed(1)}/${sample.duration.toFixed(1)} ` +
        `ahead=${sample.aheadSeconds.toFixed(1)}s buf=${sample.bufferedRanges}`
      : "",
    sample?.hasVideo
      ? `${sample.videoWidth}x${sample.videoHeight}` +
        `${sample.presentedFps !== null ? ` fps≈${sample.presentedFps}` : ""} muted=${muted}`
      : "",
    `ios-shared=${usesIOSSharedVideo} doc-scroll=${usesDocumentScroll}`,
  ].filter(Boolean);

  return (
    <div
      aria-hidden="true"
      style={{
        position: "fixed",
        top: "calc(env(safe-area-inset-top, 0px) + 64px)",
        left: 8,
        zIndex: 60,
        padding: "6px 8px",
        borderRadius: 6,
        background: "rgba(0, 0, 0, 0.72)",
        color: "#7CFC9B",
        font: "10px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace",
        whiteSpace: "pre",
        pointerEvents: "none",
        maxWidth: "calc(100vw - 16px)",
        overflow: "hidden",
      }}
    >
      {lines.join("\n")}
    </div>
  );
}
