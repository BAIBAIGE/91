import assert from "node:assert/strict";
import test from "node:test";
import {
  crawlerScanDetail,
  crawlerScanStateText,
  formatCrawlerProgressBytes,
  formatCrawlerProgressElapsed,
} from "../src/admin/crawlerProgress.ts";
import type { DriveGenerationStatus } from "../src/admin/api.ts";

function status(overrides: Partial<DriveGenerationStatus> = {}): DriveGenerationStatus {
  return {
    state: "scanning",
    queueLength: 0,
    scannedCount: 0,
    addedCount: 0,
    doneCount: 0,
    totalCount: 0,
    ...overrides,
  };
}

test("crawler status names the active end-to-end phase", () => {
  assert.equal(crawlerScanStateText(status({ phase: "discovering" })), "发现候选");
  assert.equal(crawlerScanStateText(status({ phase: "downloading" })), "下载中");
  assert.equal(crawlerScanStateText(status({ phase: "fingerprinting" })), "指纹计算");
  assert.equal(crawlerScanStateText(status()), "抓取中");
  assert.equal(crawlerScanStateText(status({ state: "idle", phase: "downloading" })), "");
});

test("crawler status exposes transfer size and phase elapsed time", () => {
  assert.equal(formatCrawlerProgressBytes(1_013_710_896), "966.8 MB");
  assert.equal(formatCrawlerProgressBytes(2 * 1024 ** 3), "2.00 GB");
  assert.equal(formatCrawlerProgressElapsed(793), "13 分 13 秒");
  assert.equal(formatCrawlerProgressElapsed(3_661), "1 小时 1 分");
  assert.equal(
    crawlerScanDetail(
      status({
        phase: "downloading",
        currentTitle: "Example Video",
        currentBytes: 1_013_710_896,
        elapsedSeconds: 793,
      })
    ),
    "Example Video · 已下载 966.8 MB · 本阶段 13 分 13 秒"
  );
});
