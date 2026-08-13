import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const thumbnailSource = readFileSync(
  new URL("../src/components/VideoThumbnail.tsx", import.meta.url),
  "utf8"
);
const cardSource = readFileSync(
  new URL("../src/components/VideoCard.tsx", import.meta.url),
  "utf8"
);
const gridSource = readFileSync(
  new URL("../src/components/VideoGrid.tsx", import.meta.url),
  "utf8"
);
const homeSource = readFileSync(
  new URL("../src/pages/HomePage.tsx", import.meta.url),
  "utf8"
);
const css = readFileSync(
  new URL("../src/styles/video-card.css", import.meta.url),
  "utf8"
);

test("video thumbnails hide failed images behind a stable lifecycle placeholder", () => {
  assert.match(thumbnailSource, /type ThumbnailState = "loading" \| "retrying" \| "ready" \| "failed"/);
  assert.match(thumbnailSource, /src\.startsWith\("\/p\/thumb\/"\)/);
  assert.match(thumbnailSource, /retry < MAX_LOCAL_THUMBNAIL_RETRIES/);
  assert.match(thumbnailSource, /setState\("failed"\)/);
  assert.match(thumbnailSource, /className={`thumb-image \$\{state === "ready" \? "is-ready" : ""\}`}/);
  assert.match(cardSource, /<VideoThumbnail[\s\S]*?src=\{video\.thumbnail\}/);
  assert.doesNotMatch(cardSource, /<VideoThumbnail[\s\S]*?key=\{video\.thumbnail\}/);
  assert.match(thumbnailSource, /<ThumbnailResource[\s\S]*?key=\{src\}/);
  assert.match(css, /\.thumb-image\s*\{[^}]*opacity:\s*0/s);
  assert.match(css, /\.thumb-image\.is-ready\s*\{[^}]*opacity:\s*1/s);
  assert.match(thumbnailSource, /className="thumb-placeholder"/);
  assert.doesNotMatch(thumbnailSource, /thumb-placeholder__mark/);
  assert.doesNotMatch(css, /\.thumb-placeholder__mark/);
});

test("cached thumbnails reconcile completion before they can remain hidden", () => {
  assert.match(thumbnailSource, /useLayoutEffect\(\(\) => \{/);
  assert.match(thumbnailSource, /const image = imageRef\.current/);
  assert.match(thumbnailSource, /if \(!image\?\.complete\) return/);
  assert.match(thumbnailSource, /if \(image\.naturalWidth > 0\) \{\s*handleLoad\(\)/);
  assert.match(thumbnailSource, /else \{\s*handleError\(\)/);
  assert.match(thumbnailSource, /ref=\{imageRef\}/);
  assert.doesNotMatch(thumbnailSource, /setState\(src \? "loading" : "failed"\)/);
});

test("only likely first-viewport thumbnails receive eager and high priority hints", () => {
  assert.match(thumbnailSource, /loading=\{eager \|\| highPriority \? "eager" : "lazy"\}/);
  assert.match(thumbnailSource, /fetchPriority=\{highPriority \? "high" : "auto"\}/);
  assert.match(thumbnailSource, /decoding="async"/);
  assert.match(thumbnailSource, /alt=""/);
  assert.match(gridSource, /eager=\{index < eagerCount\}/);
  assert.match(gridSource, /highPriority=\{index < highPriorityCount\}/);
  assert.match(homeSource, /<SectionHeader title="随机推荐"[\s\S]*?eagerCount=\{eagerCount\}[\s\S]*?highPriorityCount=\{1\}/);
  assert.match(homeSource, /<SectionHeader title="最新视频"[\s\S]*?<VideoGrid[\s\S]*?videos=\{latest\}[\s\S]*?skeletonCount=\{displayCount\}/);
  assert.doesNotMatch(
    homeSource.match(/<SectionHeader title="最新视频"[\s\S]*?<\/div>/)?.[0] ?? "",
    /eagerCount|highPriorityCount/
  );
});

test("preview loading uses an indeterminate indicator instead of fake progress", () => {
  assert.match(css, /\.preview-loader\s*\{[^}]*width:\s*30%[^}]*animation:\s*preview-loading 1\.1s ease-in-out infinite/s);
  assert.match(css, /@keyframes preview-loading/);
  assert.doesNotMatch(css, /@keyframes preview-progress[\s\S]*?width:\s*100%/);
});
