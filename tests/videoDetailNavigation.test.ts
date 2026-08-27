import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  consumePrefetchedVideoDetail,
  consumePrefetchedVideoRecommendations,
  prefetchVideoDetail,
  prefetchVideoRecommendations,
} from "../src/data/videos.ts";

const appSource = readFileSync(
  new URL("../src/App.tsx", import.meta.url),
  "utf8"
);
const routeSource = readFileSync(
  new URL("../src/lib/videoDetailRoute.ts", import.meta.url),
  "utf8"
);
const gridSource = readFileSync(
  new URL("../src/components/VideoGrid.tsx", import.meta.url),
  "utf8"
);
const cardSource = readFileSync(
  new URL("../src/components/VideoCard.tsx", import.meta.url),
  "utf8"
);
const detailPageSource = readFileSync(
  new URL("../src/pages/VideoDetailPage.tsx", import.meta.url),
  "utf8"
);

test("video detail route preloads once and always has a visible route fallback", () => {
  assert.match(routeSource, /let routeModulePromise:/);
  assert.match(routeSource, /routeModulePromise = import\("@\/pages\/VideoDetailPage"\)/);
  assert.match(routeSource, /window\.requestIdleCallback\(preload, \{ timeout: 1_500 \}\)/);
  assert.match(appSource, /const VideoDetailPage = lazy\(loadVideoDetailPage\)/);
  assert.match(
    appSource,
    /path="\/video\/:id"[\s\S]*?<PageSuspense fallback=\{<VideoDetailRouteFallback \/>\}>[\s\S]*?<VideoDetailPage \/>/
  );
  assert.match(
    appSource,
    /function VideoDetailRouteFallback\(\)[\s\S]*?<VideoDetailLoading isAdmin=\{isAdmin\} \/>/
  );
  assert.match(gridSource, /useEffect\(\(\) => \{\s*scheduleVideoDetailPagePreload\(\)/);
});

test("video cards start independent detail and recommendation requests for confirmed navigation", () => {
  assert.match(
    cardSource,
    /function prepareDetailNavigation\(\) \{\s*preloadVideoDetailPage\(\);\s*void prefetchVideoDetail\(video\.id\)/
  );
  assert.match(
    cardSource,
    /function handlePointerDown[\s\S]*?prepareDetailNavigation\(\)/
  );
  assert.match(
    cardSource,
    /function prepareConfirmedDetailNavigation\(\) \{\s*prepareDetailNavigation\(\);\s*void prefetchVideoRecommendations\(video\.id\)/
  );
  assert.match(
    cardSource,
    /if \(\s*!shouldInterceptPreviewTap\([\s\S]*?prepareConfirmedDetailNavigation\(\);\s*return;[\s\S]*?event\.preventDefault\(\);[\s\S]*?startTouchPreviewIntent\(\)/
  );
  assert.match(
    detailPageSource,
    /const prefetchedDetail = consumePrefetchedVideoDetail\(id\)[\s\S]*?detailRequest\.then\(\(d\) =>[\s\S]*?setDetail\(stableDetail\);[\s\S]*?setLoading\(false\)/
  );
  assert.match(
    detailPageSource,
    /const prefetchedRecommendations =[\s\S]*?consumePrefetchedVideoRecommendations\(id\)[\s\S]*?const recommendationsRequest =[\s\S]*?fetchVideoRecommendations\(id, \{ signal: controller\.signal \}\)/
  );
  assert.doesNotMatch(detailPageSource, /Promise\.all\(\[detailRequest, fetchTags\(\)\]\)/);
  assert.doesNotMatch(
    detailPageSource,
    /Promise\.all\(\[[^\]]*detailRequest[^\]]*recommendationsRequest/
  );
  assert.match(
    detailPageSource,
    /if \(!isAdmin\) \{[\s\S]*?return;[\s\S]*?fetchTags\(\)/
  );
});

test("a touch preview intent delays media creation but still arms navigation", () => {
  assert.match(
    cardSource,
    /function startTouchPreviewIntent\(\)[\s\S]*?touchPreviewArmedRef\.current = true;[\s\S]*?setPreviewState\("intent"\);[\s\S]*?window\.setTimeout\([\s\S]*?startPreviewNow\(\{ requireInView: false \}\);[\s\S]*?TOUCH_PREVIEW_DELAY_MS/
  );
  assert.match(
    cardSource,
    /const previewActive =[\s\S]*?touchPreviewArmedRef\.current \|\| shouldRenderPreview[\s\S]*?cleanup\(\);[\s\S]*?prepareConfirmedDetailNavigation\(\)/
  );
  const touchPreviewIntentBlock = cardSource.match(
    /function startTouchPreviewIntent\(\)([\s\S]*?)\n  function clearPreviewIntentTimer/
  )?.[1];
  assert.ok(touchPreviewIntentBlock);
  assert.doesNotMatch(touchPreviewIntentBlock, /prefetchVideoRecommendations/);
});

test("recommendation prefetch is shared and consumed by one navigation", async () => {
  const originalFetch = globalThis.fetch;
  const videoID = "recommendation-prefetch-navigation-test";
  let requestCount = 0;
  globalThis.fetch = async (input) => {
    requestCount += 1;
    assert.match(String(input), /\/api\/video\/recommendation-prefetch-navigation-test\/recommendations$/);
    return new Response(JSON.stringify([{ id: "recommended-video" }]), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };

  try {
    const first = prefetchVideoRecommendations(videoID);
    const second = prefetchVideoRecommendations(videoID);
    assert.strictEqual(second, first);
    assert.strictEqual(consumePrefetchedVideoRecommendations(videoID), first);
    assert.equal(consumePrefetchedVideoRecommendations(videoID), null);
    assert.equal((await first)[0]?.id, "recommended-video");
    assert.equal(requestCount, 1);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("detail-data prefetch is shared and consumed by one navigation", async () => {
  const originalFetch = globalThis.fetch;
  const videoID = "prefetch-navigation-test";
  let requestCount = 0;
  globalThis.fetch = async () => {
    requestCount += 1;
    return new Response(JSON.stringify({ id: videoID, title: "预取测试" }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };

  try {
    const first = prefetchVideoDetail(videoID);
    const second = prefetchVideoDetail(videoID);
    assert.strictEqual(second, first);
    assert.strictEqual(consumePrefetchedVideoDetail(videoID), first);
    assert.equal(consumePrefetchedVideoDetail(videoID), null);
    assert.equal((await first)?.id, videoID);
    assert.equal(requestCount, 1);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
