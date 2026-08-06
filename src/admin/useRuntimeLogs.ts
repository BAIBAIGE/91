import { useCallback, useEffect, useRef, useState } from "react";
import * as api from "./api";

export const LOG_REFRESH_INTERVAL_MS = 3000;
export const LOG_FETCH_BATCH_LIMIT = 1000;
export const LOG_BUFFER_LIMIT = 10000;

const LOG_MAX_RETRY_DELAY_MS = 30000;
const LOG_CATCH_UP_DELAY_MS = 100;
const LOG_MAX_CATCH_UP_BATCHES = 5;

type ActiveLogRequest = {
  controller: AbortController;
  version: number;
};

type RefreshOutcome =
  | { status: "success"; catchUp: boolean }
  | { status: "failed" | "busy" | "cancelled" };

type LoadTailOptions = {
  interrupt: boolean;
  showProgress: boolean;
};

function isAbortError(error: unknown) {
  return (
    typeof error === "object" &&
    error !== null &&
    "name" in error &&
    error.name === "AbortError"
  );
}

function boundedEntries(entries: api.AdminLogEntry[], limit = LOG_BUFFER_LIMIT) {
  return entries.length > limit ? entries.slice(-limit) : entries;
}

export function mergeRuntimeLogEntries(
  current: api.AdminLogEntry[],
  incoming: api.AdminLogEntry[],
  limit = LOG_BUFFER_LIMIT
) {
  if (incoming.length === 0) {
    return current.length > limit ? current.slice(-limit) : current;
  }

  const knownIDs = new Set(current.map((entry) => entry.id));
  const merged = [...current];
  for (const entry of incoming) {
    if (knownIDs.has(entry.id)) continue;
    knownIDs.add(entry.id);
    merged.push(entry);
  }
  if (merged.length === current.length && current.length <= limit) return current;
  return merged.length > limit ? merged.slice(-limit) : merged;
}

export function logRefreshRetryDelay(failureCount: number) {
  const exponent = Math.max(0, Math.min(failureCount - 1, 10));
  return Math.min(
    LOG_REFRESH_INTERVAL_MS * 2 ** exponent,
    LOG_MAX_RETRY_DELAY_MS
  );
}

function snapshotWithBoundedEntries(snapshot: api.AdminLogSnapshot) {
  return {
    ...snapshot,
    entries: boundedEntries(snapshot.entries),
  };
}

export function mergeRuntimeLogSnapshot(
  current: api.AdminLogSnapshot | null,
  latest: api.AdminLogSnapshot,
  incoming: api.AdminLogEntry[]
) {
  if (!current || latest.reset) return snapshotWithBoundedEntries(latest);

  const entries = mergeRuntimeLogEntries(current.entries, incoming);
  if (
    entries === current.entries &&
    current.matched === latest.matched &&
    current.storageBytes === latest.storageBytes &&
    current.maxStorageBytes === latest.maxStorageBytes
  ) {
    return current;
  }

  return {
    ...latest,
    entries,
  };
}

export function useRuntimeLogs({ autoRefresh }: { autoRefresh: boolean }) {
  const [snapshot, setSnapshot] = useState<api.AdminLogSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");

  const activeRequestRef = useRef<ActiveLogRequest | null>(null);
  const requestVersionRef = useRef(0);
  const cursorRef = useRef<string>();
  const readyRef = useRef(false);
  const hasLoadedRef = useRef(false);

  const invalidateActiveRequest = useCallback(() => {
    requestVersionRef.current += 1;
    activeRequestRef.current?.controller.abort();
    activeRequestRef.current = null;
  }, []);

  const beginRequest = useCallback((interrupt: boolean) => {
    if (activeRequestRef.current) {
      if (!interrupt) return null;
      activeRequestRef.current.controller.abort();
    }

    const request: ActiveLogRequest = {
      controller: new AbortController(),
      version: requestVersionRef.current + 1,
    };
    requestVersionRef.current = request.version;
    activeRequestRef.current = request;
    return request;
  }, []);

  const requestIsCurrent = useCallback(
    (request: ActiveLogRequest) => requestVersionRef.current === request.version,
    []
  );

  const finishRequest = useCallback((request: ActiveLogRequest) => {
    if (activeRequestRef.current === request) activeRequestRef.current = null;
  }, []);

  const loadTail = useCallback(
    async ({ interrupt, showProgress }: LoadTailOptions): Promise<RefreshOutcome> => {
      const request = beginRequest(interrupt);
      if (!request) return { status: "busy" };

      if (showProgress) {
        setError("");
        if (hasLoadedRef.current) setRefreshing(true);
        else setLoading(true);
      }

      try {
        const next = await api.listLogs(
          {
            limit: LOG_BUFFER_LIMIT,
          },
          request.controller.signal
        );
        if (!requestIsCurrent(request)) return { status: "cancelled" };

        cursorRef.current = next.nextCursor;
        readyRef.current = Boolean(next.nextCursor);
        hasLoadedRef.current = true;
        setSnapshot(snapshotWithBoundedEntries(next));
        setError("");
        return { status: "success", catchUp: false };
      } catch (loadError) {
        if (!requestIsCurrent(request) || isAbortError(loadError)) {
          return { status: "cancelled" };
        }
        setError(loadError instanceof Error ? loadError.message : "日志加载失败");
        return { status: "failed" };
      } finally {
        if (requestIsCurrent(request)) {
          setLoading(false);
          setRefreshing(false);
        }
        finishRequest(request);
      }
    },
    [beginRequest, finishRequest, requestIsCurrent]
  );

  const pollLogs = useCallback(async (): Promise<RefreshOutcome> => {
    const initialCursor = cursorRef.current;
    if (!readyRef.current || !initialCursor) {
      readyRef.current = false;
      return { status: "cancelled" };
    }

    const request = beginRequest(false);
    if (!request) return { status: "busy" };

    let cursor: string | undefined = initialCursor;
    let latest: api.AdminLogSnapshot | null = null;
    let incoming: api.AdminLogEntry[] = [];
    let catchUp = false;

    try {
      for (let batch = 0; batch < LOG_MAX_CATCH_UP_BATCHES; batch += 1) {
        if (!cursor) break;
        const next = await api.listLogs(
          {
            limit: LOG_FETCH_BATCH_LIMIT,
            cursor,
          },
          request.controller.signal
        );
        if (!requestIsCurrent(request)) return { status: "cancelled" };

        latest = next;
        if (next.reset) {
          // The reset response already carries a bounded tail and a fresh cursor.
          // Keep its reset marker so stale buffered entries are replaced atomically.
          cursor = next.nextCursor;
          catchUp = false;
          break;
        }

        incoming = mergeRuntimeLogEntries(incoming, next.entries);
        const nextCursor = next.nextCursor;
        catchUp = next.entries.length >= LOG_FETCH_BATCH_LIMIT;
        if (nextCursor) cursor = nextCursor;
        if (!catchUp || !nextCursor || document.hidden) break;
      }

      if (!requestIsCurrent(request)) return { status: "cancelled" };
      if (latest) {
        const latestSnapshot = latest;
        cursorRef.current = cursor;
        readyRef.current = Boolean(cursor);
        setSnapshot((current) =>
          mergeRuntimeLogSnapshot(current, latestSnapshot, incoming)
        );
      }
      setError("");
      return { status: "success", catchUp };
    } catch (loadError) {
      if (!requestIsCurrent(request) || isAbortError(loadError)) {
        return { status: "cancelled" };
      }
      setError(loadError instanceof Error ? loadError.message : "日志加载失败");
      return { status: "failed" };
    } finally {
      finishRequest(request);
    }
  }, [beginRequest, finishRequest, requestIsCurrent]);

  useEffect(() => {
    invalidateActiveRequest();
    cursorRef.current = undefined;
    readyRef.current = false;
    void loadTail({ interrupt: true, showProgress: true });
    return invalidateActiveRequest;
  }, [invalidateActiveRequest, loadTail]);

  useEffect(() => {
    if (!autoRefresh) return;

    let cancelled = false;
    let failures = 0;
    let timer: number | undefined;

    const clearTimer = () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = undefined;
    };

    const schedule = (delay: number) => {
      clearTimer();
      if (cancelled || document.hidden) return;
      timer = window.setTimeout(() => void tick(), delay);
    };

    async function tick() {
      timer = undefined;
      if (cancelled || document.hidden) return;

      const outcome = readyRef.current
        ? await pollLogs()
        : await loadTail({ interrupt: false, showProgress: false });
      if (cancelled || document.hidden) return;

      if (outcome.status === "failed") {
        failures += 1;
        schedule(logRefreshRetryDelay(failures));
        return;
      }
      if (outcome.status === "success") {
        failures = 0;
        schedule(outcome.catchUp ? LOG_CATCH_UP_DELAY_MS : LOG_REFRESH_INTERVAL_MS);
        return;
      }
      schedule(LOG_REFRESH_INTERVAL_MS);
    }

    const handleVisibilityChange = () => {
      clearTimer();
      if (!document.hidden) void tick();
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    schedule(LOG_REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearTimer();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [autoRefresh, loadTail, pollLogs]);

  const reload = useCallback(
    () => loadTail({ interrupt: true, showProgress: true }),
    [loadTail]
  );

  const resetAfterClear = useCallback(() => {
    invalidateActiveRequest();
    cursorRef.current = undefined;
    readyRef.current = false;
    setSnapshot((current) =>
      current
        ? {
            ...current,
            entries: [],
            matched: 0,
            storageBytes: 0,
            nextCursor: undefined,
            reset: true,
          }
        : current
    );
    setError("");
    void loadTail({ interrupt: true, showProgress: false });
  }, [invalidateActiveRequest, loadTail]);

  return {
    snapshot,
    loading,
    refreshing,
    error,
    reload,
    resetAfterClear,
  };
}
