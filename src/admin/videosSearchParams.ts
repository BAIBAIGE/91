const POSITIVE_INTEGER_PATTERN = /^[1-9]\d*$/;

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
