import {
  type UIEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

export const LOG_INITIAL_VISIBLE_COUNT = 100;
export const LOG_LOAD_MORE_COUNT = 200;
export const LOG_LOAD_MORE_THRESHOLD_PX = 72;
export const LOG_FOLLOW_TAIL_THRESHOLD_PX = 48;

type UseLogScrollerOptions<T> = {
  entries: T[];
  loading: boolean;
  viewKey: string;
  showRawLogs: boolean;
  fullscreen: boolean;
};

type ViewportTransitionSnapshot = {
  followTail: boolean;
  scrollRatio: number;
  anchorID?: string;
  anchorOffset?: number;
};

export function nextVisibleLogCount(current: number, total: number) {
  const safeCurrent = Math.max(0, current);
  const safeTotal = Math.max(0, total);
  return Math.min(safeCurrent + LOG_LOAD_MORE_COUNT, safeTotal);
}

export function selectVisibleLogEntries<T>(entries: T[], visibleCount: number) {
  const safeVisibleCount = Math.max(0, Math.floor(visibleCount));
  if (safeVisibleCount === 0) return [];
  return entries.length > safeVisibleCount
    ? entries.slice(-safeVisibleCount)
    : entries;
}

function isNearLogBottom(node: HTMLDivElement) {
  return (
    node.scrollHeight - node.scrollTop - node.clientHeight <=
    LOG_FOLLOW_TAIL_THRESHOLD_PX
  );
}

function viewportContentTop(viewport: HTMLDivElement) {
  const viewportTop = viewport.getBoundingClientRect().top;
  const banner = viewport.querySelector<HTMLElement>(".admin-log-load-more");
  return banner
    ? Math.max(viewportTop, banner.getBoundingClientRect().bottom)
    : viewportTop;
}

export function useLogScroller<T>({
  entries,
  loading,
  viewKey,
  showRawLogs,
  fullscreen,
}: UseLogScrollerOptions<T>) {
  const viewportRef = useRef<HTMLDivElement>(null);
  const pendingPrependScrollRef = useRef<{
    scrollHeight: number;
    scrollTop: number;
  } | null>(null);
  const pendingViewportTransitionRef =
    useRef<ViewportTransitionSnapshot | null>(null);
  const previousViewRef = useRef({ viewKey, entryCount: entries.length });
  const [visibleCount, setVisibleCount] = useState(LOG_INITIAL_VISIBLE_COUNT);
  const [followTail, setFollowTail] = useState(true);
  const followTailRef = useRef(true);

  const visibleEntries = useMemo(
    () => selectVisibleLogEntries(entries, visibleCount),
    [entries, visibleCount]
  );
  const hiddenEntryCount = Math.max(entries.length - visibleEntries.length, 0);
  const canLoadMore = hiddenEntryCount > 0;

  const updateFollowTail = useCallback((value: boolean) => {
    followTailRef.current = value;
    setFollowTail(value);
  }, []);

  const prepareViewportTransition = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;

    const maxScrollTop = Math.max(
      viewport.scrollHeight - viewport.clientHeight,
      0
    );
    const snapshot: ViewportTransitionSnapshot = {
      followTail: followTailRef.current || isNearLogBottom(viewport),
      scrollRatio: maxScrollTop > 0 ? viewport.scrollTop / maxScrollTop : 0,
    };
    const contentTop = viewportContentTop(viewport);
    const rows = viewport.querySelectorAll<HTMLElement>("[data-log-entry-id]");
    for (const row of rows) {
      const bounds = row.getBoundingClientRect();
      if (bounds.bottom <= contentTop) continue;
      snapshot.anchorID = row.dataset.logEntryId;
      snapshot.anchorOffset = bounds.top - contentTop;
      break;
    }
    pendingViewportTransitionRef.current = snapshot;
  }, []);

  const revealOlderEntries = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport || !canLoadMore || pendingPrependScrollRef.current) return;

    pendingPrependScrollRef.current = {
      scrollHeight: viewport.scrollHeight,
      scrollTop: viewport.scrollTop,
    };
    setVisibleCount((current) => nextVisibleLogCount(current, entries.length));
  }, [canLoadMore, entries.length]);

  useLayoutEffect(() => {
    const previous = previousViewRef.current;
    const viewChanged = previous.viewKey !== viewKey;
    const entryDelta = entries.length - previous.entryCount;
    previousViewRef.current = { viewKey, entryCount: entries.length };

    if (viewChanged) {
      pendingPrependScrollRef.current = null;
      pendingViewportTransitionRef.current = null;
      setVisibleCount(LOG_INITIAL_VISIBLE_COUNT);
      updateFollowTail(true);
      return;
    }

    if (!followTail && entryDelta > 0) {
      // Keep every currently rendered row mounted while new rows arrive below it.
      setVisibleCount((current) =>
        Math.min(entries.length, current + entryDelta)
      );
      return;
    }

    if (entryDelta < 0) {
      setVisibleCount((current) =>
        Math.min(current, Math.max(LOG_INITIAL_VISIBLE_COUNT, entries.length))
      );
    }
  }, [entries.length, followTail, updateFollowTail, viewKey]);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    const pending = pendingPrependScrollRef.current;
    if (!viewport || !pending) return;

    const addedHeight = viewport.scrollHeight - pending.scrollHeight;
    viewport.scrollTop = pending.scrollTop + addedHeight;
    pendingPrependScrollRef.current = null;
  }, [visibleCount]);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    const pending = pendingViewportTransitionRef.current;
    if (!viewport || !pending) return;

    if (pending.followTail) {
      viewport.scrollTop = viewport.scrollHeight;
    } else {
      let anchor: HTMLElement | undefined;
      if (pending.anchorID !== undefined) {
        const rows = viewport.querySelectorAll<HTMLElement>(
          "[data-log-entry-id]"
        );
        anchor = Array.from(rows).find(
          (row) => row.dataset.logEntryId === pending.anchorID
        );
      }

      if (anchor && pending.anchorOffset !== undefined) {
        const currentOffset =
          anchor.getBoundingClientRect().top - viewportContentTop(viewport);
        viewport.scrollTop += currentOffset - pending.anchorOffset;
      } else {
        const maxScrollTop = Math.max(
          viewport.scrollHeight - viewport.clientHeight,
          0
        );
        viewport.scrollTop = maxScrollTop * pending.scrollRatio;
      }
    }
    pendingViewportTransitionRef.current = null;
  }, [fullscreen]);

  useLayoutEffect(() => {
    const viewport = viewportRef.current;
    if (!followTail || !viewport) return;
    viewport.scrollTop = viewport.scrollHeight;
  }, [fullscreen, followTail, showRawLogs, visibleEntries]);

  const tryAutoLoadUntilScrollable = useCallback(() => {
    const viewport = viewportRef.current;
    if (
      loading ||
      !viewport ||
      !canLoadMore ||
      pendingPrependScrollRef.current
    ) {
      return;
    }
    if (viewport.scrollHeight > viewport.clientHeight + 1) return;
    revealOlderEntries();
  }, [canLoadMore, loading, revealOlderEntries]);

  useEffect(() => {
    const frame = window.requestAnimationFrame(tryAutoLoadUntilScrollable);
    return () => window.cancelAnimationFrame(frame);
  }, [
    fullscreen,
    showRawLogs,
    tryAutoLoadUntilScrollable,
    viewKey,
    visibleEntries.length,
  ]);

  useEffect(() => {
    const handleResize = () => {
      window.requestAnimationFrame(tryAutoLoadUntilScrollable);
    };
    window.addEventListener("resize", handleResize);
    return () => window.removeEventListener("resize", handleResize);
  }, [tryAutoLoadUntilScrollable]);

  const handleViewportScroll = useCallback(
    (event: UIEvent<HTMLDivElement>) => {
      const viewport = event.currentTarget;
      updateFollowTail(isNearLogBottom(viewport));
      if (viewport.scrollTop <= LOG_LOAD_MORE_THRESHOLD_PX) {
        revealOlderEntries();
      }
    },
    [revealOlderEntries, updateFollowTail]
  );

  const enableFollowTail = useCallback(() => {
    updateFollowTail(true);
    window.requestAnimationFrame(() => {
      const viewport = viewportRef.current;
      if (viewport) viewport.scrollTop = viewport.scrollHeight;
    });
  }, [updateFollowTail]);

  return {
    viewportRef,
    visibleEntries,
    hiddenEntryCount,
    canLoadMore,
    followTail,
    handleViewportScroll,
    enableFollowTail,
    prepareViewportTransition,
  };
}
