import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const appSource = readFileSync(
  new URL("../src/App.tsx", import.meta.url),
  "utf8"
);
const actionsSource = readFileSync(
  new URL("../src/components/VideoActions.tsx", import.meta.url),
  "utf8"
);
const dataSource = readFileSync(
  new URL("../src/data/videos.ts", import.meta.url),
  "utf8"
);
const shareClipboardSource = readFileSync(
  new URL("../src/lib/videoShareClipboard.ts", import.meta.url),
  "utf8"
);
const sharePageSource = readFileSync(
  new URL("../src/pages/SharedVideoPage.tsx", import.meta.url),
  "utf8"
);
const shareStylesSource = readFileSync(
  new URL("../src/styles/video-detail.css", import.meta.url),
  "utf8"
);
const baseStylesSource = readFileSync(
  new URL("../src/styles/base.css", import.meta.url),
  "utf8"
);

test("one-time share page is public and claims the fragment token", () => {
  assert.match(appSource, /path="\/share"/);
  assert.match(appSource, /<SharedVideoPage \/>/);
  assert.match(sharePageSource, /location\.hash\.slice\(1\)/);
  assert.match(sharePageSource, /consumeVideoShare\(token\)/);
  assert.doesNotMatch(
    appSource,
    /path="\/share"[\s\S]{0,180}<RequireAuth>/
  );
  assert.doesNotMatch(appSource, /path="\/tmp"/);
});

test("share page loads subtitles only from the player subtitle menu", () => {
  const claimBlock = /consumeVideoShare\(token\)([\s\S]*?)\.catch\(/.exec(
    sharePageSource
  );
  assert.ok(claimBlock);
  assert.doesNotMatch(claimBlock[1], /fetchSharedVideoSubtitles/);
  assert.match(
    sharePageSource,
    /function loadSubtitles\(\)[\s\S]*?fetchSharedVideoSubtitles\(claim\.shareId\)/
  );
  assert.match(sharePageSource, /loadSubtitles=\{loadSubtitles\}/);
});

test("used share page shows the supplied illustration and concise message", () => {
  assert.match(sharePageSource, /share-link-used\.webp/);
  assert.match(sharePageSource, /当前链接已失效/);
  assert.match(sharePageSource, /share-page__state--bare/);
  assert.doesNotMatch(sharePageSource, /<Link2Off|这个一次性链接已被使用/);
});

test("share loading state only shows an unframed spinner", () => {
  assert.match(
    sharePageSource,
    /loadState === "loading"[\s\S]*?share-page__state--bare[\s\S]*?share-page__spinner/
  );
  assert.doesNotMatch(sharePageSource, /正在领取分享视频|领取成功后/);
});

test("share load error has no outer frame or network hint", () => {
  assert.match(
    sharePageSource,
    /loadState === "error"[\s\S]*?share-page__state--bare[\s\S]*?src=\{linkUsedImage\}[\s\S]*?暂时无法加载/
  );
  assert.doesNotMatch(sharePageSource, /网络连接可能不稳定/);
  assert.doesNotMatch(sharePageSource, /重新加载/);
  assert.doesNotMatch(sharePageSource, /RefreshCw/);
});

test("loading, unavailable, and error share states are centered in the page", () => {
  assert.match(
    sharePageSource,
    /loadState === "loading" \|\|[\s\S]*?loadState === "unavailable" \|\|[\s\S]*?loadState === "error"[\s\S]*?share-page__main--centered/
  );
});

test("share page fits the visible mobile viewport without a forced scrollbar", () => {
  assert.match(
    shareStylesSource,
    /\.share-page\s*\{[\s\S]*?min-height:\s*100dvh;/
  );
  assert.match(baseStylesSource, /html\s*\{[\s\S]*?overflow-y:\s*auto;/);
  assert.doesNotMatch(baseStylesSource, /html\s*\{[\s\S]*?overflow-y:\s*scroll;/);
});

test("share header logo aligns with the video content and clears the safe area", () => {
  assert.match(
    shareStylesSource,
    /\.share-page\s*\{\s*--share-page-content-max:\s*1040px;/
  );
  assert.match(
    shareStylesSource,
    /\.share-page__header\s*\{[\s\S]*?padding-top:\s*env\(safe-area-inset-top, 0px\);/
  );
  assert.match(
    shareStylesSource,
    /\.share-page__header-inner\s*\{\s*max-width:\s*var\(--share-page-content-max\);/
  );
  assert.match(
    shareStylesSource,
    /\.share-page__main\s*\{[\s\S]*?max-width:\s*var\(--share-page-content-max\);/
  );
});

test("the share footer text links to the project repository", () => {
  assert.match(
    sharePageSource,
    /href="https:\/\/github\.com\/nianzhibai\/91"[\s\S]*?>\s*© \{new Date\(\)\.getFullYear\(\)\} 91\s*<\/a>/
  );
  assert.match(
    shareStylesSource,
    /\.share-page__footer a\s*\{[\s\S]*?display:\s*inline-block;/
  );
  assert.doesNotMatch(
    shareStylesSource,
    /\.share-page__footer a\s*\{[^}]*width:\s*100%;/
  );
});

test("share creation copies a newly generated one-time URL", () => {
  assert.match(actionsSource, /createAndCopyVideoShare\(video\.id\)/);
  assert.match(shareClipboardSource, /createVideoShare\(videoID\)/);
  assert.match(actionsSource, /生成并复制一次性分享链接/);
  assert.match(actionsSource, /<Share2 size=\{16\} \/>/);
  assert.match(actionsSource, /:\s*"分享"\}/);
  assert.doesNotMatch(actionsSource, /:\s*"一次性分享"\}/);
  assert.match(shareClipboardSource, /navigator\.clipboard\?\.writeText/);
  assert.match(shareClipboardSource, /document\.execCommand\("copy"\)/);
  assert.match(actionsSource, /scheduleShareStateReset\(1500\)/);
});

test("successful mobile share shows a bottom-right confirmation toast", () => {
  assert.match(
    actionsSource,
    /shareState === "copied" \|\| shareState === "copy-ready"[\s\S]*createPortal\(/
  );
  assert.match(
    actionsSource,
    /shareState === "copied"[\s\S]*?"已复制一次性分享链接"[\s\S]*?"请再次点击分享按钮"/
  );
  assert.doesNotMatch(
    actionsSource,
    /className="vd-share-toast"[\s\S]*?<Check[\s\S]*?<\/div>/
  );
  assert.match(actionsSource, /className="vd-share-toast"[\s\S]*role="status"/);
  assert.match(
    shareStylesSource,
    /\.vd-share-toast\s*\{\s*display:\s*none;/s
  );
  assert.match(
    shareStylesSource,
    /@media \(max-width:\s*768px\)\s*\{[\s\S]*?\.vd-share-toast\s*\{[^}]*position:\s*fixed[^}]*right:\s*calc\(16px \+ env\(safe-area-inset-right, 0px\)\)[^}]*bottom:\s*calc\(16px \+ env\(safe-area-inset-bottom, 0px\)\)[^}]*z-index:\s*var\(--z-toast\)[^}]*display:\s*block/s
  );
});

test("iOS starts deferred clipboard writing before awaiting share creation", () => {
  assert.match(
    shareClipboardSource,
    /const shareURLPromise = createVideoShare\(videoID\)[\s\S]*?const clipboardWrite = startShareClipboardWrite\(shareURLPromise\);[\s\S]*?await Promise\.all/
  );
  assert.match(
    shareClipboardSource,
    /new ClipboardItem\(\{ "text\/plain": textBlob \}\)/
  );
  assert.match(
    shareClipboardSource,
    /valuePromise\.then\([\s\S]*?new Blob\(\[value\], \{ type: "text\/plain" \}\)/
  );
  assert.match(actionsSource, /pendingShareURL\.current = result\.url/);
  assert.match(actionsSource, /copyExistingVideoShareURL\(pendingShareURL\.current\)/);
});

test("share claim token stays out of the request URL and duplicate effects share one promise", () => {
  assert.match(dataSource, /fetch\("\/api\/share\/consume"/);
  assert.match(dataSource, /body:\s*JSON\.stringify\(\{ token \}\)/);
  assert.match(dataSource, /const pendingVideoShareClaims = new Map/);
  assert.match(dataSource, /const existing = pendingVideoShareClaims\.get\(token\)/);
  assert.doesNotMatch(dataSource, /api\/share\/\$\{encodeURIComponent\(token\)\}/);
});

test("successful claim removes the one-time token from the address bar", () => {
  assert.match(sharePageSource, /window\.history\.replaceState/);
  assert.doesNotMatch(sharePageSource, /请勿刷新或关闭当前页面/);
  assert.doesNotMatch(sharePageSource, /你可以在24小时内反复观看当前视频/);
});
