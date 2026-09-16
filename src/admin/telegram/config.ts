export function parseTelegramUserIDs(value: string): number[] {
  if (!value.trim()) return [];
  return [
    ...new Set(
      value
        .split(/[\s,，]+/)
        .filter(Boolean)
        .map((id) => {
          if (
            !/^\d+$/.test(id) ||
            !Number.isSafeInteger(Number(id)) ||
            Number(id) <= 0
          )
            throw new Error("允许的用户必须是正整数 ID，用逗号或换行分隔");
          return Number(id);
        }),
    ),
  ];
}
export function importStageLabel(job: {
  state: string;
  stage: string;
  cancelRequested?: boolean;
}): string {
  if (job.cancelRequested) return "正在取消";
  if (job.state === "queued" && job.stage === "retry_wait") return "等待重试";
  if (job.state === "downloading")
    return "正在从 TG 获取";
  return (
    (
      {
        queued: "排队中",
        validating: "校验视频",
        saving: "正在入库",
        completed: "已保存",
        failed: "失败",
        canceled: "已取消",
      } as Record<string, string>
    )[job.state] ?? job.state
  );
}
