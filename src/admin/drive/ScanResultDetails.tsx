import type { ScanResult } from "../api";
import { scanOutcomeLabels, scanResultSummary } from "./scanResults";

export function ScanResultDetails({ result }: { result: ScanResult }) {
  const finishedAt = new Date(result.finishedAt);
  return (
    <div className="admin-scan-result">
      <p className="admin-scan-result__summary">
        扫描结果：{scanOutcomeLabels[result.state]}
        {Number.isFinite(finishedAt.getTime()) && ` - 结束时间：${finishedAt.toLocaleString("zh-CN", {
          month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
        }).replace(/\//g, "-")}`}
      </p>
      <p className="admin-scan-result__summary">{scanResultSummary(result)}</p>
    </div>
  );
}
