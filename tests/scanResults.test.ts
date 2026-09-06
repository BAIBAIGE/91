import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { ScanResult } from "../src/admin/api";
import { isGenerationBusy, scanOutcomeLabels } from "../src/admin/drive/scanResults";
import { ScanResultDetails } from "../src/admin/drive/ScanResultDetails";

const result: ScanResult = {
  driveId: "drive", state: "partial", startedAt: "2026-09-05T10:00:00Z", finishedAt: "2026-09-05T10:01:00Z",
  scannedCount: 7, addedCount: 2, updatedCount: 1, duplicateCount: 3, tombstonedCount: 1, errorCount: 1,
  issues: [{ stage: "discovery", message: "子目录读取失败" }],
};

test("completed scan outcomes do not block another scan", () => {
  for (const state of Object.keys(scanOutcomeLabels)) assert.equal(isGenerationBusy(state), false, state);
  for (const state of ["scanning", "cooling", "queued", "generating", "uploading"]) assert.equal(isGenerationBusy(state), true, state);
});

test("scan details retain status and counts without internal messages", () => {
  const canceled: ScanResult = { ...result, state: "canceled", message: "context canceled", errorCount: 25 };
  const html = renderToStaticMarkup(createElement(ScanResultDetails, { result: canceled })).replace(/<[^>]+>/g, " ").replace(/\s+/g, " ");
  for (const text of ["已取消", "已扫描 7", "新增 2", "更新 1", "重复跳过 3", "错误 25", "结束时间："]) assert.ok(html.includes(text), text);
  for (const text of ["context canceled", "子目录读取失败", "日志"]) assert.ok(!html.includes(text), text);
});
