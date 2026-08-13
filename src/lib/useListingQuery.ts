import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { fetchListing } from "@/data/videos";
import type { SortKey, VideoItem } from "@/types";

export type ListingQuery = {
  q: string;
  tag: string;
  sort: SortKey;
  page: number;
  pageSize: number;
};

export type ListingSnapshot = {
  key: string;
  query: ListingQuery;
  items: VideoItem[];
  total: number;
  receivedAt: number;
};

export type ListingQueryPhase =
  | "idle"
  | "initial-loading"
  | "refreshing"
  | "ready"
  | "error";

export type ListingQueryState = {
  phase: ListingQueryPhase;
  requestID: number;
  requestedQuery: ListingQuery | null;
  snapshot: ListingSnapshot | null;
  error: Error | null;
};

export type ListingQueryDisplay = {
  phase: ListingQueryPhase;
  snapshot: ListingSnapshot | null;
  transitioning: boolean;
  revalidating: boolean;
};

export type ListingQueryAction =
  | { type: "disable"; requestID: number }
  | {
      type: "start";
      requestID: number;
      query: ListingQuery;
      cached: ListingSnapshot | null;
      fresh: boolean;
    }
  | { type: "success"; requestID: number; snapshot: ListingSnapshot }
  | { type: "failure"; requestID: number; error: Error };

const LISTING_CACHE_TTL_MS = 60_000;
const LISTING_CACHE_MAX_ENTRIES = 20;
const listingCache = new Map<string, ListingSnapshot>();

function normalizeQuery(query: ListingQuery): ListingQuery {
  return {
    q: query.q.trim(),
    tag: query.tag.trim(),
    sort: query.sort,
    page: Number.isInteger(query.page) && query.page > 0 ? query.page : 1,
    pageSize:
      Number.isInteger(query.pageSize) && query.pageSize > 0
        ? query.pageSize
        : 1,
  };
}

export function listingQueryKey(query: ListingQuery): string {
  const normalized = normalizeQuery(query);
  return JSON.stringify([
    normalized.q,
    normalized.tag,
    normalized.sort,
    normalized.page,
    normalized.pageSize,
  ]);
}

function readListingCache(key: string): ListingSnapshot | null {
  const snapshot = listingCache.get(key) ?? null;
  if (!snapshot) return null;
  listingCache.delete(key);
  listingCache.set(key, snapshot);
  return snapshot;
}

// Render paths must not mutate the LRU order. The effect uses readListingCache
// after commit to record actual cache usage.
function peekListingCache(key: string): ListingSnapshot | null {
  return listingCache.get(key) ?? null;
}

function listingSnapshotIsFresh(snapshot: ListingSnapshot, now: number): boolean {
  return now - snapshot.receivedAt < LISTING_CACHE_TTL_MS;
}

function writeListingCache(snapshot: ListingSnapshot) {
  listingCache.delete(snapshot.key);
  listingCache.set(snapshot.key, snapshot);
  while (listingCache.size > LISTING_CACHE_MAX_ENTRIES) {
    const oldestKey = listingCache.keys().next().value as string | undefined;
    if (!oldestKey) break;
    listingCache.delete(oldestKey);
  }
}

function initialState(query: ListingQuery, enabled: boolean): ListingQueryState {
  if (!enabled) {
    return {
      phase: "idle",
      requestID: 0,
      requestedQuery: null,
      snapshot: null,
      error: null,
    };
  }

  const key = listingQueryKey(query);
  const cached = peekListingCache(key);
  const fresh = cached !== null && listingSnapshotIsFresh(cached, Date.now());
  return {
    phase: cached ? (fresh ? "ready" : "refreshing") : "initial-loading",
    requestID: 0,
    requestedQuery: query,
    snapshot: cached,
    error: null,
  };
}

export function listingQueryReducer(
  state: ListingQueryState,
  action: ListingQueryAction
): ListingQueryState {
  switch (action.type) {
    case "disable":
      return {
        phase: "idle",
        requestID: action.requestID,
        requestedQuery: null,
        snapshot: null,
        error: null,
      };
    case "start": {
      const snapshot = action.cached ?? state.snapshot;
      return {
        phase: action.fresh
          ? "ready"
          : snapshot
          ? "refreshing"
          : "initial-loading",
        requestID: action.requestID,
        requestedQuery: action.query,
        snapshot,
        error: null,
      };
    }
    case "success":
      if (action.requestID !== state.requestID) return state;
      return {
        phase: "ready",
        requestID: action.requestID,
        requestedQuery: action.snapshot.query,
        snapshot: action.snapshot,
        error: null,
      };
    case "failure":
      if (action.requestID !== state.requestID) return state;
      return {
        ...state,
        phase: "error",
        error: action.error,
      };
  }
}

export function listingQueryDisplayPhase(
  state: ListingQueryState,
  key: string,
  enabled: boolean,
  cachedForKey: ListingSnapshot | null = null,
  now = Date.now()
): ListingQueryPhase {
  return deriveListingQueryDisplay(state, key, enabled, cachedForKey, now).phase;
}

export function deriveListingQueryDisplay(
  state: ListingQueryState,
  key: string,
  enabled: boolean,
  cachedForKey: ListingSnapshot | null = null,
  now = Date.now()
): ListingQueryDisplay {
  if (!enabled) {
    return {
      phase: "idle",
      snapshot: null,
      transitioning: false,
      revalidating: false,
    };
  }

  const requestedKey = state.requestedQuery
    ? listingQueryKey(state.requestedQuery)
    : null;
  let phase = state.phase;
  let snapshot = state.snapshot;

  if (requestedKey !== key) {
    const targetSnapshot =
      state.snapshot?.key === key
        ? state.snapshot
        : cachedForKey?.key === key
        ? cachedForKey
        : null;
    if (targetSnapshot) {
      snapshot = targetSnapshot;
      phase = listingSnapshotIsFresh(targetSnapshot, now) ? "ready" : "refreshing";
    } else {
      phase = state.snapshot ? "refreshing" : "initial-loading";
    }
  }

  const refreshing = phase === "refreshing";
  const snapshotMatchesRequest = snapshot?.key === key;
  return {
    phase,
    snapshot,
    transitioning: refreshing && !snapshotMatchesRequest,
    revalidating: refreshing && snapshotMatchesRequest,
  };
}

function errorValue(error: unknown): Error {
  return error instanceof Error ? error : new Error("视频列表加载失败");
}

export function useListingQuery(
  input: ListingQuery,
  options: { enabled?: boolean } = {}
) {
  const enabled = options.enabled ?? true;
  const query = useMemo(
    () => normalizeQuery(input),
    [input.q, input.tag, input.sort, input.page, input.pageSize]
  );
  const key = listingQueryKey(query);
  const [state, dispatch] = useReducer(
    listingQueryReducer,
    undefined,
    () => initialState(query, enabled)
  );
  const [retryVersion, setRetryVersion] = useState(0);
  const nextRequestIDRef = useRef(0);

  useEffect(() => {
    const requestID = ++nextRequestIDRef.current;
    if (!enabled) {
      dispatch({ type: "disable", requestID });
      return;
    }

    const cached = readListingCache(key);
    const fresh = cached !== null && listingSnapshotIsFresh(cached, Date.now());
    dispatch({ type: "start", requestID, query, cached, fresh });
    if (fresh) return;

    const controller = new AbortController();
    fetchListing(
      query.page,
      query.pageSize,
      { q: query.q, tag: query.tag, sort: query.sort },
      { signal: controller.signal }
    )
      .then((result) => {
        const snapshot: ListingSnapshot = {
          key,
          query,
          items: result.items ?? [],
          total: result.total ?? 0,
          receivedAt: Date.now(),
        };
        writeListingCache(snapshot);
        dispatch({ type: "success", requestID, snapshot });
      })
      .catch((error) => {
        if (controller.signal.aborted) return;
        dispatch({ type: "failure", requestID, error: errorValue(error) });
      });

    return () => controller.abort();
  }, [enabled, key, query, retryVersion]);

  const retry = useCallback(() => setRetryVersion((version) => version + 1), []);
  const display = deriveListingQueryDisplay(
    state,
    key,
    enabled,
    peekListingCache(key)
  );

  return {
    ...state,
    phase: display.phase,
    snapshot: display.snapshot,
    query,
    key,
    retry,
    initialLoading: display.phase === "initial-loading",
    refreshing: display.phase === "refreshing",
    transitioning: display.transitioning,
    revalidating: display.revalidating,
  };
}

export function clearListingQueryCache() {
  listingCache.clear();
}
