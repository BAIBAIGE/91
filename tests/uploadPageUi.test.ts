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
  assert.match(uploadPageSource, /fetchUploadTags\(\)/);
  assert.match(uploadPageSource, /availableTags\.map\(\(tag\) => tag\.label\)/);
  assert.doesNotMatch(uploadPageSource, /UPLOAD_TAGS/);
  assert.match(uploadPageSource, /uploadTagOptions\.length > 0/);
  assert.match(uploadPageSource, /<label key="local-upload-file" className="upload-drop">/);
  assert.match(
    uploadPageSource,
    /<label key="remote-upload-url" className="upload-field upload-remote-url">/
  );

  const uploadActions = ruleBody(layoutCss, ".upload-actions");
  const uploadSubmit = ruleBody(layoutCss, ".upload-submit");
  assert.match(uploadActions, /justify-content\s*:\s*flex-end/);
  assert.match(uploadSubmit, /height\s*:\s*36px/);
  assert.match(uploadSubmit, /padding\s*:\s*0 var\(--space-4\)/);
  assert.match(uploadSubmit, /border\s*:\s*1px solid var\(--border-default\)/);
  assert.match(uploadSubmit, /background\s*:\s*var\(--bg-elevated\)/);
  assert.match(uploadSubmit, /color\s*:\s*var\(--text-default\)/);
  assert.doesNotMatch(uploadSubmit, /accent|glow|text-on-accent/);
  assert.doesNotMatch(uploadSubmit, /min-width/);
  assert.doesNotMatch(uploadSubmit, /gap\s*:/);
  assert.doesNotMatch(
    layoutCss,
    /\.upload-submit\s*\{[^}]*width\s*:\s*100%/s
  );

  const uploadSubmitHover = ruleBody(layoutCss, ".upload-submit:hover:not(:disabled)");
  assert.match(uploadSubmitHover, /border-color\s*:\s*var\(--border-strong\)/);
  assert.match(uploadSubmitHover, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(uploadSubmitHover, /color\s*:\s*var\(--text-strong\)/);
  assert.doesNotMatch(uploadSubmitHover, /accent|glow|filter/);
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
