import type { SortKey } from "../types";

export type ListingViewMode = "grid" | "compact";

export function readListingSort(params: URLSearchParams): SortKey {
  const sort = params.get("sort");
  switch (sort) {
    case "latest":
    case "recent":
    case "hot":
      return sort;
    default:
      return "hot";
  }
}

export function withListingSort(
  params: URLSearchParams,
  sort: SortKey
): URLSearchParams {
  const next = new URLSearchParams(params);
  if (sort === "hot") {
    next.delete("sort");
  } else {
    next.set("sort", sort);
  }
  return next;
}

export function readListingPage(params: URLSearchParams): number {
  const raw = params.get("page");
  if (!raw || !/^\d+$/.test(raw)) return 1;
  const page = Number.parseInt(raw, 10);
  return Number.isSafeInteger(page) && page > 0 ? page : 1;
}

export function withListingPage(
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

export function readListingView(params: URLSearchParams): ListingViewMode {
  return params.get("view") === "compact" ? "compact" : "grid";
}

export function withListingView(
  params: URLSearchParams,
  view: ListingViewMode
): URLSearchParams {
  const next = new URLSearchParams(params);
  if (view === "compact") {
    next.set("view", "compact");
  } else {
    next.delete("view");
  }
  return next;
}

export function withListingNavigation(
  params: URLSearchParams,
  patch: { page?: number; sort?: SortKey; view?: ListingViewMode }
): URLSearchParams {
  let next = new URLSearchParams(params);
  if (patch.sort !== undefined) next = withListingSort(next, patch.sort);
  if (patch.page !== undefined) next = withListingPage(next, patch.page);
  if (patch.view !== undefined) next = withListingView(next, patch.view);
  return next;
}
