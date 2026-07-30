import type { VideoDetail, VideoItem, VideoSubtitle } from "@/types";
import type {
  VideoReaction,
  VideoReactionCounts,
} from "@/lib/videoReaction";

export type VideoShareClaim = {
  shareId: string;
  expiresAt: string;
  video: VideoDetail;
};

export class VideoShareUnavailableError extends Error {
  constructor(readonly status: number) {
    super("Video share unavailable");
    this.name = "VideoShareUnavailableError";
  }
}

// 真实后端接口调用。未配置网盘时，各接口返回空数据。
export async function fetchHomeVideos(count?: number): Promise<VideoItem[]> {
  // 整库随机轮次由服务端按登录会话维护；前端只需告知本次展示数量。
  const path = count === undefined ? "/api/home" : `/api/home?count=${count}`;
  const items = await apiGet<VideoItem[]>(path);
  if (!Array.isArray(items)) {
    throw new Error("Invalid /api/home response");
  }
  return items;
}

export async function fetchListing(
  page: number,
  pageSize: number,
  params?: { q?: string; tag?: string; sort?: string; includeTotal?: boolean }
): Promise<{ items: VideoItem[]; total: number }> {
  const qs = new URLSearchParams({
    page: String(page),
    size: String(pageSize),
  });
  if (params?.q) qs.set("q", params.q);
  if (params?.tag) qs.set("tag", params.tag);
  if (params?.sort) qs.set("sort", params.sort);
  if (params?.includeTotal === false) qs.set("count", "false");
  const result = await apiGet<{ items: VideoItem[]; total: number }>(
    `/api/list?${qs.toString()}`
  );
  if (
    !result ||
    !Array.isArray(result.items) ||
    typeof result.total !== "number"
  ) {
    throw new Error("Invalid /api/list response");
  }
  return result;
}

export function fetchVideoDetail(id: string): Promise<VideoDetail | null> {
  return apiGet<VideoDetail>(`/api/video/${encodeURIComponent(id)}`).catch(
    () => null
  );
}

export function fetchVideoSubtitles(id: string): Promise<VideoSubtitle[]> {
  return apiGet<VideoSubtitle[]>(`/api/video/${encodeURIComponent(id)}/subtitles`);
}

export function createVideoShare(id: string): Promise<{ url: string }> {
  return apiJSON<{ url: string }>(
    `/api/video/${encodeURIComponent(id)}/share`,
    { method: "POST" }
  );
}

// React StrictMode 会在开发环境重复运行 effect。共享同一个领取 Promise，
// 避免两个并发 POST 用不同 cookie 抢占同一条一次性链接。
const pendingVideoShareClaims = new Map<string, Promise<VideoShareClaim>>();

export function consumeVideoShare(token: string): Promise<VideoShareClaim> {
  const existing = pendingVideoShareClaims.get(token);
  if (existing) return existing;

  const request = fetch("/api/share/consume", {
    method: "POST",
    credentials: "include",
    cache: "no-store",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ token }),
  })
    .then(async (res) => {
      if (res.status === 404 || res.status === 410) {
        throw new VideoShareUnavailableError(res.status);
      }
      if (!res.ok) throw new HTTPStatusError(res.status);
      const result = (await res.json()) as VideoShareClaim;
      if (
        !result ||
        typeof result.shareId !== "string" ||
        !result.shareId ||
        typeof result.expiresAt !== "string" ||
        !result.video ||
        typeof result.video.videoSrc !== "string"
      ) {
        throw new Error("Invalid video share response");
      }
      return result;
    })
    .finally(() => {
      if (pendingVideoShareClaims.get(token) === request) {
        pendingVideoShareClaims.delete(token);
      }
    });

  pendingVideoShareClaims.set(token, request);
  return request;
}

export function fetchSharedVideoSubtitles(
  shareId: string
): Promise<VideoSubtitle[]> {
  return apiGet<VideoSubtitle[]>(
    `/api/share/${encodeURIComponent(shareId)}/subtitles`
  );
}

export function recordSharedVideoView(
  shareId: string
): Promise<{ views: number }> {
  return apiJSON<{ views: number }>(
    `/api/share/${encodeURIComponent(shareId)}/view`,
    { method: "POST" }
  );
}

export function updateVideoTags(
  id: string,
  tags: string[]
): Promise<VideoItem> {
  return apiJSON<VideoItem>(`/api/video/${encodeURIComponent(id)}/tags`, {
    method: "PUT",
    body: JSON.stringify({ tags }),
  });
}

export function hideVideo(id: string): Promise<{ ok: boolean }> {
  return apiJSON<{ ok: boolean }>(
    `/api/video/${encodeURIComponent(id)}/hide`,
    { method: "POST" }
  );
}

export function deleteVideo(
  id: string,
  options: { deleteSource?: boolean } = {}
): Promise<{ ok: boolean; deletedSource: boolean }> {
  return apiJSON<{ ok: boolean; deletedSource: boolean }>(
    `/admin/api/videos/${encodeURIComponent(id)}`,
    {
      method: "DELETE",
      body: JSON.stringify({ deleteSource: !!options.deleteSource }),
    }
  );
}

export function recordView(id: string): Promise<{ views: number }> {
  return apiJSON<{ views: number }>(
    `/api/video/${encodeURIComponent(id)}/view`,
    { method: "POST" }
  );
}

export type VideoReactionResult = VideoReactionCounts & {
  reaction: VideoReaction;
};

export async function setVideoVisitReaction(
  id: string,
  visitId: string,
  reaction: VideoReaction
): Promise<VideoReactionResult> {
  const result = await apiJSON<VideoReactionResult>(
    `/api/video/${encodeURIComponent(id)}/reaction`,
    {
      method: "PUT",
      body: JSON.stringify({ visitId, reaction }),
    }
  );
  if (
    !result ||
    (result.reaction !== "none" &&
      result.reaction !== "like" &&
      result.reaction !== "dislike") ||
    !Number.isInteger(result.likes) ||
    result.likes < 0 ||
    !Number.isInteger(result.dislikes) ||
    result.dislikes < 0
  ) {
    throw new Error("Invalid video reaction response");
  }
  return result;
}

export type UploadVideoInput = {
  file: File;
  title: string;
  tags: string[];
};

export function uploadVideo(input: UploadVideoInput): Promise<VideoItem> {
  const body = new FormData();
  body.append("file", input.file);
  if (input.title.trim()) {
    body.append("title", input.title.trim());
  }
  for (const tag of input.tags) {
    body.append("tags", tag);
  }
  return apiForm<VideoItem>("/api/upload", body);
}

export type RemoteUploadState =
  | "queued"
  | "downloading"
  | "validating"
  | "saving"
  | "completed"
  | "failed"
  | "canceled";

export type RemoteUploadJob = {
  id: string;
  state: RemoteUploadState;
  sourceLabel: string;
  title?: string;
  tags: string[];
  bytesDownloaded: number;
  totalBytes: number;
  canCancel: boolean;
  cancelRequested?: boolean;
  error?: string;
  completedVideoId?: string;
  videoHref?: string;
  createdAt: string;
  startedAt?: string;
  updatedAt: string;
  finishedAt?: string;
};

export type CreateRemoteUploadInput = {
  url: string;
  title: string;
  tags: string[];
};

export function createRemoteUpload(
  input: CreateRemoteUploadInput
): Promise<RemoteUploadJob> {
  return apiJSON<RemoteUploadJob>("/api/upload/remote", {
    method: "POST",
    body: JSON.stringify({
      url: input.url.trim(),
      title: input.title.trim(),
      tags: input.tags,
    }),
  });
}

export function fetchRemoteUploads(limit = 20): Promise<RemoteUploadJob[]> {
  return apiGet<RemoteUploadJob[]>(
    `/api/upload/remote?limit=${encodeURIComponent(String(limit))}`
  );
}

export function cancelRemoteUpload(id: string): Promise<RemoteUploadJob> {
  return apiJSON<RemoteUploadJob>(
    `/api/upload/remote/${encodeURIComponent(id)}/cancel`,
    { method: "POST" }
  );
}

export type TagItem = { id: string; label: string; count?: number };

let cachedTags: TagItem[] | null = null;
let pendingTags: Promise<TagItem[]> | null = null;

export function readCachedTags(): TagItem[] | null {
  return cachedTags;
}

export function fetchTags(): Promise<TagItem[]> {
  if (cachedTags !== null) {
    return Promise.resolve(cachedTags);
  }
  if (pendingTags) return pendingTags;
  pendingTags = apiGet<TagItem[]>("/api/tags")
    .then((tags) => {
      cachedTags = tags;
      return tags;
    })
    .catch(() => cachedTags ?? [])
    .finally(() => {
      pendingTags = null;
    });
  return pendingTags;
}

/** 短视频模式单条记录。比 VideoItem 多 videoSrc / poster。 */
export type ShortsItem = VideoItem & {
  videoSrc: string;
  poster: string;
  /** Tiny server-preblurred texture for the viewport-sized letterbox backdrop. */
  backgroundPoster?: string;
  /**
   * 文件大小与时长，用来算平均码率，进而把预加载门槛从固定秒数换算成
   * 一份固定的字节预算。元数据缺失时后端会省略这两个字段（omitempty），
   * 消费方必须按"未知码率"兜底回原有的固定门槛。
   */
  sizeBytes?: number;
  durationSeconds?: number;
};

export type ShortsFeedItem = ShortsItem & {
  /** Resume position immediately after this item in the server-side feed. */
  feedCursor: number;
};

/** 短视频"取下一批"接口的响应。 */
export type ShortsNextResponse = {
  items: ShortsFeedItem[];
  total: number;
  feedToken: string;
  nextCursor: number;
  /** true 表示当前服务端随机轮次已经读到末尾。 */
  roundComplete: boolean;
};

export class ShortsFeedExpiredError extends Error {
  constructor() {
    super("Shorts feed expired");
    this.name = "ShortsFeedExpiredError";
  }
}

/**
 * 拉取服务端随机 feed 的下一批候选。请求只携带固定大小的令牌和游标，
 * 不会再随已看视频数量增长，也没有请求体。
 */
export async function fetchShortsNext(
  feedToken: string,
  cursor: number,
  count: number
): Promise<ShortsNextResponse> {
  const params = new URLSearchParams({
    cursor: String(cursor),
    count: String(count),
  });
  if (feedToken) params.set("feedToken", feedToken);

  let result: ShortsNextResponse;
  try {
    result = await apiGet<ShortsNextResponse>(
      `/api/shorts/next?${params.toString()}`
    );
  } catch (error) {
    if (error instanceof HTTPStatusError && error.status === 410) {
      throw new ShortsFeedExpiredError();
    }
    throw error;
  }

  if (
    !result ||
    !Array.isArray(result.items) ||
    !Number.isInteger(result.total) ||
    result.total < 0 ||
    typeof result.feedToken !== "string" ||
    (result.total > 0 && result.feedToken.length === 0) ||
    !Number.isInteger(result.nextCursor) ||
    result.nextCursor < 0 ||
    typeof result.roundComplete !== "boolean" ||
    result.items.some(
      (item) =>
        !Number.isInteger(item.feedCursor) ||
        item.feedCursor < 1 ||
        item.feedCursor > result.nextCursor
    )
  ) {
    throw new Error("Invalid /api/shorts/next response");
  }
  return result;
}

const API_GET_MAX_ATTEMPTS = 2;
const API_GET_RETRY_DELAY_MS = 200;
const API_GET_TIMEOUT_MS = 10_000;

class HTTPStatusError extends Error {
  constructor(readonly status: number) {
    super(`HTTP ${status}`);
    this.name = "HTTPStatusError";
  }
}

function isRetryableGetError(error: unknown): boolean {
  if (!(error instanceof HTTPStatusError)) return true;
  return error.status === 408 || error.status === 425 || error.status === 429 || error.status >= 500;
}

function wait(ms: number): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, ms));
}

async function apiGet<T>(path: string): Promise<T> {
  let lastError: unknown;

  for (let attempt = 1; attempt <= API_GET_MAX_ATTEMPTS; attempt += 1) {
    const controller = new AbortController();
    const timeoutID = globalThis.setTimeout(
      () => controller.abort(),
      API_GET_TIMEOUT_MS
    );
    try {
      const res = await fetch(path, {
        credentials: "include",
        cache: "no-store",
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
      if (!res.ok) throw new HTTPStatusError(res.status);
      return (await res.json()) as T;
    } catch (error) {
      lastError = error;
      if (attempt >= API_GET_MAX_ATTEMPTS || !isRetryableGetError(error)) {
        throw error;
      }
    } finally {
      globalThis.clearTimeout(timeoutID);
    }

    await wait(API_GET_RETRY_DELAY_MS);
  }

  throw lastError instanceof Error ? lastError : new Error("API request failed");
}

async function apiJSON<T>(path: string, init: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) throw await responseError(res);
  return res.json();
}

async function apiForm<T>(path: string, body: FormData): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    body,
  });
  if (!res.ok) throw await responseError(res);
  return res.json();
}

async function responseError(res: Response): Promise<Error> {
  try {
    const body = (await res.json()) as { error?: unknown };
    if (typeof body?.error === "string" && body.error.trim()) {
      return new Error(body.error.trim());
    }
  } catch {
    // Non-JSON errors retain the stable HTTP fallback below.
  }
  return new Error(`HTTP ${res.status}`);
}
