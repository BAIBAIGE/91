import { useCallback, useEffect, useRef, useState } from "react";
import { fetchShortsNext, ShortsFeedExpiredError } from "@/data/videos";
import {
  BATCH_SIZE,
  clearShortsFeedState,
  EMPTY_SHORTS_FEED,
  loadShortsFeedState,
  mergeShortsQueue,
  planShortsPrefetch,
  requestShortsBatch,
  saveShortsFeedState,
  type QueuedShortsItem,
  type ShortsFeedState,
} from "./shortsFeed";

/**
 * 短视频队列：向服务端 token/cursor feed 拉取批次，维护页面内的视频
 * 队列、续播书签与预取节奏。activeIndex 是当前视口内的视频索引；
 * 队列因空库被丢弃时会调用 onQueueReset，让页面把 activeIndex 归零。
 */
export function useShortsFeed(activeIndex: number, onQueueReset: () => void) {
  // 已加入页面的视频队列（按出现顺序）
  const [items, setItems] = useState<QueuedShortsItem[]>([]);
  // 是否正在加载下一批，避免并发请求
  const [loading, setLoading] = useState(false);
  const loadingRef = useRef(false);
  // 后端报告"本轮已耗尽"，下次请求前会自动重置
  const [roundComplete, setRoundComplete] = useState(false);
  // 没有任何视频可放（库为空 / 全部隐藏）
  const [empty, setEmpty] = useState(false);
  // 请求失败和真实空库必须分开，不能再把断网误报为"没有视频"。
  const [loadError, setLoadError] = useState(false);
  const [initialFeedState] = useState(loadShortsFeedState);
  // 指向已经取到队列尾部的位置；只在内存中预取，不直接写 localStorage。
  const requestFeedRef = useRef<ShortsFeedState>(initialFeedState);
  // 当前页面队列中已经写入续播书签的最远索引。回滑只用于回看，不能让
  // localStorage 中的 token/cursor 倒退到旧视频或上一轮随机队列。
  const persistedFeedHighIndexRef = useRef(-1);
  const onQueueResetRef = useRef(onQueueReset);
  onQueueResetRef.current = onQueueReset;

  const loadMore = useCallback(async () => {
    if (loadingRef.current) return;
    loadingRef.current = true;
    setLoading(true);
    setLoadError(false);
    try {
      const outcome = await requestShortsBatch({
        feed: requestFeedRef.current,
        count: BATCH_SIZE,
        fetchNext: fetchShortsNext,
        isFeedExpiredError: (error) => error instanceof ShortsFeedExpiredError,
        commitFeed: (feed, event) => {
          requestFeedRef.current = feed;
          if (event === "expired") clearShortsFeedState();
        },
      });

      if (outcome.kind === "empty") {
        setEmpty(true);
        // 库在旧队列播放期间可能被清空。丢弃已经失效的队列并停止换轮，
        // 否则末条视频的预取 effect 会持续请求同一个空库。
        setItems([]);
        onQueueResetRef.current();
        persistedFeedHighIndexRef.current = -1;
        setRoundComplete(false);
        requestFeedRef.current = EMPTY_SHORTS_FEED;
        clearShortsFeedState();
        return;
      }

      setEmpty(false);
      setItems((prev) => mergeShortsQueue(prev, outcome.response));
      setRoundComplete(outcome.response.roundComplete);
    } catch {
      setLoadError(true);
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }, []);

  // 首次加载
  useEffect(() => {
    void loadMore();
  }, [loadMore]);

  // 只提交首次进入过的最远视频游标。预取不会跳过未观看条目；回滑也不会
  // 让书签倒退。刷新页面后从本次实际到达过的最远视频之后恢复。
  useEffect(() => {
    if (empty) return;
    const active = items[activeIndex];
    if (!active) return;

    if (activeIndex > persistedFeedHighIndexRef.current) {
      persistedFeedHighIndexRef.current = activeIndex;
      saveShortsFeedState({
        feedToken: active.feedToken,
        cursor: active.feedCursor,
      });
    }

    const plan = planShortsPrefetch({
      remainingAfterActive: items.length - 1 - activeIndex,
      loading,
      loadError,
      roundComplete,
    });
    if (plan === "none") return;
    if (plan === "new-round") {
      requestFeedRef.current = EMPTY_SHORTS_FEED;
      setRoundComplete(false);
    }
    void loadMore();
  }, [activeIndex, items, loading, loadError, empty, roundComplete, loadMore]);

  return { items, loading, empty, loadError, loadMore };
}
