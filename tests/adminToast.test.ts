import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const toastSource = readFileSync(
  new URL("../src/admin/ToastContext.tsx", import.meta.url),
  "utf8"
);
const clipboardSource = readFileSync(
  new URL("../src/lib/clipboard.ts", import.meta.url),
  "utf8"
);
const sharedStateCss = readFileSync(
  new URL("../src/styles/shared-state.css", import.meta.url),
  "utf8"
);

function ruleBody(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `Expected CSS rule for ${selector}`);
  return match[1];
}

function mobileCss(): string {
  const marker = "@media (max-width: 640px)";
  const start = sharedStateCss.indexOf(marker);
  assert.notEqual(start, -1, "Expected mobile shared-state media query");
  return sharedStateCss.slice(start);
}

test("admin toasts auto-dismiss and copy their text when clicked", () => {
  assert.match(toastSource, /const TOAST_DISMISS_MS = 2500/);
  assert.match(toastSource, /const TOAST_MAX_VISIBLE = 2/);
  assert.match(toastSource, /const TOAST_COPY_SUCCESS_TEXT = "已复制到剪贴板"/);
  assert.match(toastSource, /const TOAST_COPY_ERROR_TEXT = "复制失败，请手动复制"/);
  assert.match(toastSource, /copyTextToClipboard\(text\)/);
  assert.match(clipboardSource, /navigator\.clipboard\?\.writeText/);
  assert.match(clipboardSource, /return fallbackCopyText\(text\)/);
  assert.match(clipboardSource, /document\.execCommand\("copy"\)/);
  assert.match(toastSource, /addToast\(TOAST_COPY_SUCCESS_TEXT,\s*"success",\s*false\)/);
  assert.match(toastSource, /addToast\(TOAST_COPY_ERROR_TEXT,\s*"error",\s*false\)/);
  assert.match(toastSource, /t\.copyable\s*\?\s*" is-copyable"\s*:\s*""/);
  assert.match(toastSource, /onClick=\{t\.copyable \? \(\) => copyToastText\(t\.text\) : undefined\}/);
  assert.match(toastSource, /aria-label=\{t\.copyable \? `复制提示：\$\{t\.text\}` : undefined\}/);
  assert.match(toastSource, /event\.key !== "Enter" && event\.key !== " "/);
  assert.doesNotMatch(toastSource, /onClick=\{\(\) => scheduleDismiss/);
  assert.doesNotMatch(toastSource, /pinnedToastIDs/);
  assert.doesNotMatch(toastSource, /isDismissPaused/);
  assert.doesNotMatch(toastSource, /pinDismiss/);
  assert.doesNotMatch(toastSource, /className="admin-toast__close"/);
  assert.doesNotMatch(toastSource, /aria-label="关闭提示"/);
  assert.doesNotMatch(toastSource, /<X size=/);
  assert.doesNotMatch(toastSource, /event\.stopPropagation\(\)/);
  assert.doesNotMatch(toastSource, /onPointerEnter/);
  assert.doesNotMatch(toastSource, /onPointerLeave/);
});

test("admin toasts keep the newest two visible", () => {
  assert.match(toastSource, /const visible = withNewToast\.slice\(-TOAST_MAX_VISIBLE\)/);
  assert.match(toastSource, /const evicted = withNewToast\.slice\(/);
  assert.match(toastSource, /for \(const item of evicted\) \{\s*forgetToast\(item\);/);
  assert.match(toastSource, /setToastItems\(visible\)/);
  assert.match(toastSource, /repeated text is refreshed and moved last/);
  assert.doesNotMatch(toastSource, /setItems\(\(list\) => \[\.\.\.list,/);
});

test("toast item updates keep the context value stable", () => {
  assert.match(toastSource, /const contextValue = useMemo\(\(\) => \(\{ show \}\), \[show\]\)/);
  assert.match(toastSource, /<ToastCtx\.Provider value=\{contextValue\}>/);
  assert.doesNotMatch(toastSource, /<ToastCtx\.Provider value=\{\{ show \}\}>/);
});

test("admin toasts show long messages without internal scrolling", () => {
  const baseToast = ruleBody(sharedStateCss, ".admin-toast");
  const baseText = ruleBody(sharedStateCss, ".admin-toast__text");
  const mobileToast = ruleBody(mobileCss(), ".admin-toast");

  assert.match(baseToast, /max-width\s*:\s*min\(520px,\s*calc\(100vw - 48px\)\)/);
  assert.match(baseToast, /padding\s*:\s*14px\s+18px/);
  assert.match(baseToast, /position\s*:\s*relative/);
  assert.match(baseToast, /overflow-wrap\s*:\s*anywhere/);
  assert.match(baseToast, /touch-action\s*:\s*manipulation/);
  assert.doesNotMatch(baseToast, /cursor\s*:\s*pointer/);
  assert.match(ruleBody(sharedStateCss, ".admin-toast.is-copyable"), /cursor\s*:\s*pointer/);
  assert.match(baseText, /display\s*:\s*block/);
  assert.doesNotMatch(sharedStateCss, /\.admin-toast__close/);
  assert.match(mobileToast, /max-width\s*:\s*100%/);
  assert.match(mobileToast, /text-align\s*:\s*left/);
  assert.doesNotMatch(baseToast, /max-height/);
  assert.doesNotMatch(baseText, /max-height/);
  assert.doesNotMatch(baseText, /overflow-y\s*:\s*auto/);
  assert.doesNotMatch(mobileToast, /max-height/);
  assert.doesNotMatch(mobileCss(), /\.admin-toast__text\s*\{/);
});
