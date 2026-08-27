import { useCallback, useEffect, useRef, useState } from "react";
import {
  useLocation,
  useNavigate,
  useNavigationType,
  useParams,
} from "react-router";
import { AppShell } from "@/components/AppShell";
import { VideoDetailLoading } from "@/components/VideoDetailLoading";
import { VideoPlayer } from "@/components/VideoPlayer";
import { VideoActions } from "@/components/VideoActions";
import { VideoMetaHeader } from "@/components/VideoMetaHeader";
import { VideoInfoPanel } from "@/components/VideoInfoPanel";
import { MobileVideoCollection } from "@/components/MobileVideoCollection";
import { RecommendedRail } from "@/components/RecommendedRail";
import {
  consumePrefetchedVideoDetail,
  consumePrefetchedVideoRecommendations,
  deleteVideo,
  fetchTags,
  fetchVideoCollectionSummary,
  fetchVideoDetail,
  fetchVideoRecommendations,
  fetchVideoSubtitles,
  readCachedTags,
  recordView,
  updateVideoTags,
} from "@/data/videos";
import { useAuth } from "@/admin/AuthContext";
import { useDocumentScrollLock } from "@/lib/useDocumentScrollLock";
import { resolveVideoReturnPath } from "@/lib/videoReturnPath";
import type {
  TagItem,
  VideoCollectionSummary,
  VideoDetail,
  VideoItem,
} from "@/types";
import type { VideoReactionCounts } from "@/lib/videoReaction";

const DETAIL_CACHE_LIMIT = 20;
const RECOMMENDATIONS_CACHE_LIMIT = 80;
const COLLECTION_SUMMARY_CACHE_LIMIT = 20;

type VideoDetailSnapshot = {
  detail: VideoDetail;
};

const cachedVideoDetailsByID = new Map<string, VideoDetailSnapshot>();
const cachedRecommendationsByID = new Map<string, VideoItem[]>();
const cachedCollectionSummariesByID = new Map<
  string,
  VideoCollectionSummary | null
>();

function readCachedVideoDetail(id: string): VideoDetailSnapshot | null {
  return cachedVideoDetailsByID.get(id) ?? null;
}

function rememberVideoDetail(snapshot: VideoDetailSnapshot) {
  const id = snapshot.detail.id;
  cachedVideoDetailsByID.delete(id);
  cachedVideoDetailsByID.set(id, snapshot);

  if (cachedVideoDetailsByID.size > DETAIL_CACHE_LIMIT) {
    const oldestID = cachedVideoDetailsByID.keys().next().value;
    if (oldestID) cachedVideoDetailsByID.delete(oldestID);
  }
}

function forgetVideoDetail(id: string) {
  cachedVideoDetailsByID.delete(id);
  cachedRecommendationsByID.delete(id);
  cachedCollectionSummariesByID.delete(id);
}

function rememberCollectionSummary(
  id: string,
  summary: VideoCollectionSummary | null
) {
  cachedCollectionSummariesByID.delete(id);
  cachedCollectionSummariesByID.set(id, summary);
  if (cachedCollectionSummariesByID.size > COLLECTION_SUMMARY_CACHE_LIMIT) {
    const oldestID = cachedCollectionSummariesByID.keys().next().value;
    if (oldestID) cachedCollectionSummariesByID.delete(oldestID);
  }
}

function readCachedRecommendations(id: string): VideoItem[] | null {
  return cachedRecommendationsByID.get(id) ?? null;
}

function rememberRecommendations(id: string, videos: VideoItem[]) {
  if (cachedRecommendationsByID.has(id)) return;
  if (cachedRecommendationsByID.size >= RECOMMENDATIONS_CACHE_LIMIT) {
    const oldestID = cachedRecommendationsByID.keys().next().value;
    if (oldestID) cachedRecommendationsByID.delete(oldestID);
  }
  cachedRecommendationsByID.set(id, videos);
}

export default function VideoDetailPage() {
  const { id } = useParams<{ id: string }>();

  // 参数变化时明确卸载上一台播放器；JSON 快照由下面的轻量缓存恢复。
  return <VideoDetailContent key={id ?? "missing"} id={id} />;
}

function VideoDetailContent({ id }: { id?: string }) {
  const navigate = useNavigate();
  const location = useLocation();
  const navigationType = useNavigationType();
  const { isAdmin } = useAuth();
  const locationState = location.state as { from?: unknown } | null;
  const [initialSnapshot] = useState<VideoDetailSnapshot | null>(() =>
    id ? readCachedVideoDetail(id) : null
  );
  const [detail, setDetail] = useState<VideoDetail | null>(
    initialSnapshot?.detail ?? null
  );
  const [initialRecommendations] = useState<VideoItem[] | null>(() =>
    id ? readCachedRecommendations(id) : null
  );
  const [recommendations, setRecommendations] = useState<VideoItem[]>(
    initialRecommendations ?? []
  );
  const [recommendationsLoading, setRecommendationsLoading] = useState(
    !!id && initialRecommendations === null
  );
  const [recommendationsError, setRecommendationsError] = useState("");
  const [recommendationsLoadVersion, setRecommendationsLoadVersion] =
    useState(0);
  const [availableTags, setAvailableTags] = useState<TagItem[]>(
    () => readCachedTags() ?? []
  );
  const [availableTagsLoading, setAvailableTagsLoading] = useState(false);
  const [availableTagsError, setAvailableTagsError] = useState("");
  const [availableTagsLoadVersion, setAvailableTagsLoadVersion] = useState(0);
  const [collectionSummary, setCollectionSummary] =
    useState<VideoCollectionSummary | null>(() =>
      id ? (cachedCollectionSummariesByID.get(id) ?? null) : null
    );
  const [loading, setLoading] = useState(initialSnapshot === null);
  const [tagSaving, setTagSaving] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteSource, setDeleteSource] = useState(false);
  const [deleteSaving, setDeleteSaving] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const reactionCountsRef = useRef<
    (VideoReactionCounts & { videoId: string }) | null
  >(null);

  useDocumentScrollLock(deleteOpen && isAdmin);

  useEffect(() => {
    if (!id) {
      setLoading(false);
      document.title = "视频不存在";
      return;
    }
    let active = true;
    if (navigationType !== "POP") {
      window.scrollTo({ top: 0, behavior: "auto" });
    }
    if (initialSnapshot) {
      // effect 中更新最近使用顺序，保持 React 的 state initializer 无副作用。
      rememberVideoDetail(initialSnapshot);
      document.title = initialSnapshot.detail.title;
    }

    // 命中快照时保留当前画面，在后台静默校验最新详情。
    // 字幕只在用户打开播放器的字幕菜单后请求。
    const prefetchedDetail = consumePrefetchedVideoDetail(id);
    const detailRequest = prefetchedDetail
      ? prefetchedDetail.then((value) => value ?? fetchVideoDetail(id))
      : fetchVideoDetail(id);

    detailRequest.then((d) => {
      if (!active) return;
      let stableDetail = d;
      const localReactionCounts = reactionCountsRef.current;
      if (stableDetail && localReactionCounts?.videoId === stableDetail.id) {
        stableDetail = {
          ...stableDetail,
          likes: localReactionCounts.likes,
          dislikes: localReactionCounts.dislikes,
        };
      }

      // 请求短暂失败时 fetchVideoDetail 会返回 null；已有快照比错误空态更有用。
      if (!stableDetail && initialSnapshot) {
        setLoading(false);
        return;
      }

      if (stableDetail) rememberVideoDetail({ detail: stableDetail });
      setDetail(stableDetail);
      setLoading(false);
      document.title = stableDetail ? stableDetail.title : "视频不存在";
    });
    return () => {
      active = false;
    };
  }, [id, initialSnapshot, navigationType]);

  useEffect(() => {
    if (!id) {
      setRecommendations([]);
      setRecommendationsLoading(false);
      setRecommendationsError("");
      return;
    }
    if (initialRecommendations !== null && recommendationsLoadVersion === 0) {
      consumePrefetchedVideoRecommendations(id);
      setRecommendationsLoading(false);
      setRecommendationsError("");
      return;
    }

    const controller = new AbortController();
    let active = true;
    const prefetchedRecommendations =
      recommendationsLoadVersion === 0
        ? consumePrefetchedVideoRecommendations(id)
        : null;
    const recommendationsRequest =
      prefetchedRecommendations ??
      fetchVideoRecommendations(id, { signal: controller.signal });

    setRecommendationsLoading(true);
    setRecommendationsError("");
    recommendationsRequest
      .then((videos) => {
        if (!active) return;
        rememberRecommendations(id, videos);
        setRecommendations(cachedRecommendationsByID.get(id) ?? videos);
      })
      .catch((error: unknown) => {
        if (
          active &&
          !(error instanceof DOMException && error.name === "AbortError")
        ) {
          setRecommendationsError("推荐视频加载失败，请稍后重试");
        }
      })
      .finally(() => {
        if (active) setRecommendationsLoading(false);
      });

    return () => {
      active = false;
      if (!prefetchedRecommendations) controller.abort();
    };
  }, [id, initialRecommendations, recommendationsLoadVersion]);

  useEffect(() => {
    if (!isAdmin) {
      setAvailableTagsLoading(false);
      setAvailableTagsError("");
      return;
    }

    const cached = readCachedTags();
    if (cached !== null) {
      setAvailableTags(cached);
      setAvailableTagsLoading(false);
      setAvailableTagsError("");
      return;
    }

    let active = true;
    setAvailableTagsLoading(true);
    setAvailableTagsError("");
    fetchTags()
      .then((tagList) => {
        if (active) setAvailableTags(tagList);
      })
      .catch(() => {
        if (active) setAvailableTagsError("标签加载失败，请稍后重试");
      })
      .finally(() => {
        if (active) setAvailableTagsLoading(false);
      });
    return () => {
      active = false;
    };
  }, [availableTagsLoadVersion, isAdmin]);

  useEffect(() => {
    if (!id || !detail?.collectionCandidate) {
      if (id) cachedCollectionSummariesByID.delete(id);
      setCollectionSummary(null);
      return;
    }

    const controller = new AbortController();
    let active = true;
    fetchVideoCollectionSummary(id, { signal: controller.signal })
      .then((summary) => {
        if (!active) return;
        rememberCollectionSummary(id, summary);
        setCollectionSummary(summary);
      })
      .catch((error: unknown) => {
        if (
          active &&
          !(error instanceof DOMException && error.name === "AbortError")
        ) {
          setCollectionSummary(
            cachedCollectionSummariesByID.get(id) ?? null
          );
        }
      });
    return () => {
      active = false;
      controller.abort();
    };
  }, [detail?.collectionCandidate, id]);

  async function handleTagsChange(nextTags: string[]) {
    if (!detail) return;
    setTagSaving(true);
    try {
      const updated = await updateVideoTags(detail.id, nextTags);
      const nextDetail = { ...detail, tags: updated.tags ?? [] };
      setDetail(nextDetail);
      rememberVideoDetail({ detail: nextDetail });
    } finally {
      setTagSaving(false);
    }
  }

  function handleOpenDelete() {
    if (!isAdmin || !detail || deleteSaving) return;
    setDeleteSource(false);
    setDeleteError("");
    setDeleteOpen(true);
  }

  function handleCloseDelete() {
    if (deleteSaving) return;
    setDeleteOpen(false);
    setDeleteError("");
  }

  async function handleConfirmDelete() {
    if (!detail || deleteSaving) return;
    setDeleteSaving(true);
    setDeleteError("");
    try {
      await deleteVideo(detail.id, { deleteSource });
      forgetVideoDetail(detail.id);
      const from = typeof locationState?.from === "string" ? locationState.from : null;
      navigate(resolveVideoReturnPath(from), { replace: true });
    } catch {
      setDeleteError(
        deleteSource
          ? "删除失败，源文件未能删除，请检查WebDAV是否有删除权限"
          : "删除失败，请稍后重试。"
      );
      setDeleteSaving(false);
    }
  }

  function handleFirstPlay() {
    if (!detail) return;
    // 失败静默忽略，不打扰用户播放体验
    recordView(detail.id).catch(() => undefined);
  }

  const handleReactionCountsChange = useCallback(
    (counts: VideoReactionCounts) => {
      if (id) {
        reactionCountsRef.current = { videoId: id, ...counts };
      }
      setDetail((current) => {
        if (!current) return current;
        const nextDetail = {
          ...current,
          likes: counts.likes,
          dislikes: counts.dislikes,
        };
        rememberVideoDetail({ detail: nextDetail });
        return nextDetail;
      });
    },
    [id]
  );

  const loadSubtitles = useCallback(() => {
    if (!id) return Promise.resolve([]);
    return fetchVideoSubtitles(id);
  }, [id]);

  if (loading) {
    return <VideoDetailLoading isAdmin={isAdmin} />;
  }

  if (!detail) {
    return (
      <AppShell mobileAutoHideNav>
        <div className="vd-page">
          <div className="container vd-page__inner">
            <div className="vd-empty">视频不存在或已被移除</div>
          </div>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell mobileAutoHideNav>
      <div className="vd-page">
        {/* Ambient 背景层：用海报作模糊底色，叠加渐变过渡到页面背景 */}
        <div
          className="vd-ambient"
          aria-hidden="true"
          style={{
            backgroundImage: detail.poster
              ? `url(${detail.poster})`
              : undefined,
          }}
        />

        <div className="container vd-page__inner">
          <div className="vd-layout">
            <div className="vd-main">
              <div className="vd-player-wrap">
                <div className="vd-player">
                  <VideoPlayer
                    id={detail.id}
                    src={detail.videoSrc}
                    preferTypedMp4SourceOnIOS={isPikPakMp4Detail(detail)}
                    poster={detail.poster}
                    previewSrc={detail.previewSrc}
                    loadSubtitles={loadSubtitles}
                    title={detail.title}
                    onFirstPlay={handleFirstPlay}
                  />
                </div>
              </div>

              <div className="vd-detail-panels">
                <section className="vd-summary" aria-label="当前视频">
                  <VideoMetaHeader video={detail} />

                  <VideoActions
                    video={detail}
                    onDeleteVideo={handleOpenDelete}
                    deleteSaving={deleteSaving}
                    canDelete={isAdmin}
                    onReactionCountsChange={handleReactionCountsChange}
                  />
                </section>

                {collectionSummary && (
                  <MobileVideoCollection
                    videoId={detail.id}
                    collection={collectionSummary}
                  />
                )}

                <VideoInfoPanel
                  video={detail}
                  availableTags={availableTags}
                  availableTagsLoading={availableTagsLoading}
                  availableTagsError={availableTagsError}
                  onRetryAvailableTags={() =>
                    setAvailableTagsLoadVersion((version) => version + 1)
                  }
                  tagSaving={tagSaving}
                  onTagsChange={isAdmin ? handleTagsChange : undefined}
                />
              </div>
            </div>

            <RecommendedRail
              videos={recommendations}
              videoId={detail.id}
              collection={collectionSummary ?? undefined}
              recommendationsLoading={recommendationsLoading}
              recommendationsError={recommendationsError}
              onRetryRecommendations={() =>
                setRecommendationsLoadVersion((version) => version + 1)
              }
            />
          </div>
        </div>
      </div>

      {deleteOpen && isAdmin && (
        <div className="vd-delete-modal" role="presentation">
          <div
            className="vd-delete-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="vd-delete-title"
          >
            <div className="vd-delete-head">
              <h2 id="vd-delete-title" className="vd-delete-title">
                删除视频
              </h2>
              <p className="vd-delete-text">
                确定删除「{detail.title}」吗？
              </p>
            </div>

            <label className="vd-delete-option">
              <input
                type="checkbox"
                checked={deleteSource}
                disabled={deleteSaving}
                onChange={(e) => setDeleteSource(e.target.checked)}
              />
              <span>
                <strong>同时删除视频源文件</strong>
              </span>
            </label>

            {deleteError && <div className="vd-delete-error">{deleteError}</div>}

            <div className="vd-delete-actions">
              <button
                type="button"
                className="vd-delete-action vd-delete-cancel"
                onClick={handleCloseDelete}
                disabled={deleteSaving}
              >
                取消
              </button>
              <button
                type="button"
                className="vd-delete-action vd-delete-confirm"
                onClick={handleConfirmDelete}
                disabled={deleteSaving}
              >
                {deleteSaving ? "删除中..." : "删除"}
              </button>
            </div>
          </div>
        </div>
      )}
    </AppShell>
  );
}

function isPikPakMp4Detail(detail: VideoDetail) {
  return (
    detail.mediaType?.toLowerCase() === "video/mp4" &&
    detail.sourceLabel?.toLowerCase().includes("pikpak") === true
  );
}
