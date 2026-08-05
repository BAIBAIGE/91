const POSITIVE_INTEGER_PATTERN = /^[1-9]\d*$/;
const DRIVE_SOURCE_PREFIX = "drive:";
const CRAWLER_SOURCE_PREFIX = "crawler:";

export type AdminVideosSourceKey =
  | "all"
  | `drive:${string}`
  | `crawler:${string}`;

export type AdminVideosSourceFilter = {
  driveId: string;
  crawlerId: string;
};

export function readAdminVideosPage(params: URLSearchParams): number {
  const value = params.get("page");
  if (!value || !POSITIVE_INTEGER_PATTERN.test(value)) return 1;

  const page = Number(value);
  return Number.isSafeInteger(page) ? page : 1;
}

export function withAdminVideosPage(
  params: URLSearchParams,
  page: number
): URLSearchParams {
  const next = new URLSearchParams(params);
  if (!Number.isSafeInteger(page) || page <= 1) {
    next.delete("page");
  } else {
    next.set("page", String(page));
  }
  return next;
}

export function readAdminVideosSourceKey(
  params: URLSearchParams
): AdminVideosSourceKey {
  return normalizeAdminVideosSourceKey(params.get("source"));
}

export function withAdminVideosSourceKey(
  params: URLSearchParams,
  sourceKey: AdminVideosSourceKey
): URLSearchParams {
  const next = new URLSearchParams(params);
  const normalizedSourceKey = normalizeAdminVideosSourceKey(sourceKey);
  if (normalizedSourceKey === "all") {
    next.delete("source");
  } else {
    next.set("source", normalizedSourceKey);
  }
  return next;
}

export function adminVideosSourceFilter(
  sourceKey: AdminVideosSourceKey
): AdminVideosSourceFilter {
  if (sourceKey.startsWith(DRIVE_SOURCE_PREFIX)) {
    return {
      driveId: sourceKey.slice(DRIVE_SOURCE_PREFIX.length),
      crawlerId: "",
    };
  }
  if (sourceKey.startsWith(CRAWLER_SOURCE_PREFIX)) {
    return {
      driveId: "",
      crawlerId: sourceKey.slice(CRAWLER_SOURCE_PREFIX.length),
    };
  }
  return { driveId: "", crawlerId: "" };
}

function normalizeAdminVideosSourceKey(
  value: string | null
): AdminVideosSourceKey {
  const sourceKey = value?.trim() ?? "";
  if (sourceKey.startsWith(DRIVE_SOURCE_PREFIX)) {
    const id = sourceKey.slice(DRIVE_SOURCE_PREFIX.length).trim();
    return id ? `drive:${id}` : "all";
  }
  if (sourceKey.startsWith(CRAWLER_SOURCE_PREFIX)) {
    const id = sourceKey.slice(CRAWLER_SOURCE_PREFIX.length).trim();
    return id ? `crawler:${id}` : "all";
  }
  return "all";
}
