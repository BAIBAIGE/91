import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Toast, type ToastKind } from "../src/components/Toast.tsx";

const toastSource = readFileSync(
  new URL("../src/components/ToastContext.tsx", import.meta.url),
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

test("toast copy and close actions are separate native buttons", () => {
  const html = renderToStaticMarkup(createElement(Toast, {
    toast: { id: 1, kind: "info", text: "任务已提交" },
    onDismiss: () => assert.fail("Rendering must not dismiss a toast"),
  }));
  const buttons = html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? [];
  assert.equal(buttons.length, 2);
  assert.match(buttons[0], /type="button"/);
  assert.match(buttons[0], /aria-label="复制提示：任务已提交"/);
  assert.match(buttons[0], /任务已提交<\/span>/);
  assert.match(buttons[1], /type="button"/);
  assert.match(buttons[1], /aria-label="关闭提示"/);
  assert.doesNotMatch(html, /role="button"/);
  assert.doesNotMatch(html, /tabindex=/);
});

test("toast messages are escaped and errors have an alert announcement", () => {
  for (const kind of ["info", "success", "error"] as ToastKind[]) {
    const html = renderToStaticMarkup(createElement(Toast, {
      toast: { id: 1, kind, text: '<script>alert("message")</script>' },
      onDismiss: () => undefined,
    }));
    assert.doesNotMatch(html, /<script>/);
    assert.match(html, /&lt;script&gt;/);
    assert.equal(html.includes('role="alert"'), kind === "error");
    assert.match(html, /role="status" aria-live="polite"/);
    assert.equal((html.match(/<svg\b/g) ?? []).length, 2);
    assert.equal((html.match(/aria-hidden="true"/g) ?? []).length, 2);
  }
});

test("toast item updates keep the context value stable", () => {
  assert.match(toastSource, /const contextValue = useMemo\(\(\) => \(\{ show \}\), \[show\]\)/);
  assert.match(toastSource, /<ToastCtx\.Provider value=\{contextValue\}>/);
  assert.doesNotMatch(toastSource, /<ToastCtx\.Provider value=\{\{ show \}\}>/);
});

test("toast cards use an opaque theme surface for every status", () => {
  const baseToast = ruleBody(sharedStateCss, ".toast");
  assert.match(baseToast, /background\s*:\s*var\(--bg-elevated\)/);
  assert.match(baseToast, /color\s*:\s*var\(--text-strong\)/);
  for (const kind of ["success", "error"]) {
    const variant = ruleBody(sharedStateCss, `.toast.is-${kind}`);
    assert.doesNotMatch(variant, /(?:background|color)\s*:/);
  }
});

test("toasts show long messages without internal scrolling", () => {
  const baseToast = ruleBody(sharedStateCss, ".toast");
  const baseText = ruleBody(sharedStateCss, ".toast__text");
  const content = ruleBody(sharedStateCss, ".toast__content");
  const mobileStack = ruleBody(mobileCss(), ".toast-stack");

  assert.match(baseText, /overflow-wrap\s*:\s*anywhere/);
  assert.match(baseText, /white-space\s*:\s*pre-wrap/);
  assert.match(content, /min-width\s*:\s*0/);
  assert.match(mobileStack, /width\s*:\s*auto/);
  assert.doesNotMatch(baseToast, /max-height/);
  assert.doesNotMatch(baseText, /max-height/);
  assert.doesNotMatch(baseText, /overflow-y\s*:\s*auto/);
});
