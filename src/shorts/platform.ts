// 短视频页的平台 / 浏览器形态探测。这些判定在页面生命周期内视为常量，
// ShortsPage 在渲染前各调用一次并按结果选择播放分支。

export function shouldUseDocumentScrollForShorts() {
  return isIPhoneBrowserShell();
}

/**
 * `?iosPreload=0` 关掉 iOS 备用元素预载（连元素本身都不创建）。
 * 用来隔离"预载抢媒体资源"是否是循环重播失败的原因：同一台机器、同一条
 * 视频，带不带这个参数各看一轮循环即可对照。
 */
export function isIOSStandbyPreloadDisabled() {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(window.location.search).get("iosPreload") === "0";
}

export function isWindowsPlatform() {
  if (typeof navigator === "undefined") return false;
  const platform = navigator.platform || "";
  const ua = navigator.userAgent || "";
  return /^Win/i.test(platform) || /\bWindows\b/i.test(ua);
}

export function shouldUseIOSSharedVideo() {
  if (typeof navigator === "undefined") return false;
  const ua = navigator.userAgent || "";
  if (/\biPhone\b|\biPad\b|\biPod\b/.test(ua)) return true;
  // iPadOS 在“请求桌面网站”模式下会伪装成 Macintosh。
  return navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1;
}

function isIPhoneBrowserShell() {
  if (typeof window === "undefined" || typeof navigator === "undefined") {
    return false;
  }
  const ua = navigator.userAgent || "";
  return /\biPhone\b|\biPod\b/.test(ua) && !isStandaloneDisplayMode();
}

function isStandaloneDisplayMode() {
  if (typeof window === "undefined" || typeof navigator === "undefined") {
    return false;
  }
  const nav = navigator as Navigator & { standalone?: boolean };
  return (
    nav.standalone === true ||
    window.matchMedia?.("(display-mode: standalone)").matches === true ||
    window.matchMedia?.("(display-mode: fullscreen)").matches === true
  );
}
