import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  canRestoreScrollY,
  readListingScrollEntry,
  resolveReachableScrollY,
  resolveRestoreCount,
  resolveRestoreFeedToken,
  resolveRestoreScrollY,
  writeListingScrollEntry,
  type FeedSnapshotRestoreScope,
  type ListingScrollStorage,
} from "@/lib/listingScrollRestore";

/**
 * 无限滚动列表的前进/后退现场恢复。历史条目的 key 是存储键，因此"后退"
 * 拿回的是那一条历史自己的进度，重新进入列表则是干净的新会话。
 *
 * 拆成两个 hook 是因为存在先后依赖：要补回多少条必须在数据层发起第一个
 * 请求之前就确定，而落盘进度又依赖数据层已经请求到的条数。
 */

// 内容还没渲染够时滚不到目标位置，按帧重试；超过上限就放弃，避免死循环。
const RESTORE_MAX_FRAMES = 90;

// 模块实例与当前 Document 同寿命：SPA 路由往返时保持不变，浏览器刷新后
// 会生成新值，因此可以只让易变快照在当前 Document 内恢复。
const LISTING_DOCUMENT_ID =
  typeof globalThis.crypto?.randomUUID === "function"
    ? globalThis.crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;

function sessionStorageOrNull(): ListingScrollStorage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

export type ListingRestoreTarget = {
  historyKey: string;
  queryKey: string;
  count: number;
  feedToken: string;
  scrollY: number;
  feedSnapshotScope: FeedSnapshotRestoreScope;
};

/**
 * 解析当前历史条目要恢复的进度。在渲染期读取（而不是 effect）：数据层的
 * 首个请求就发生在这次渲染之后，晚一拍就会先打一个只有首屏的请求。
 */
export function useListingRestoreTarget(input: {
  historyKey: string;
  queryKey: string;
  pageSize: number;
  feedSnapshotScope?: FeedSnapshotRestoreScope;
}): ListingRestoreTarget {
  const targetRef = useRef<ListingRestoreTarget | null>(null);
  const feedSnapshotScope = input.feedSnapshotScope ?? "session";
  if (
    !targetRef.current ||
    targetRef.current.historyKey !== input.historyKey ||
    targetRef.current.queryKey !== input.queryKey ||
    targetRef.current.feedSnapshotScope !== feedSnapshotScope
  ) {
    const entry = readListingScrollEntry(
      sessionStorageOrNull(),
      input.historyKey
    );
    targetRef.current = {
      historyKey: input.historyKey,
      queryKey: input.queryKey,
      count: resolveRestoreCount({
        entry,
        queryKey: input.queryKey,
        pageSize: input.pageSize,
        documentID: LISTING_DOCUMENT_ID,
      }),
      feedToken: resolveRestoreFeedToken(entry, input.queryKey, {
        scope: feedSnapshotScope,
        documentID: LISTING_DOCUMENT_ID,
      }),
      scrollY: resolveRestoreScrollY(
        entry,
        input.queryKey,
        LISTING_DOCUMENT_ID
      ),
      feedSnapshotScope,
    };
  }
  return targetRef.current;
}

export type UseListingScrollRestoreInput = {
  target: ListingRestoreTarget;
  queryKey: string;
  /** 当前已经请求过的条目数，作为下次恢复的进度。 */
  requestedCount: number;
  feedToken: string;
  itemCount: number;
  /** Retained listing routes stay mounted but must not own global scroll work. */
  active?: boolean;
};

type ListingScrollSession = {
  identity: string;
  historyKey: string;
  queryKey: string;
  feedToken: string;
  requestedCount: number;
  pendingScrollY: number;
  lastScrollY: number;
  initialScrollPrepared: boolean;
  lastPersistedSignature: string;
};

export function useListingScrollRestore({
  target,
  queryKey,
  requestedCount,
  feedToken,
  itemCount,
  active = true,
}: UseListingScrollRestoreInput) {
  const historyKey = target.historyKey;
  const restoreIdentity = `${historyKey}\u0000${queryKey}`;
  const sessionRef = useRef<ListingScrollSession | null>(null);
  if (!sessionRef.current || sessionRef.current.identity !== restoreIdentity) {
    sessionRef.current = {
      identity: restoreIdentity,
      historyKey,
      queryKey,
      feedToken,
      requestedCount,
      pendingScrollY: target.scrollY,
      lastScrollY: target.scrollY,
      initialScrollPrepared: false,
      lastPersistedSignature: "",
    };
  } else {
    // 请求进度可以变化很多次；滚动监听器始终读取同一个会话对象的最新值。
    sessionRef.current.feedToken = feedToken;
    sessionRef.current.requestedCount = requestedCount;
  }
  const session = sessionRef.current;
  const [restoring, setRestoring] = useState(target.scrollY > 0);

  const persist = useCallback(() => {
    // Persisting before restoration completes would overwrite the saved target
    // with the temporary position used while content is still being rebuilt.
    if (session.pendingScrollY > 0 || session.requestedCount <= 0) return;
    const scrollY = Math.max(0, Math.round(session.lastScrollY));
    const signature = `${session.feedToken}\u0000${session.requestedCount}\u0000${scrollY}`;
    if (signature === session.lastPersistedSignature) return;
    writeListingScrollEntry(sessionStorageOrNull(), session.historyKey, {
      queryKey: session.queryKey,
      feedToken: session.feedToken,
      documentID: LISTING_DOCUMENT_ID,
      requestedCount: session.requestedCount,
      scrollY,
    });
    session.lastPersistedSignature = signature;
  }, [session]);

  useLayoutEffect(() => {
    if (!active) return;
    // 该 hook 已经按 history entry 恢复列表位置，挂载期间不再让浏览器进行
    // 第二套自动恢复。新 Document 或新列表没有恢复目标时则明确从顶部开始。
    const supportsManualRestoration =
      "scrollRestoration" in window.history;
    if (supportsManualRestoration) {
      window.history.scrollRestoration = "manual";
    }
    if (!session.initialScrollPrepared && target.scrollY <= 0) {
      window.scrollTo({ top: 0, left: 0, behavior: "auto" });
    }
    session.initialScrollPrepared = true;

    return () => {
      // Deactivation happens before the foreground route locks the document,
      // so this is the last reliable moment to persist the real list offset.
      persist();
      if (supportsManualRestoration) {
        // Same-document detail entries inherit the current entry's mode.
        // They own an independent scroll surface, so browser restoration must
        // be enabled again before the listing becomes inactive.
        window.history.scrollRestoration = "auto";
      }
    };
  }, [active, persist, restoreIdentity, session, target.scrollY]);

  useEffect(() => {
    if (!active) return;
    const targetScrollY = session.pendingScrollY;
    if (targetScrollY <= 0) {
      setRestoring(false);
      return;
    }
    setRestoring(true);
    if (itemCount === 0) return;

    let frame = 0;
    let handle = 0;
    const finish = (restoredScrollY: number) => {
      session.pendingScrollY = 0;
      session.lastScrollY = restoredScrollY;
      setRestoring(false);
    };
    const attempt = () => {
      if (
        canRestoreScrollY({
          targetScrollY,
          documentHeight: document.documentElement.scrollHeight,
          viewportHeight: window.innerHeight,
        })
      ) {
        window.scrollTo(0, targetScrollY);
        finish(targetScrollY);
        return;
      }
      frame += 1;
      if (frame >= RESTORE_MAX_FRAMES) {
        // 保存的位置比恢复上限更深时，停在能到达的最远处而不是回到顶部。
        const reachable = resolveReachableScrollY({
          targetScrollY,
          documentHeight: document.documentElement.scrollHeight,
          viewportHeight: window.innerHeight,
        });
        window.scrollTo(0, reachable);
        finish(reachable);
        return;
      }
      handle = window.requestAnimationFrame(attempt);
    };
    handle = window.requestAnimationFrame(attempt);

    return () => window.cancelAnimationFrame(handle);
  }, [active, itemCount, restoreIdentity, session]);

  useLayoutEffect(() => {
    if (!active) return;
    const handleScroll = () => {
      // 位置要在滚动事件里同步记下：详情 Surface 随后会锁住 document，
      // 停用阶段再读取 window.scrollY 已经不一定是列表的真实位置。
      session.lastScrollY = Math.max(0, Math.round(window.scrollY));
    };
    const handlePageHide = () => {
      session.lastScrollY = Math.max(0, Math.round(window.scrollY));
      persist();
    };

    window.addEventListener("scroll", handleScroll, { passive: true });
    window.addEventListener("pagehide", handlePageHide);
    return () => {
      window.removeEventListener("scroll", handleScroll);
      window.removeEventListener("pagehide", handlePageHide);
      // 停用或离开列表页时把最后的位置落盘，供冷恢复路径使用。
      persist();
    };
  }, [active, persist, restoreIdentity, session]);

  return { restoring };
}
