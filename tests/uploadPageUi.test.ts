import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const uploadPageSource = readFileSync(
  new URL("../src/pages/UploadPage.tsx", import.meta.url),
  "utf8"
);
const layoutCss = readFileSync(
  new URL("../src/styles/layout.css", import.meta.url),
  "utf8"
);

function ruleBody(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `Expected CSS rule for ${selector}`);
  return match[1];
}

test("upload page supports local files and persistent remote-link jobs", () => {
  assert.match(uploadPageSource, /<SectionHeader title="上传视频" \/>/);
  assert.match(uploadPageSource, /本地文件/);
  assert.match(uploadPageSource, /视频直链/);
  assert.match(uploadPageSource, /createRemoteUpload/);
  assert.match(uploadPageSource, /fetchRemoteUploads\(20\)/);
  assert.match(uploadPageSource, /cancelRemoteUpload/);
  assert.match(uploadPageSource, /window\.setInterval\(\(\) => \{[\s\S]*?\}, 2000\)/);
  assert.match(uploadPageSource, /document\.visibilityState !== "hidden"/);
  assert.match(uploadPageSource, /disabled=\{submitDisabled\}/);
  assert.match(uploadPageSource, /任务已加入后台队列，关闭页面不会中断下载/);

  const uploadActions = ruleBody(layoutCss, ".upload-actions");
  const uploadSubmit = ruleBody(layoutCss, ".upload-submit");
  assert.match(uploadActions, /justify-content\s*:\s*flex-end/);
  assert.match(uploadSubmit, /height\s*:\s*36px/);
  assert.match(uploadSubmit, /padding\s*:\s*0 var\(--space-4\)/);
  assert.doesNotMatch(uploadSubmit, /min-width/);
  assert.doesNotMatch(uploadSubmit, /gap\s*:/);
  assert.doesNotMatch(
    layoutCss,
    /\.upload-submit\s*\{[^}]*width\s*:\s*100%/s
  );
});

test("remote upload task list has progress, cancellation, and mobile layout", () => {
  assert.match(uploadPageSource, /role="progressbar"/);
  assert.match(uploadPageSource, /job\.totalBytes > 0/);
  assert.match(uploadPageSource, /远程大小未知/);
  assert.match(uploadPageSource, /job\.canCancel/);
  assert.match(uploadPageSource, /job\.videoHref/);
  assert.match(layoutCss, /\.remote-upload-progress\s*\{/);
  assert.match(layoutCss, /\.remote-upload-cancel\s*,\s*\.remote-upload-detail\s*\{/);
  assert.match(
    layoutCss,
    /@media \(max-width: 640px\)[\s\S]*?\.remote-upload-job\s*\{[\s\S]*?flex-direction:\s*column/
  );
});
