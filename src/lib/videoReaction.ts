export type VideoReaction = "none" | "like" | "dislike";
export type SelectableVideoReaction = Exclude<VideoReaction, "none">;

export type VideoReactionCounts = {
  likes: number;
  dislikes: number;
};

export function nextVideoReaction(
  current: VideoReaction,
  selected: SelectableVideoReaction
): VideoReaction {
  return current === selected ? "none" : selected;
}

export function applyVideoReactionTransition(
  counts: VideoReactionCounts,
  previous: VideoReaction,
  next: VideoReaction
): VideoReactionCounts {
  if (previous === next) return counts;

  return {
    likes: Math.max(
      0,
      counts.likes -
        (previous === "like" ? 1 : 0) +
        (next === "like" ? 1 : 0)
    ),
    dislikes: Math.max(
      0,
      counts.dislikes -
        (previous === "dislike" ? 1 : 0) +
        (next === "dislike" ? 1 : 0)
    ),
  };
}

/**
 * 每个视频详情页实例生成一张新的匿名临时选票。该 ID 不持久化，也不包含
 * 用户、账号或设备信息；刷新或重新进入详情页时自然生成另一张选票。
 */
export function createVideoReactionVisitId(): string {
  const cryptoAPI = globalThis.crypto;
  if (cryptoAPI && typeof cryptoAPI.getRandomValues === "function") {
    const bytes = new Uint8Array(16);
    cryptoAPI.getRandomValues(bytes);
    return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
      ""
    );
  }

  // 只为缺少 Web Crypto 的旧环境兜底。服务端鉴权仍然存在，这个值仅用于
  // 区分同一页面实例的幂等请求，不承担登录或授权职责。
  return `visit-${Date.now().toString(36)}-${Math.random()
    .toString(36)
    .slice(2)}-${Math.random().toString(36).slice(2)}`;
}
