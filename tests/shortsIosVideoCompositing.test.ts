import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const shortsCss = readFileSync(
  new URL("../src/styles/shorts.css", import.meta.url),
  "utf8"
);
const shortsPageSource = readFileSync(
  new URL("../src/pages/ShortsPage.tsx", import.meta.url),
  "utf8"
);
const shortsPlatformSource = readFileSync(
  new URL("../src/shorts/platform.ts", import.meta.url),
  "utf8"
);
const indexHtml = readFileSync(
  new URL("../index.html", import.meta.url),
  "utf8"
);
const manifest = JSON.parse(
  readFileSync(new URL("../public/manifest.webmanifest", import.meta.url), "utf8")
) as { icons: Array<{ src: string; sizes: string; purpose: string }> };

// iOS Safari/WebKit does not composite an inline <video> nested inside a
// `position: fixed` ancestor — the video decodes and plays but never paints
// (black screen on iOS only). The shorts page wrapper must therefore not be
// position:fixed; it locks the viewport via html/body overflow + 100svh height.
test("shorts page wrapper is not position:fixed (breaks iOS <video> compositing)", () => {
  const pageRule = /\.shorts-page \{[\s\S]*?\}/.exec(shortsCss);
  assert.ok(pageRule, ".shorts-page rule should exist");
  assert.doesNotMatch(pageRule[0], /position:\s*fixed/);
  assert.match(pageRule[0], /position:\s*relative/);
  assert.match(pageRule[0], /height:\s*100svh/);
});

test("iPhone browser uses document scrolling without manual fullscreen controls", () => {
  assert.match(shortsPlatformSource, /function shouldUseDocumentScrollForShorts\(\)/);
  assert.match(shortsPlatformSource, /function isIPhoneBrowserShell\(\)/);
  assert.match(shortsPageSource, /root:\s*null/);
  assert.doesNotMatch(shortsPageSource, /supportsElementFullscreenAPI/);
  assert.doesNotMatch(shortsPageSource, /requestFullscreen/);
  assert.doesNotMatch(shortsPageSource, /aria-label=\{isFullscreen \? "退出全屏" : "进入全屏"\}/);
  assert.doesNotMatch(shortsPageSource, /function handleFullscreenButtonPointerDown/);
  assert.doesNotMatch(shortsPageSource, /onPointerDown=\{handleFullscreenButtonPointerDown\}/);
  assert.doesNotMatch(shortsPageSource, /onFirstPointer/);
  assert.doesNotMatch(shortsPageSource, /currentPage\.addEventListener\("pointerdown"/);
  assert.match(shortsCss, /html\.shorts-document-scroll[\s\S]*scroll-snap-type:\s*y mandatory/);
  assert.match(shortsCss, /\.shorts-page\.is-document-scroll \.shorts-feed[\s\S]*overflow-y:\s*visible/);
  assert.match(shortsCss, /\.shorts-page\.is-document-scroll \.shorts-header,[\s\S]*\.shorts-page\.is-document-scroll \.shorts-hud-toast[\s\S]*position:\s*fixed/);
});

test("app has standalone display metadata for iPhone home-screen launch", () => {
  assert.match(indexHtml, /<link rel="manifest" href="\/manifest\.webmanifest" \/>/);
  assert.match(
    indexHtml,
    /<link rel="apple-touch-icon" sizes="180x180" href="\/apple-touch-icon\.png" \/>/
  );
  assert.match(indexHtml, /<meta name="apple-mobile-web-app-capable" content="yes" \/>/);
  assert.match(indexHtml, /<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent" \/>/);
});

test("home-screen icons use safe-area assets instead of the in-app logo", () => {
  assert.ok(
    manifest.icons.some(
      (icon) =>
        icon.src === "/app-icon-512.png" &&
        icon.sizes === "512x512" &&
        icon.purpose === "any"
    )
  );
  assert.ok(
    manifest.icons.some(
      (icon) =>
        icon.src === "/app-icon-maskable-512.png" &&
        icon.sizes === "512x512" &&
        icon.purpose === "maskable"
    )
  );
  assert.equal(
    manifest.icons.some((icon) => icon.src === "/icon.png" && icon.purpose.includes("maskable")),
    false
  );
});
