import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const shortsPageSource = readFileSync(
  new URL("../src/pages/ShortsPage.tsx", import.meta.url),
  "utf8"
);
const shortsCssSource = readFileSync(
  new URL("../src/styles/shorts.css", import.meta.url),
  "utf8"
);
const videosDataSource = readFileSync(
  new URL("../src/data/videos.ts", import.meta.url),
  "utf8"
);
const shareClipboardSource = readFileSync(
  new URL("../src/lib/videoShareClipboard.ts", import.meta.url),
  "utf8"
);
const shortsFeedSource = readFileSync(
  new URL("../src/shorts/shortsFeed.ts", import.meta.url),
  "utf8"
);
const useShortsFeedSource = readFileSync(
  new URL("../src/shorts/useShortsFeed.ts", import.meta.url),
  "utf8"
);
const mediaBufferSource = readFileSync(
  new URL("../src/shorts/mediaBuffer.ts", import.meta.url),
  "utf8"
);
const shortsPlatformSource = readFileSync(
  new URL("../src/shorts/platform.ts", import.meta.url),
  "utf8"
);
const useShortsKeyboardSource = readFileSync(
  new URL("../src/shorts/useShortsKeyboard.tsx", import.meta.url),
  "utf8"
);
const slideGesturesSource = readFileSync(
  new URL("../src/shorts/useShortsSlideGestures.ts", import.meta.url),
  "utf8"
);

test("shorts does not keep recommendation preference from likes or watch time", () => {
  assert.doesNotMatch(shortsPageSource, /currentTime\s*>=\s*3/);
  assert.doesNotMatch(shortsPageSource, /onPreferenceReady/);
  assert.doesNotMatch(shortsPageSource, /preferredFromVideoId/);
  assert.doesNotMatch(videosDataSource, /preferredFromVideoId/);
  // feed 层同样不允许偷偷记录偏好画像
  assert.doesNotMatch(shortsFeedSource + useShortsFeedSource, /preferred|watchTime|seenIds/i);

  const match = /const handleLikeToggle[\s\S]*?const hasLiked/.exec(
    shortsPageSource
  );
  assert.ok(match, "handleLikeToggle block should be present");

  assert.doesNotMatch(match[0], /preferred/i);
  assert.doesNotMatch(videosDataSource, /JSON\.stringify\(\{ seenIds, count \}\)/);
  assert.match(videosDataSource, /const params = new URLSearchParams\(\{\s*cursor: String\(cursor\),\s*count: String\(count\),/);
  assert.match(videosDataSource, /apiGet<ShortsNextResponse>/);
});

test("shorts progress dragging uses immediate pointer state", () => {
  assert.match(shortsPageSource, /const scrubbingRef = useRef\(false\)/);
  assert.match(slideGesturesSource, /scrubbingRef\.current = true;/);
  assert.match(slideGesturesSource, /if \(!options\.scrubbingRef\.current\) return;/);
  assert.doesNotMatch(shortsPageSource + slideGesturesSource, /if \(!scrubbing\) return;/);
  assert.match(shortsPageSource, /function getSeekDuration/);
  assert.match(shortsPageSource, /onLostPointerCapture=\{handleProgressPointerEnd\}/);
});

test("mobile shorts scrubbing time is shown at the top", () => {
  assert.match(
    shortsPageSource,
    /\{scrubbing && isActive && shouldLoad && !isMarkedHidden && \(\s*<div className="shorts-slide__progress-time" aria-live="polite">\s*\{formatClock\(currentTime\)\} \/ \{formatClock\(duration\)\}/
  );
  assert.doesNotMatch(
    shortsPageSource,
    /className=\{`shorts-slide__progress[\s\S]*?<div className="shorts-slide__progress-time"/
  );
  assert.match(
    shortsCssSource,
    /@media \(hover: none\) and \(pointer: coarse\) \{\s*\.shorts-slide__progress-time \{\s*top: calc\(env\(safe-area-inset-top\) \+ 76px\);\s*bottom: auto;/
  );
});

test("shorts horizontal video swipe seeks relative to the current playback time", () => {
  assert.match(slideGesturesSource, /const SHORTS_SEEK_ACTIVATION_PX = 12;/);
  assert.match(slideGesturesSource, /const SHORTS_SEEK_DIRECTION_LOCK_RATIO = 1\.2;/);
  assert.match(slideGesturesSource, /type ShortsTouchSeekState = \{/);
  assert.match(slideGesturesSource, /startTime: video\.currentTime \|\| 0/);
  assert.match(slideGesturesSource, /video\.addEventListener\("touchmove", handleTouchMove, \{ passive: false \}\);/);
  // 相对快进的换算与方向锁的行为用例见 shortsGestures.test.ts
  assert.match(
    slideGesturesSource,
    /input\.startTime \+ \(input\.dx \/ Math\.max\(1, input\.width\)\) \* input\.duration/
  );
  assert.match(slideGesturesSource, /suppressNextClickRef\.current = true;/);
  assert.match(slideGesturesSource, /if \(suppressNextClickRef\.current\) \{/);
  assert.doesNotMatch(
    shortsPageSource + slideGesturesSource,
    /touch\.clientX - rect\.left\) \/ Math\.max\(1,\s*rect\.width\)/
  );
});

test("shorts long-press release does not become a click that pauses playback", () => {
  assert.match(
    slideGesturesSource,
    /const handleTouchEnd = \(event: TouchEvent\) => \{[\s\S]*?const wasFastPress = active;[\s\S]*?if \(wasSeeking \|\| wasFastPress\) \{\s*suppressNextSyntheticClick\(\);/
  );
  assert.match(
    slideGesturesSource,
    /function suppressNextSyntheticClick\(\) \{[\s\S]*?suppressNextClickRef\.current = true;[\s\S]*?SHORTS_SYNTHETIC_CLICK_RESET_MS/
  );
  assert.match(
    slideGesturesSource,
    /if \(suppressNextClickRef\.current\) \{\s*suppressNextClickRef\.current = false;\s*clearSuppressNextClickResetTimer\(\);[\s\S]*?return;/
  );
});

test("shorts progress listeners rebind when deferred videos mount", () => {
  assert.match(
    shortsPageSource,
    /VIDEO_WINDOW_SIZE 会让窗口外的 slide 先以海报占位/
  );
  assert.match(shortsPageSource, /if \(!shouldMount\) \{\s*setDuration\(0\);\s*setCurrentTime\(0\);/);
  assert.match(
    shortsPageSource,
    /getVideoElement,[\s\S]*?shouldLoad,[\s\S]*?shouldMount,[\s\S]*?usesSharedVideo,[\s\S]*?\]\);/
  );
});

test("shorts paused overlay follows native video playback events", () => {
  assert.match(
    shortsPageSource,
    /const handlePlay = \(\) => \{[\s\S]*?if \(isVideoPausedByUser\(index\)\) \{[\s\S]*?video\.pause\(\);[\s\S]*?setPaused\(true\);[\s\S]*?return;[\s\S]*?setPaused\(false\);/
  );
  assert.match(
    shortsPageSource,
    /const handlePause = \(\) => \{[\s\S]*?if \(!isActive \|\| video\.ended\) return;[\s\S]*?setPaused\(true\);[\s\S]*?setIsBuffering\(false\);/
  );
  assert.match(shortsPageSource, /video\.addEventListener\("play", handlePlay\);/);
  assert.match(shortsPageSource, /video\.addEventListener\("pause", handlePause\);/);
  assert.match(shortsPageSource, /video\.removeEventListener\("play", handlePlay\);/);
  assert.match(shortsPageSource, /video\.removeEventListener\("pause", handlePause\);/);
});

test("shorts preserves a user pause while the active video is still loading", () => {
  assert.match(shortsPageSource, /const userPausedIndexRef = useRef<number \| null>\(null\);/);
  assert.match(shortsPageSource, /const \[, setUserPausedIndexState\] = useState<number \| null>\(null\);/);
  assert.match(shortsPageSource, /const setUserPausedForIndex = useCallback/);
  assert.match(
    shortsPageSource,
    /const canContinue = \(\) =>[\s\S]*?!isVideoPausedByUser\(index\);/
  );
  assert.match(
    useShortsKeyboardSource,
    /isVideoPausedByUser\(target\.videoIndex\) \|\|\s*\(target\.video\.paused && target\.video\.readyState >= 3\)/
  );
  assert.match(
    useShortsKeyboardSource,
    /setUserPausedForIndex\(target\.videoIndex, false\);\s*target\.video\.play\(\)\.catch/
  );
  assert.match(
    useShortsKeyboardSource,
    /setUserPausedForIndex\(target\.videoIndex, true\);\s*target\.video\.pause\(\);/
  );
  assert.match(
    shortsPageSource,
    /const shouldResume =\s*isVideoPausedByUser\(index\) \|\| \(video\.paused && !isBuffering\);/
  );
  assert.match(
    shortsPageSource,
    /onUserPausedChange\(index, true\);\s*video\.pause\(\);\s*setPaused\(true\);\s*setIsBuffering\(false\);/
  );
  assert.match(
    shortsPageSource,
    /const handleCanPlay = \(\) => \{[\s\S]*?if \(isActive && isVideoPausedByUser\(index\)\) \{[\s\S]*?video\.pause\(\);[\s\S]*?setPaused\(true\);[\s\S]*?return;/
  );
});

test("shorts retries interrupted active playback and exposes rejected autoplay", () => {
  assert.match(shortsPageSource, /const attemptPlay = \(\) => \{/);
  assert.match(shortsPageSource, /request = video\.play\(\);/);
  assert.match(
    shortsPageSource,
    /errorName === "AbortError" && retryCount < 2[\s\S]*?window\.setTimeout\(attemptPlay, retryCount \* 120\)/
  );
  assert.match(shortsPageSource, /video\.addEventListener\("loadeddata", retryWhenReady\);/);
  assert.match(shortsPageSource, /video\.addEventListener\("canplay", retryWhenReady\);/);
  assert.match(
    shortsPageSource,
    /const markPlayBlocked = \(\) => \{[\s\S]*?setIsBuffering\(false\);[\s\S]*?setPaused\(true\);/
  );
  assert.match(shortsPageSource, /autoPlay=\{isActive\}/);
  // \u81ea\u52a8\u64ad\u653e\u88ab\u62d2\u540e\u7684\u9996\u6b21\u70b9\u51fb\uff1a\u5224\u5b9a\u4e0e\u6062\u590d\u5728 slide\uff0c\u5206\u53d1\u65f6\u5e8f\u5728\u624b\u52bf hook
  assert.match(
    shortsPageSource,
    /function shouldResumeImmediatelyOnClick\(\) \{[\s\S]*?video\?\.paused && !isBuffering/
  );
  assert.match(
    shortsPageSource,
    /function handleImmediateResume\(\) \{[\s\S]*?video\.play\(\)\.catch/
  );
  assert.match(
    slideGesturesSource,
    /if \(options\.shouldResumeImmediately\(\)\) \{[\s\S]*?options\.onImmediateResume\(\);[\s\S]*?return;[\s\S]*?\/\/ \u5355\u51fb\u6302\u8d77/
  );
});

test("shorts keyboard play pause does not show a toast", () => {
  const keyboardBlock = /else if \(e\.key === " "\) \{[\s\S]*?\} else if \(e\.key === "m"/.exec(useShortsKeyboardSource);
  assert.ok(keyboardBlock, "space key handler should be present");
  assert.doesNotMatch(keyboardBlock[0], /showHud\("播放"|showHud\("暂停"/);
});

test("shorts double space likes without toggling playback", () => {
  assert.match(useShortsKeyboardSource, /const SHORTS_KEYBOARD_DOUBLE_SPACE_MS = 280;/);
  assert.match(
    useShortsKeyboardSource,
    /let pendingSpaceTimer: number \| null = null;[\s\S]*?let pendingSpaceTarget:/
  );
  assert.match(
    useShortsKeyboardSource,
    /pendingSpaceTimer = window\.setTimeout\(\(\) => \{[\s\S]*?toggleKeyboardPlayback\(target\);[\s\S]*?SHORTS_KEYBOARD_DOUBLE_SPACE_MS/
  );
  assert.match(
    useShortsKeyboardSource,
    /pendingSpaceTimer !== null &&[\s\S]*?pendingSpaceTarget\.video === activeVideo[\s\S]*?clearKeyboardSpaceTimer\(\);[\s\S]*?likeActiveVideo\(videoIndex\);[\s\S]*?return;/
  );
  assert.match(
    useShortsKeyboardSource,
    /const keyboardLikeHandlersRef = useRef<Map<number, \(\) => void>>\(new Map\(\)\);/
  );
  assert.match(
    useShortsKeyboardSource,
    /const likeActiveVideo = \(videoIndex: number\) => \{\s*keyboardLikeHandlersRef\.current\.get\(videoIndex\)\?\.\(\);/
  );
  assert.match(
    shortsPageSource,
    /registerKeyboardLikeHandler=\{registerKeyboardLikeHandler\}/
  );
  assert.match(
    shortsPageSource,
    /keyboardLikeHandlerRef\.current = \(\) => \{[\s\S]*?slideRef\.current\?\.getBoundingClientRect\(\);[\s\S]*?handleDoubleClickLike\(slideRect\.width \/ 2, slideRect\.height \/ 2\);/
  );
  assert.match(
    shortsPageSource,
    /registerKeyboardLikeHandler\(index, handleKeyboardLike\);[\s\S]*?registerKeyboardLikeHandler\(index, null\);/
  );
  assert.doesNotMatch(shortsPageSource + useShortsKeyboardSource, /new MouseEvent\("click"/);
  assert.match(shortsPageSource, /data-shorts-like=""/);
  assert.match(
    useShortsKeyboardSource,
    /window\.removeEventListener\("blur", handleWindowBlur\);[\s\S]*?clearKeyboardSpaceTimer\(\);/
  );
});

test("shorts keeps the full heart animation when reduced motion is enabled", () => {
  assert.match(
    shortsCssSource,
    /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.shorts-slide__heart-burst\s*\{\s*animation:\s*shorts-heart-pop 650ms cubic-bezier\(0\.175,\s*0\.885,\s*0\.32,\s*1\.275\) forwards !important;/
  );
});

test("desktop held arrow-key seeking previews progress and commits once", () => {
  assert.match(
    useShortsKeyboardSource,
    /type ShortsKeyboardSeekPreview = \{[\s\S]*?videoIndex: number;[\s\S]*?currentTime: number;[\s\S]*?duration: number;/
  );
  assert.match(
    useShortsKeyboardSource,
    /const baseTime = canContinuePendingTarget[\s\S]*?pendingTarget\.currentTime[\s\S]*?: activeVideo\.currentTime;[\s\S]*?const currentTime = clamp\(baseTime \+ delta, 0, duration\);/
  );
  assert.match(
    useShortsKeyboardSource,
    /setKeyboardSeekPreview\(\{ videoIndex, currentTime, duration \}\);/
  );
  assert.match(
    useShortsKeyboardSource,
    /const handleKeyUp = \(e: KeyboardEvent\) => \{[\s\S]*?keyboardSeekHeldKeysRef\.current\.delete\(e\.key\);[\s\S]*?size === 0\) finishKeyboardSeek\(\)/
  );
  const previewStart = useShortsKeyboardSource.indexOf("const previewKeyboardSeek");
  const keydownStart = useShortsKeyboardSource.indexOf("const handleKeyDown", previewStart);
  assert.ok(previewStart >= 0 && keydownStart > previewStart);
  assert.doesNotMatch(
    useShortsKeyboardSource.slice(previewStart, keydownStart),
    /\.currentTime\s*=/
  );
  assert.match(
    useShortsKeyboardSource,
    /const commitKeyboardSeek = \(\) => \{[\s\S]*?target\.video\.currentTime = nextTime;/
  );
  assert.match(
    useShortsKeyboardSource,
    /SHORTS_KEYBOARD_SEEK_IDLE_COMMIT_MS[\s\S]*?scheduleKeyboardSeekIdleCommit/
  );
  assert.match(useShortsKeyboardSource, /window\.addEventListener\("keyup", handleKeyUp\);/);
  assert.match(
    shortsPageSource,
    /className="shorts-keyboard-seek-time" aria-live="polite"[\s\S]*?formatClock\(keyboardSeekPreview\.currentTime\)[\s\S]*?formatClock\(keyboardSeekPreview\.duration\)/
  );
  assert.match(
    shortsCssSource,
    /\.shorts-keyboard-seek-time \{\s*top: calc\(env\(safe-area-inset-top\) \+ 76px\);\s*z-index: 40;/
  );
  assert.match(
    shortsPageSource,
    /keyboardSeekPreview=\{[\s\S]*?keyboardSeekPreview\?\.videoIndex === index[\s\S]*?\? keyboardSeekPreview/
  );
  assert.match(
    shortsPageSource,
    /const progressCurrentTime = keyboardSeekPreview\?\.currentTime \?\? currentTime;[\s\S]*?const progressDuration = keyboardSeekPreview\?\.duration \?\? duration;[\s\S]*?clamp\(progressCurrentTime \/ progressDuration, 0, 1\)/
  );
  assert.match(
    shortsPageSource,
    /useLayoutEffect\(\(\) => \{[\s\S]*?lastKeyboardSeekPreviewTimeRef\.current = keyboardSeekPreview\.currentTime;[\s\S]*?setCurrentTime\(lastPreviewTime\);[\s\S]*?\}, \[keyboardSeekPreview\]\);/
  );
});

test("shorts play pause does not render transient center hud", () => {
  assert.doesNotMatch(shortsPageSource, /function shouldShowPlayPauseHud\(\)/);
  assert.doesNotMatch(shortsPageSource, /setPlayPauseHud/);
  assert.doesNotMatch(shortsPageSource, /playPauseHud/);
  assert.doesNotMatch(shortsPageSource, /shorts-slide__hud-pulse/);
  assert.doesNotMatch(shortsCssSource, /\.shorts-slide__hud-pulse/);
  assert.doesNotMatch(shortsCssSource, /@keyframes shorts-hud-pop/);
  assert.match(
    shortsPageSource,
    /\{paused && isActive && !scrubbing && \(\s*<div className="shorts-slide__paused"/
  );
  assert.match(
    shortsPageSource,
    /<span className="shorts-slide__paused-icon">\s*<Play size=\{22\} fill="currentColor" strokeWidth=\{1\.75\} \/>/
  );
  assert.match(
    shortsCssSource,
    /\.shorts-slide__paused-icon\s*\{[\s\S]*?width:\s*52px;[\s\S]*?height:\s*52px;[\s\S]*?border-radius:\s*50%;/
  );
  assert.doesNotMatch(shortsPageSource, />\s*▶\s*</);
});

test("shorts hud toast keeps icon and text close together", () => {
  assert.match(
    shortsCssSource,
    /\.shorts-hud-toast\s*\{[\s\S]*gap:\s*4px;/
  );
});

test("shorts loading spinner covers video buffering and initial feed loading", () => {
  assert.match(shortsPageSource, /function ShortsLoadingSpinner/);
  assert.match(shortsPageSource, /requestAnimationFrame\(tick\)/);
  assert.match(shortsPageSource, /spinner\.style\.transform = `rotate\(\$\{rotation\}deg\)`;/);
  assert.match(shortsPageSource, /"--shorts-spinner-size": `\$\{size\}px`/);
  assert.match(shortsPageSource, /<ShortsLoadingSpinner size=\{30\} \/>/);
  assert.doesNotMatch(shortsPageSource, /<ShortsLoadingSpinner size=\{16\} \/>/);
  assert.doesNotMatch(shortsPageSource, /加载中…/);
  assert.match(shortsPageSource, /className="shorts-empty shorts-loading" aria-live="polite"/);
  assert.match(shortsPageSource, /正在加载短视频/);
  assert.match(
    shortsCssSource,
    /\.shorts-slide__loading-spinner\s*\{[\s\S]*width:\s*var\(--shorts-spinner-size,\s*30px\);[\s\S]*height:\s*var\(--shorts-spinner-size,\s*30px\);[\s\S]*border:\s*3px solid rgba\(255,\s*255,\s*255,\s*0\.24\);[\s\S]*border-top-color:\s*rgba\(255,\s*255,\s*255,\s*0\.98\);[\s\S]*border-radius:\s*50%;/
  );
  assert.match(shortsCssSource, /\.shorts-loading \.shorts-slide__loading-spinner\s*\{/);
  assert.match(
    shortsCssSource,
    /@media \(max-width:\s*640px\)\s*\{[\s\S]*\.shorts-slide__buffering\s*\{[\s\S]*--shorts-spinner-size:\s*24px;[\s\S]*width:\s*56px;[\s\S]*height:\s*56px;/
  );
});

test("shorts preloads the next two original videos only after the active video has comfortable buffer", () => {
  assert.match(shortsPageSource, /const \[activeReadyForPreload, setActiveReadyForPreload\] = useState\(false\);/);
  assert.match(mediaBufferSource, /const ACTIVE_PRELOAD_BUFFER_SECONDS = 12;/);
  assert.match(shortsPageSource, /const PRELOAD_AHEAD_COUNT = 2;/);
  assert.match(
    shortsPageSource,
    /const preloadOffset = index - activeIndex;[\s\S]*?preloadOffset > 0 &&[\s\S]*?preloadOffset <= PRELOAD_AHEAD_COUNT;/
  );
  assert.match(shortsPageSource, /const shouldLoad = isActiveSlide \|\| shouldPreload \|\| shouldRetainCached;/);
  assert.match(shortsPageSource, /shouldLoad=\{shouldLoad\}/);
  assert.match(shortsPageSource, /setActiveReadyForPreload\(false\);\s*setActiveIndex\(bestIndex\);/);
  assert.match(shortsPageSource, /function syncActivePreloadReadiness\(currentVideo: HTMLVideoElement\)/);
  assert.match(shortsPageSource, /if \(videoHasComfortableBuffer\(currentVideo\)\) \{\s*onActiveReadyForPreload\(index\);/);
  assert.match(shortsPageSource, /if \(isActive\) onActiveNeedsPriority\(index\);/);
  assert.match(shortsPageSource, /video\.addEventListener\("progress", handleProgress\);/);
  assert.match(shortsPageSource, /src=\{shouldLoad \? item\.videoSrc : undefined\}/);
  assert.match(shortsPageSource, /video\.removeAttribute\("src"\)/);
  assert.doesNotMatch(shortsPageSource, /src=\{shouldLoad \? item\.previewSrc/);
});

test("shorts preload grant uses high/low watermark hysteresis", () => {
  // 高水位 12s 授权、低水位 4s 收回，之间维持现状，避免阈值附近抖动。
  // 判定本身的行为矩阵见 shortsMediaBuffer.test.ts；这里钉住页面接线。
  assert.match(mediaBufferSource, /const ACTIVE_PRELOAD_KEEP_SECONDS = 4;/);
  assert.match(
    shortsPageSource,
    /\} else if \(videoBufferIsCritical\(currentVideo\)\) \{[\s\S]*?onActiveNeedsPriority\(index\);/
  );
  assert.match(mediaBufferSource, /function videoBufferIsCritical\(video: BufferedMediaProbe\)/);
  // 已缓冲到片尾时既视为健康也不视为告急，避免临近结尾误收回授权
  assert.match(mediaBufferSource, /function videoBufferedToEnd\(video: BufferedMediaProbe\)/);
  assert.match(
    mediaBufferSource,
    /if \(videoBufferedToEnd\(video\)\) return true;[\s\S]*?>= ACTIVE_PRELOAD_BUFFER_SECONDS;/
  );
  assert.match(
    mediaBufferSource,
    /if \(videoBufferedToEnd\(video\)\) return false;[\s\S]*?< ACTIVE_PRELOAD_KEEP_SECONDS;/
  );
});

test("shorts only advances viewed cursors and waits for queue end before starting a new feed", () => {
  assert.doesNotMatch(shortsPageSource + shortsFeedSource + useShortsFeedSource, /seenIdsRef|saveSeenIds/);
  assert.match(
    useShortsFeedSource,
    /const persistedFeedHighIndexRef = useRef\(-1\);/
  );
  assert.match(
    useShortsFeedSource,
    /if \(activeIndex > persistedFeedHighIndexRef\.current\) \{\s*persistedFeedHighIndexRef\.current = activeIndex;\s*saveShortsFeedState\(\{\s*feedToken: active\.feedToken,\s*cursor: active\.feedCursor,/
  );
  // 续播书签只归 feed hook 管；页面的缓存窗口推进不得写书签
  assert.doesNotMatch(shortsPageSource, /saveShortsFeedState/);
  // 换轮时机的行为矩阵见 shortsFeedLogic.test.ts 的 planShortsPrefetch 用例
  assert.match(
    shortsFeedSource,
    /if \(input\.roundComplete\) \{\s*return input\.remainingAfterActive > 0 \? "none" : "new-round";/
  );
  assert.match(
    useShortsFeedSource,
    /if \(plan === "new-round"\) \{\s*requestFeedRef\.current = EMPTY_SHORTS_FEED;\s*setRoundComplete\(false\);\s*\}\s*void loadMore\(\);/
  );
});

test("shorts distinguishes feed failures from a genuinely empty library", () => {
  // 恢复流程（令牌失效 / 快照耗尽 / 空库）的行为用例见 shortsFeedLogic.test.ts
  assert.match(
    shortsFeedSource,
    /if \(resp\.total === 0\) \{\s*return \{ kind: "empty" \};/
  );
  assert.match(
    useShortsFeedSource,
    /if \(outcome\.kind === "empty"\) \{\s*setEmpty\(true\);[\s\S]*?setItems\(\[\]\);\s*onQueueResetRef\.current\(\);[\s\S]*?setRoundComplete\(false\);\s*requestFeedRef\.current = EMPTY_SHORTS_FEED;\s*clearShortsFeedState\(\);\s*return;/
  );
  assert.match(
    shortsPageSource,
    /const handleQueueReset = useCallback\(\(\) => setActiveIndex\(0\), \[\]\);/
  );
  assert.match(
    useShortsFeedSource,
    /useEffect\(\(\) => \{\s*if \(empty\) return;\s*const active = items\[activeIndex\];/
  );
  assert.match(useShortsFeedSource, /catch \{\s*setLoadError\(true\);/);
  assert.match(shortsPageSource, /短视频加载失败，请检查网络后重试/);
  assert.match(shortsPageSource, /onClick=\{\(\) => void loadMore\(\)\}/);
  assert.doesNotMatch(
    videosDataSource,
    /\.catch\(\(\) => \(\{ items: \[\], total: 0/
  );
});

test("shorts empty library reuses the homepage empty visual", () => {
  assert.match(
    shortsPageSource,
    /import \{ AdminEmptyVisual \} from "@\/admin\/AdminEmptyVisual";/
  );
  assert.match(
    shortsPageSource,
    /\{empty && items\.length === 0 && \([\s\S]*?<AdminEmptyVisual[\s\S]*?variant="empty"[\s\S]*?text="当前库中没有视频"[\s\S]*?className="shorts-empty__visual"/
  );
  assert.match(
    shortsPageSource,
    /className="shorts-header__actions">\s*\{items\.length > 0 && \(/
  );
  assert.doesNotMatch(shortsPageSource, /当前没有可播放的视频/);
  assert.match(
    shortsCssSource,
    /\.shorts-empty__visual \.admin-empty-visual__text\s*\{[^}]*color:\s*rgba\(255,\s*255,\s*255,\s*0\.72\);/
  );
});

test("shorts hidden overlay keeps only the concise confirmation", () => {
  assert.match(shortsPageSource, /shorts-slide__hidden-title">已隐藏该视频/);
  assert.doesNotMatch(
    shortsPageSource,
    /系统将不会再次在任何地方向您展示此视频|shorts-slide__hidden-desc/
  );
  assert.doesNotMatch(shortsCssSource, /\.shorts-slide__hidden-desc/);
});

test("shorts hide action is icon-only and advances without a toast", () => {
  assert.match(
    shortsPageSource,
    /aria-label="不再展示"[\s\S]*?<EyeOff size=\{22\} \/>/
  );
  assert.doesNotMatch(
    shortsPageSource,
    /<span className="shorts-slide__action-count">隐藏<\/span>/
  );
  assert.doesNotMatch(
    shortsPageSource,
    /已选择不再展示，正在滑至下一首/
  );
  assert.match(
    shortsPageSource,
    /const handleHideSuccess = useCallback\(\(idx: number\) => \{[\s\S]*?nextSlide\.scrollIntoView\(\{ behavior: "smooth" \}\);[\s\S]*?\}, \[items\.length\]\);/
  );
});

test("shorts creates and copies the existing one-time video share", () => {
  assert.match(
    shortsPageSource,
    /copyExistingVideoShareURL,[\s\S]*?createAndCopyVideoShare/
  );
  assert.match(
    shortsPageSource,
    /async function handleShareClick[\s\S]*?createAndCopyVideoShare\(item\.id\)[\s\S]*?pendingShareURLRef\.current = result\.url[\s\S]*?showHud\("请再次点击分享按钮"\)[\s\S]*?showHud\("一次性分享链接已复制"\)/
  );
  assert.match(
    shortsPageSource,
    /aria-label="生成并复制一次性分享链接"[\s\S]*?disabled=\{isSharing\}[\s\S]*?onClick=\{handleShareClick\}[\s\S]*?<Share2 size=\{22\} \/>/
  );
  assert.match(
    shortsPageSource,
    /copyExistingVideoShareURL\(pendingShareURLRef\.current\)/
  );
  assert.match(shareClipboardSource, /navigator\.clipboard\?\.writeText/);
  assert.match(shareClipboardSource, /document\.execCommand\("copy"\)/);
  assert.match(
    videosDataSource,
    /`\/api\/video\/\$\{encodeURIComponent\(id\)\}\/share`/
  );
  assert.doesNotMatch(shortsPageSource, /\/shorts\/share|ShortsSharePage/);
});

test("shorts like action does not display a count", () => {
  assert.doesNotMatch(shortsPageSource, /shorts-slide__action-count/);
  assert.doesNotMatch(shortsPageSource, /function formatCount\(/);
  assert.doesNotMatch(shortsCssSource, /\.shorts-slide__action-count/);
});

test("shorts keeps buffered sources inside a six video window", () => {
  assert.match(shortsPageSource, /const \[cacheableSourceIds, setCacheableSourceIds\] = useState<Set<string>>/);
  assert.match(shortsPageSource, /setCacheableSourceIds\(\(prev\) => \{/);
  assert.match(mediaBufferSource, /const VIDEO_WINDOW_SIZE = 6;/);
  assert.doesNotMatch(shortsPageSource + mediaBufferSource, /VIDEO_WINDOW_BACKWARD_BIAS/);
  assert.match(shortsPageSource, /const \[cacheWindowHighIndex, setCacheWindowHighIndex\] = useState\(-1\);/);
  assert.match(shortsPageSource, /setCacheWindowHighIndex\(\(prev\) => Math\.max\(prev, activeIndex\)\);/);
  // 窗口边界计算的行为用例见 shortsMediaBuffer.test.ts
  assert.match(mediaBufferSource, /function getVideoWindowBounds\(highestViewedIndex: number, itemCount: number\)/);
  assert.match(
    shortsPageSource,
    /const videoWindow = getVideoWindowBounds\(cacheWindowHighIndex, items\.length\);/
  );
  assert.match(
    shortsPageSource,
    /const isInCacheWindow =\s*index >= videoWindow\.start && index <= videoWindow\.end;/
  );
  assert.match(
    shortsPageSource,
    /const shouldMount =\s*isActiveSlide \|\|\s*\(!useIOSSharedVideo && \(isInCacheWindow \|\| shouldPreload\)\);/
  );
  // 视频窗口内已缓冲过的视频都保留 src，来回切换均复用缓存
  assert.match(
    shortsPageSource,
    /const shouldRetainCached =\s*!useIOSSharedVideo &&\s*isInCacheWindow &&\s*!isActiveSlide &&\s*cacheableSourceIds\.has\(item\.id\);/
  );
  // 窗口内视频一旦 canplay 就标记可复用，快速划走的视频回滑也有缓存
  assert.match(
    shortsPageSource,
    /if \(shouldLoad\) onSourceCached\(item\.id\);/
  );
  // 窗口内视频只要已经产生缓冲就同样标记，授权收回时不丢弃其数据
  assert.match(
    shortsPageSource,
    /if \(shouldLoad && videoHasBufferedData\(video\)\) \{\s*onSourceCached\(item\.id\);/
  );
  const playbackBlock = /\/\/ 先停掉所有非当前屏[\s\S]*?\}, \[activeIndex, items\.length\]\);/.exec(shortsPageSource);
  assert.ok(playbackBlock, "parent inactive-video pause effect should be present");
  assert.doesNotMatch(playbackBlock[0], /video\.play\(\)/);
  assert.doesNotMatch(playbackBlock[0], /currentTime\s*=\s*0/);
  assert.match(shortsPageSource, /shouldEagerLoad=\{shouldEagerLoad\}/);
  assert.match(shortsPageSource, /preload=\{shouldLoad \? \(shouldEagerLoad \? "auto" : "metadata"\) : "none"\}/);
});

test("shorts reuses one persistent media element across iOS slides", () => {
  assert.match(shortsPageSource, /const useIOSSharedVideo = shouldUseIOSSharedVideo\(\);/);
  assert.match(shortsPlatformSource, /function shouldUseIOSSharedVideo\(\)/);
  assert.match(shortsPlatformSource, /\\biPhone\\b\|\\biPad\\b\|\\biPod\\b/);
  assert.match(shortsPlatformSource, /navigator\.platform === "MacIntel" && navigator\.maxTouchPoints > 1/);
  assert.match(
    shortsPageSource,
    /const shouldPreload =\s*!useIOSSharedVideo &&\s*activeReadyForPreload/
  );
  assert.match(shortsPageSource, /const iosSharedVideoRef = useRef<HTMLVideoElement \| null>\(null\);/);
  assert.match(shortsPageSource, /if \(!video\) \{\s*video = document\.createElement\("video"\);/);
  assert.match(shortsPageSource, /slot\.appendChild\(video\);/);
  assert.match(
    shortsPageSource,
    /video\.dataset\.shortsVideoId = item\.id;[\s\S]*?video\.src = item\.videoSrc;[\s\S]*?video\.load\(\);/
  );
  assert.match(shortsPageSource, /className="shorts-slide__ios-video-slot"/);
  assert.match(shortsPageSource, /sharedVideoRef=\{\s*useIOSSharedVideo \? iosSharedVideoRef : undefined/);
  assert.match(
    shortsCssSource,
    /\.shorts-slide__video--ios-shared\s*\{[\s\S]*?z-index:\s*2;/
  );
  assert.doesNotMatch(shortsPageSource, /key=\{item\.id\}[\s\S]{0,300}document\.createElement\("video"\)/);
});

test("stale iOS play work cannot control a later shared source", () => {
  assert.match(shortsPageSource, /let disposed = false;/);
  assert.match(shortsPageSource, /disposed = true;/);
  assert.match(shortsPageSource, /if \(retryTimer !== null\) window\.clearTimeout\(retryTimer\);/);
  assert.match(
    shortsPageSource,
    /getVideoElement\(\) === video[\s\S]*?video\.dataset\.shortsVideoId === item\.id/
  );
  assert.match(
    shortsPageSource,
    /const belongsToSlide = \(\) =>[\s\S]*?video\.dataset\.shortsVideoId === item\.id/
  );
});

test("iOS loops restart under app control and progress follows presented frames", () => {
  assert.match(shortsPageSource, /video\.loop = false;/);
  assert.match(shortsPageSource, /const loopRestartPendingRef = useRef\(false\);/);
  assert.match(shortsPageSource, /const handleIOSLoopEnded = \(\) => \{/);
  assert.match(shortsPageSource, /video\.addEventListener\("ended", handleIOSLoopEnded\);/);
  assert.match(
    shortsPageSource,
    /loopRestartPendingRef\.current = true;[\s\S]*?setCurrentTime\(0\);[\s\S]*?setIsBuffering\(true\);[\s\S]*?video\.currentTime = 0;/
  );
  assert.match(
    shortsPageSource,
    /const handleIOSLoopEnded = \(\) => \{[\s\S]*?if \(loopRestartPendingRef\.current\) \{\s*failRestart\(loopRestartAttemptRef\.current\);\s*return;/
  );
  assert.match(shortsPageSource, /const IOS_LOOP_FRAME_WATCHDOG_MS = \d+;/);
  assert.match(shortsPageSource, /const IOS_LOOP_RELOAD_TIMEOUT_MS = \d+;/);
  assert.match(shortsPageSource, /\}, timeoutMs\);/);
  assert.match(
    shortsPageSource,
    /if \(loopRestartReloadedRef\.current\) \{[\s\S]*?failRestart\(attempt\);[\s\S]*?loopRestartReloadedRef\.current = true;[\s\S]*?loopFrameBarrierRef\.current = null;[\s\S]*?video\.load\(\);/
  );
  assert.match(shortsPageSource, /video\.requestVideoFrameCallback\(handlePresentedFrame\)/);
  assert.match(shortsPageSource, /const mediaTime = metadata\.mediaTime;/);
  assert.match(
    shortsPageSource,
    /loopRestartPendingRef\.current[\s\S]*?metadata\.presentationTime >= frameBarrier/
  );
  assert.match(shortsPageSource, /loopFrameBarrierRef\.current = performance\.now\(\);/);
  assert.match(
    shortsPageSource,
    /if \(canObservePresentedFrames\) \{[\s\S]*?video\.currentTime = 0;[\s\S]*?\} else \{[\s\S]*?loopRestartReloadedRef\.current = true;[\s\S]*?video\.load\(\);/
  );
  assert.match(shortsPageSource, /confirmPresentedPlayback\(mediaTime\);/);
  assert.match(shortsPageSource, /video\.cancelVideoFrameCallback\(frameCallbackId\);/);

  const playingStart = shortsPageSource.indexOf("const handlePlaying = () => {");
  const playingEnd = shortsPageSource.indexOf("const handleProgress = () => {", playingStart);
  assert.ok(playingStart >= 0 && playingEnd > playingStart);
  const playingBlock = shortsPageSource.slice(playingStart, playingEnd);
  const waitingForFrameBranch =
    /if \(waitForIOSPlaybackMotion\) \{([\s\S]*?)\} else \{([\s\S]*?)\}/.exec(
      playingBlock
    );
  assert.ok(waitingForFrameBranch);
  assert.doesNotMatch(waitingForFrameBranch[1], /confirmPresentedPlayback|setIsBuffering\(false\)/);
  assert.match(waitingForFrameBranch[2], /confirmPresentedPlayback\(\);/);

  const confirmationStart = shortsPageSource.indexOf(
    "const confirmPresentedPlayback = useCallback("
  );
  const confirmationEnd = shortsPageSource.indexOf(
    "// 是否已点过赞。真正的防重",
    confirmationStart
  );
  assert.ok(confirmationStart >= 0 && confirmationEnd > confirmationStart);
  const confirmationBlock = shortsPageSource.slice(
    confirmationStart,
    confirmationEnd
  );
  assert.match(confirmationBlock, /clearLoopRestartWatchdog\(\);/);
  assert.match(confirmationBlock, /loopRestartPendingRef\.current = false;/);
  assert.match(confirmationBlock, /loopRestartAwaitingFrameRef\.current = false;/);
  assert.match(confirmationBlock, /hasStartedPlayingRef\.current = true;/);
  assert.match(confirmationBlock, /setIsBuffering\(false\);/);
  assert.match(confirmationBlock, /setCurrentTime\(mediaTime\);/);
  assert.match(shortsPageSource, /const presentedFrameAdvanced =/);
  assert.match(
    shortsPageSource,
    /if \(playbackNeedsMotionConfirmation\) \{[\s\S]*?playbackMotionFrameCountRef\.current \+= 1;[\s\S]*?playbackMotionFrameCountRef\.current >= 2[\s\S]*?confirmPresentedPlayback\(mediaTime\);/
  );

  // The ordinary per-slide videos keep native looping; only the iOS shared
  // element uses the controlled restart path.
  assert.match(
    shortsPageSource,
    /<video[\s\S]*?autoPlay=\{isActive\}[\s\S]*?playsInline[\s\S]*?loop[\s\S]*?muted=\{muted\}/
  );
});

test("shorts buffering state survives stalled and self-heals on real progress", () => {
  const stalledStart = shortsPageSource.indexOf("const handleStalled = () => {");
  const stalledEnd = shortsPageSource.indexOf("const handleError = () => {", stalledStart);
  assert.ok(stalledStart >= 0 && stalledEnd > stalledStart);
  const stalledBlock = shortsPageSource.slice(stalledStart, stalledEnd);
  assert.doesNotMatch(stalledBlock, /setIsBuffering\(true\)/);
  assert.doesNotMatch(stalledBlock, /hasStartedPlayingRef\.current = false/);
  assert.match(stalledBlock, /onActiveNeedsPriority\(index\);/);

  const timeStart = shortsPageSource.indexOf("const handleTime = () => {");
  const timeEnd = shortsPageSource.indexOf("const handleWaiting = () => {", timeStart);
  assert.ok(timeStart >= 0 && timeEnd > timeStart);
  const timeBlock = shortsPageSource.slice(timeStart, timeEnd);
  assert.match(timeBlock, /const mediaTimeAdvanced =/);
  assert.match(
    timeBlock,
    /if \(\s*!usesPresentedFrameProgress &&\s*!loopRestartPendingRef\.current &&\s*!video\.seeking &&\s*!scrubbingRef\.current\s*\) \{\s*setCurrentTime\(mediaTime\);/
  );
  assert.match(timeBlock, /!video\.paused/);
  assert.match(timeBlock, /!video\.seeking/);
  assert.match(timeBlock, /confirmPresentedPlayback\(mediaTime\);/);

  const waitingStart = shortsPageSource.indexOf("const handleWaiting = () => {");
  const waitingEnd = shortsPageSource.indexOf("const cacheAvailableSource", waitingStart);
  assert.ok(waitingStart >= 0 && waitingEnd > waitingStart);
  const waitingBlock = shortsPageSource.slice(waitingStart, waitingEnd);
  assert.match(waitingBlock, /SHORTS_BUFFERING_INDICATOR_DELAY_MS/);
  assert.match(waitingBlock, /hasStartedPlayingRef\.current/);
});

test("shorts grants preload only after the active video really started", () => {
  assert.match(shortsPageSource, /const hasStartedPlayingRef = useRef\(false\);/);
  assert.match(
    shortsPageSource,
    /const confirmPresentedPlayback = useCallback\([\s\S]*?hasStartedPlayingRef\.current = true;/
  );
  assert.match(
    shortsPageSource,
    /const handlePlaying = \(\) => \{[\s\S]*?confirmPresentedPlayback\(\);/
  );
  assert.match(
    shortsPageSource,
    /currentVideo\.paused \|\|[\s\S]*?!hasStartedPlayingRef\.current \|\|[\s\S]*?onActiveNeedsPriority\(index\);/
  );
});

test("shorts sound toggle grants playback in the direct user click", () => {
  assert.match(shortsPageSource, /function applyVideoMutedState/);
  assert.doesNotMatch(shortsPageSource, /onFirstPointer/);
  assert.doesNotMatch(shortsPageSource, /currentPage\.addEventListener\("pointerdown"/);
  assert.match(
    shortsPageSource,
    /const stopHeaderControlPropagation = useCallback\(\(e: React\.SyntheticEvent\) => \{\s*e\.stopPropagation\(\);/
  );
  assert.match(shortsPageSource, /onPointerDownCapture=\{stopHeaderControlPropagation\}/);
  assert.match(shortsPageSource, /onTouchStartCapture=\{stopHeaderControlPropagation\}/);
  assert.match(shortsPageSource, /onPointerDown=\{stopHeaderControlPropagation\}/);
  assert.match(shortsPageSource, /onTouchStart=\{stopHeaderControlPropagation\}/);
  assert.match(shortsPageSource, /function normalizeVideoPlaybackRate/);
  assert.match(shortsPageSource, /function stabilizeVideoAfterAudioToggle/);
  assert.match(shortsPageSource, /normalizeVideoPlaybackRate\(activeVideo\);/);
  assert.match(shortsPageSource, /getVideoAtIndex\(activeIndexRef\.current\) === activeVideo/);
  assert.match(
    shortsPageSource,
    /applyVideoMutedState\(activeVideo, next\);[\s\S]*?activeVideo\.play\(\)\.catch[\s\S]*?setMuted\(next\);/
  );
  assert.match(shortsPageSource, /stabilizeVideoAfterAudioToggle\(\s*activeVideo,\s*canResumeActiveVideo\s*\);/);
  assert.match(shortsPageSource, /if \(shouldResume\(\) && video\.paused && !video\.ended\) \{/);
  assert.match(shortsPageSource, /for \(const delay of \[80, 240, 600\]\)/);
  assert.match(
    shortsPageSource,
    /const sharedVideo = iosSharedVideoRef\.current;\s*if \(sharedVideo\) applyVideoMutedState\(sharedVideo, muted\);/
  );
  assert.match(shortsPageSource, /\}, \[muted, items\.length, useIOSSharedVideo\]\);/);
});

test("shorts leaves loudness to the system and only exposes mute", () => {
  assert.match(
    shortsPageSource,
    /<button[\s\S]*?className="shorts-header__icon-btn"[\s\S]*?aria-label=\{muted \? "取消静音" : "静音"\}[\s\S]*?handleMuteButtonClick\(\);/
  );
  assert.doesNotMatch(shortsPageSource, /type="range"/);
  assert.doesNotMatch(shortsPageSource, /handleVolumeSliderChange|setVolume|volumeRef/);
  assert.doesNotMatch(shortsPageSource, /video\.volume\s*=/);
  assert.doesNotMatch(shortsCssSource, /shorts-header__volume-slider|shorts-header__volume-group/);
  assert.match(
    shortsPageSource,
    /function applyVideoMutedState\(video: HTMLVideoElement, nextMuted: boolean\) \{[\s\S]*?video\.muted = nextMuted;/
  );
});

test("Windows viewport resize keeps the current short aligned", () => {
  assert.match(
    shortsPageSource,
    /const isWindowsShortsPlatform = isWindowsPlatform\(\);/
  );
  assert.match(
    shortsPlatformSource,
    /function isWindowsPlatform\(\) \{[\s\S]*?\/\^Win\/i\.test\(platform\) \|\| \/\\bWindows\\b\/i\.test\(ua\);/
  );
  assert.match(
    shortsPageSource,
    /const viewportResizeAnchorIndexRef = useRef<number \| null>\(null\);/
  );
  assert.match(
    shortsPageSource,
    /const handleViewportResize = \(\) => \{[\s\S]*?viewportResizeAnchorIndexRef\.current = activeIndexRef\.current;[\s\S]*?alignAnchoredSlide\(\);[\s\S]*?window\.requestAnimationFrame/
  );
  assert.match(
    shortsPageSource,
    /root\.scrollTop = activeSlide\.offsetTop;/
  );
  assert.match(
    shortsPageSource,
    /window\.addEventListener\("resize", handleViewportResize\);/
  );
  assert.match(
    shortsPageSource,
    /document\.addEventListener\("fullscreenchange", handleViewportResize\);/
  );
  assert.match(
    shortsPageSource,
    /const observer = new IntersectionObserver\(\s*\(entries\) => \{\s*if \(viewportResizeAnchorIndexRef\.current !== null\) return;/
  );
});

test("shorts back button exits document fullscreen before showing home", () => {
  assert.match(shortsPageSource, /import \{ Link, useNavigate \} from "react-router-dom";/);
  assert.match(shortsPageSource, /const navigate = useNavigate\(\);/);
  assert.match(
    shortsPageSource,
    /const handleBackToHomeClick = useCallback\([\s\S]*?const exitRequest = exitDocumentFullscreen\(\);[\s\S]*?if \(!exitRequest\) return;[\s\S]*?event\.preventDefault\(\);[\s\S]*?exitRequest\.then\(returnHome, returnHome\)/
  );
  assert.match(
    shortsPageSource,
    /function exitDocumentFullscreen\(\): Promise<void> \| null \{[\s\S]*?fullscreenDocument\.fullscreenElement \?\?[\s\S]*?fullscreenDocument\.webkitFullscreenElement[\s\S]*?fullscreenDocument\.exitFullscreen\?\.bind[\s\S]*?fullscreenDocument\.webkitExitFullscreen\?\.bind[\s\S]*?Promise\.resolve\(exitFullscreen\(\)\)/
  );
  assert.match(
    shortsPageSource,
    /<Link\s*to="\/"[\s\S]*?className="shorts-header__back"[\s\S]*?onClick=\{handleBackToHomeClick\}/
  );
});

test("shorts page defaults to immersive playback without fullscreen controls", () => {
  assert.match(shortsPageSource, /const activeIndexRef = useRef\(0\)/);
  assert.match(shortsCssSource, /\.shorts-page \{[\s\S]*height:\s*100svh/);
  assert.match(shortsPageSource, /html\.style\.overflow = "hidden"/);
  assert.match(shortsPageSource, /body\.style\.overflow = "hidden"/);
  assert.match(shortsPageSource, /body\.style\.background = "#000"/);
  assert.doesNotMatch(shortsPageSource, /Maximize/);
  assert.doesNotMatch(shortsPageSource, /Minimize/);
  assert.doesNotMatch(shortsPageSource, /aria-label=\{isFullscreen \? "退出全屏" : "进入全屏"\}/);
  assert.doesNotMatch(shortsPageSource, /e\.key === "f"/);
  assert.doesNotMatch(shortsPageSource, /requestFullscreen/);
});
