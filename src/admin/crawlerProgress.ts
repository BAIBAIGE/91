import type { DriveGenerationStatus } from "./api";

const crawlerPhaseLabels: Record<string, string> = {
  discovering: "发现候选",
  downloading: "下载中",
  validating: "媒体校验",
  fingerprinting: "指纹计算",
  thumbnail: "封面处理",
  deduplicating: "重复检测",
  cataloging: "写入媒体库",
};

export function crawlerScanStateText(status?: DriveGenerationStatus) {
  if (status?.state !== "scanning") return "";
  return crawlerPhaseLabels[status.phase || ""] || "抓取中";
}

export function formatCrawlerProgressBytes(value: number) {
  const bytes = Math.max(0, Number.isFinite(value) ? value : 0);
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

export function formatCrawlerProgressElapsed(value: number) {
  const seconds = Math.max(0, Math.floor(Number.isFinite(value) ? value : 0));
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分 ${seconds % 60} 秒`;
  const hours = Math.floor(minutes / 60);
  return `${hours} 小时 ${minutes % 60} 分`;
}

export function crawlerScanDetail(status?: DriveGenerationStatus) {
  if (!status) return "";
  const details: string[] = [];
  if (status.currentTitle?.trim()) details.push(status.currentTitle.trim());
  if (status.phase === "downloading" && (status.currentBytes ?? 0) > 0) {
    details.push(`已下载 ${formatCrawlerProgressBytes(status.currentBytes ?? 0)}`);
  }
  if ((status.elapsedSeconds ?? 0) > 0) {
    details.push(`本阶段 ${formatCrawlerProgressElapsed(status.elapsedSeconds ?? 0)}`);
  }
  return details.join(" · ");
}
