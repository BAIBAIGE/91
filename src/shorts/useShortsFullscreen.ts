import { useEffect, useRef, useState } from "react";
import {
  exitShortsFullscreen,
  isShortsFullscreen,
  observeShortsFullscreen,
  requestShortsFullscreen,
  supportsShortsFullscreen,
} from "./fullscreen";

/** 全屏以浏览器的实际状态为准，清屏和媒体播放由各自的组件负责。 */
export function useShortsFullscreen() {
  const [isFullscreen, setIsFullscreen] = useState(isShortsFullscreen);
  const [fullscreenSupported] = useState(supportsShortsFullscreen);
  const mountedRef = useRef(false);

  useEffect(() => {
    mountedRef.current = true;
    const stopObserving = observeShortsFullscreen(setIsFullscreen);
    return () => {
      stopObserving();
      mountedRef.current = false;
      // StrictMode 会在同一轮重放 effect。真正卸载后才退出原生全屏。
      queueMicrotask(() => {
        if (!mountedRef.current) void exitShortsFullscreen();
      });
    };
  }, []);

  return { isFullscreen, fullscreenSupported, requestFullscreen: requestShortsFullscreen };
}
