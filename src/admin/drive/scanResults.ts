import type { ScanOutcome, ScanResult } from "../api";

export const scanOutcomeLabels: Record<ScanOutcome, string> = {
  succeeded: "已完成",
  partial: "部分完成",
  failed: "失败",
  canceled: "已取消",
  skipped: "已跳过",
};

export const scanOutcomeClasses: Record<ScanOutcome, string> = {
  succeeded: "idle",
  partial: "cooling",
  failed: "error",
  canceled: "queued",
  skipped: "queued",
};

export function isGenerationBusy(state: string): boolean {
  return ["scanning", "uploading", "generating", "cooling", "queued"].includes(state);
}

export function scanResultSummary(result: ScanResult): string {
  return `已扫描 ${result.scannedCount} · 新增 ${result.addedCount} · 更新 ${result.updatedCount} · 重复跳过 ${result.duplicateCount} · 黑名单跳过 ${result.tombstonedCount} · 错误 ${result.errorCount}`;
}
