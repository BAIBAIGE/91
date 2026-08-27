import assert from "node:assert/strict";
import test from "node:test";

import {
  shouldInterceptPreviewTap,
  shouldStartInstantPreview,
  TOUCH_PREVIEW_DELAY_MS,
} from "../src/lib/previewIntent.ts";

test("touch preview media waits 200ms after the first tap intent", () => {
  assert.equal(TOUCH_PREVIEW_DELAY_MS, 200);
});

test("touch tap starts preview instead of navigating when preview is idle", () => {
  assert.equal(
    shouldInterceptPreviewTap({
      canHover: false,
      pointerType: "touch",
      previewActive: false,
    }),
    true
  );
  assert.equal(shouldStartInstantPreview({ pointerType: "touch" }), true);
});

test("touch tap navigates when the same card preview is already active", () => {
  assert.equal(
    shouldInterceptPreviewTap({
      canHover: false,
      pointerType: "touch",
      previewActive: true,
    }),
    false
  );
});

test("mouse click does not intercept normal navigation", () => {
  assert.equal(
    shouldInterceptPreviewTap({
      canHover: true,
      pointerType: "mouse",
      previewActive: false,
    }),
    false
  );
  assert.equal(shouldStartInstantPreview({ pointerType: "mouse" }), false);
});
