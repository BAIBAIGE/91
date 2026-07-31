import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  Check,
  ChevronDown,
  Edit,
  RefreshCw,
  Search,
  Image,
  Trash2,
  Upload,
} from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { Modal } from "./Modal";
import { ConfirmModal } from "./ConfirmModal";
import { formatBytes } from "./storageFormat";
import { AdminEmptyVisual } from "./AdminEmptyVisual";
import { AdminLoading } from "./AdminLoading";
import { driveKindAbbr, driveKindIconPath, kindLabel } from "./drive/constants";
import { readAdminVideosPage, withAdminVideosPage } from "./videosSearchParams";

const DESKTOP_VIDEOS_PAGE_SIZE = 50;
const MOBILE_VIDEOS_PAGE_SIZE = 20;
const NORMAL_VIDEOS_PAGE_SIZE = 10;
const VIDEOS_MOBILE_QUERY = "(max-width: 640px)";
const REGEN_PREVIEW_STATUS = "generating";
const REGEN_PREVIEW_POLL_INTERVAL_MS = 2000;
const REGEN_PREVIEW_TRACK_TIMEOUT_MS = 30 * 60 * 1000;
const ADMIN_SEARCH_DEBOUNCE_MS = 500;
const LOCAL_UPLOAD_SOURCE_ID = "local-upload";

type TabKey = "current" | "blacklist";
type PageSetter = Dispatch<SetStateAction<number>>;

type RegenPreviewState = {
  expiresAt: number;
  originalUpdatedAt: number;
};

type VideoAdvancedFilterValues = {
  driveId: string;
  crawlerId: string;
  createdFrom: string;
  createdTo: string;
  durationMinMinutes: string;
  durationMaxMinutes: string;
};

const EMPTY_VIDEO_FILTERS: VideoAdvancedFilterValues = {
  driveId: "",
  crawlerId: "",
  createdFrom: "",
  createdTo: "",
  durationMinMinutes: "",
  durationMaxMinutes: "",
};

const TABS: { key: TabKey; label: string }[] = [
  { key: "current", label: "正常视频" },
  { key: "blacklist", label: "拉黑视频" },
];

/**
 * 视频管理容器：顶部分段标签在「当前 / 拉黑」两个视图间切换，
 * 激活标签和当前页同步到 URL 的 ?tab= / ?page=。
 */
export function VideosPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawTab = searchParams.get("tab");
  const activeTab: TabKey = rawTab === "blacklist" ? "blacklist" : "current";
  const page = readAdminVideosPage(searchParams);
  const setPage = useCallback<PageSetter>((nextPage) => {
    setSearchParams(
      (prev) => {
        const currentPage = readAdminVideosPage(prev);
        const resolvedPage = typeof nextPage === "function" ? nextPage(currentPage) : nextPage;
        return withAdminVideosPage(prev, resolvedPage);
      },
      { replace: true }
    );
  }, [setSearchParams]);

  function selectTab(key: TabKey) {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (key !== activeTab) next.delete("page");
        if (key === "current") next.delete("tab");
        else next.set("tab", key);
        return next;
      },
      { replace: true }
    );
  }

  return (
    <section>
      {activeTab === "current" && (
        <CurrentVideosTab
          tabSelector={<VideoTabSelector activeTab={activeTab} onSelect={selectTab} />}
          page={page}
          setPage={setPage}
        />
      )}
      {activeTab === "blacklist" && (
        <BlacklistTab
          tabSelector={<VideoTabSelector activeTab={activeTab} onSelect={selectTab} />}
          page={page}
          setPage={setPage}
        />
      )}
    </section>
  );
}

function VideoTabSelector({
  activeTab,
  onSelect,
}: {
  activeTab: TabKey;
  onSelect: (key: TabKey) => void;
}) {
  return (
    <div className="admin-video-tabs" role="tablist" aria-label="视频管理标签页">
      {TABS.map((t) => (
        <button
          key={t.key}
          type="button"
          role="tab"
          aria-selected={activeTab === t.key}
          className={`admin-video-tab ${activeTab === t.key ? "is-active" : ""}`}
          onClick={() => onSelect(t.key)}
        >
          <span>{t.label}</span>
        </button>
      ))}
    </div>
  );
}

// ---------- 当前视频 ----------

function CurrentVideosTab({
  tabSelector,
  page,
  setPage,
}: {
  tabSelector: ReactNode;
  page: number;
  setPage: PageSetter;
}) {
  const [list, setList] = useState<api.AdminVideo[]>([]);
  const [drives, setDrives] = useState<api.AdminDrive[]>([]);
  const [crawlers, setCrawlers] = useState<api.AdminCrawler[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [searchKeyword, setSearchKeyword] = useState("");
  const [advancedFiltersOpen, setAdvancedFiltersOpen] = useState(false);
  const [draftFilters, setDraftFilters] = useState<VideoAdvancedFilterValues>(() => ({ ...EMPTY_VIDEO_FILTERS }));
  const [appliedFilters, setAppliedFilters] = useState<VideoAdvancedFilterValues>(() => ({ ...EMPTY_VIDEO_FILTERS }));
  const [total, setTotal] = useState(0);
  const [editing, setEditing] = useState<api.AdminVideo | null>(null);
  const [availableTags, setAvailableTags] = useState<api.AdminTag[]>([]);
  const [selectMode, setSelectMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false);
  const [batchDeleting, setBatchDeleting] = useState(false);
  const [batchDeleteSource, setBatchDeleteSource] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<api.AdminVideo | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteSource, setDeleteSource] = useState(false);
  const [regenPreviewById, setRegenPreviewById] = useState<Record<string, RegenPreviewState>>({});
  const listRequestIdRef = useRef(0);
  const pageSize = NORMAL_VIDEOS_PAGE_SIZE;
  const { show } = useToast();
  const activeListQueryKey = JSON.stringify([page, searchKeyword, appliedFilters]);
  const activeListQueryKeyRef = useRef(activeListQueryKey);
  activeListQueryKeyRef.current = activeListQueryKey;

  async function refresh() {
    const requestId = ++listRequestIdRef.current;
    const queryKey = activeListQueryKey;
    setLoading(true);
    setLoadError("");
    try {
      const r = await api.listVideos({
        page,
        size: pageSize,
        keyword: searchKeyword,
        ...appliedFilters,
      });
      if (requestId !== listRequestIdRef.current || queryKey !== activeListQueryKeyRef.current) return;
      setList(r.items ?? []);
      setTotal(r.total ?? 0);
    } catch (e) {
      if (requestId !== listRequestIdRef.current || queryKey !== activeListQueryKeyRef.current) return;
      const message = e instanceof Error ? e.message : "加载失败";
      setLoadError(message);
      show(message, "error");
    } finally {
      if (requestId === listRequestIdRef.current && queryKey === activeListQueryKeyRef.current) {
        setLoading(false);
      }
    }
  }

  async function refreshListOnly() {
    const queryKey = activeListQueryKey;
    try {
      const r = await api.listVideos({
        page,
        size: pageSize,
        keyword: searchKeyword,
        ...appliedFilters,
      });
      if (queryKey !== activeListQueryKeyRef.current) return;
      setList(r.items ?? []);
      setTotal(r.total ?? 0);
    } catch {
      // Polling is only used to clear optimistic preview-generation state.
    }
  }

  const trackedRegenCount = Object.keys(regenPreviewById).length;
  const hasGeneratingPreview = list.some((v) => v.previewStatus === REGEN_PREVIEW_STATUS);

  useEffect(() => {
    refresh();
  }, [page, searchKeyword, pageSize, appliedFilters]);

  useEffect(() => {
    let active = true;
    void Promise.all([api.listTags(), api.listDrives(), api.listCrawlers()])
      .then(([tagList, driveList, crawlerList]) => {
        if (!active) return;
        setAvailableTags(tagList ?? []);
        setDrives(driveList ?? []);
        setCrawlers(crawlerList ?? []);
      })
      .catch((e) => {
        if (active) show(e instanceof Error ? e.message : "筛选选项加载失败", "error");
      });
    return () => {
      active = false;
    };
  }, [show]);

  useEffect(() => {
    setSelectedIds(new Set());
  }, [searchKeyword, appliedFilters]);

  useEffect(() => {
    if (keyword === searchKeyword) return;
    const timer = window.setTimeout(() => {
      setSearchKeyword(keyword);
      setPage(1);
    }, ADMIN_SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [keyword, searchKeyword]);

  useEffect(() => {
    if (trackedRegenCount === 0 && !hasGeneratingPreview) return;
    const timer = window.setInterval(() => {
      refreshListOnly();
    }, REGEN_PREVIEW_POLL_INTERVAL_MS);
    return () => window.clearInterval(timer);
  }, [trackedRegenCount, hasGeneratingPreview, page, pageSize, searchKeyword, appliedFilters]);

  useEffect(() => {
    if (trackedRegenCount === 0) return;
    const now = Date.now();
    setRegenPreviewById((current) => {
      const next = { ...current };
      let changed = false;
      const byId = new Map(list.map((v) => [v.id, v]));
      for (const [id, state] of Object.entries(current)) {
        const video = byId.get(id);
        const updatedAt = videoUpdatedAtMs(video);
        if (!video || now >= state.expiresAt || updatedAt > state.originalUpdatedAt) {
          delete next[id];
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }, [list, trackedRegenCount]);

  const driveNameMap = new Map(drives.map((d) => [d.id, d.name || d.id]));
  driveNameMap.set(LOCAL_UPLOAD_SOURCE_ID, "上传来源");

  const listItems = list;
  const editingVideo = editing ? (listItems.find((v) => v.id === editing.id) ?? editing) : null;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  useEffect(() => {
    if (!loading && page > totalPages) setPage(totalPages);
  }, [loading, page, totalPages, setPage]);
  const showPagination = totalPages > 1;
  const placeholderRows = showPagination ? Math.max(0, pageSize - listItems.length) : 0;
  const activeAdvancedFilterCount = countVideoAdvancedFilters(appliedFilters);
  const hasActiveSearch = searchKeyword.trim().length > 0 || activeAdvancedFilterCount > 0;
  const hasVideoActions = listItems.length > 0;
  const allPageSelected =
    listItems.length > 0 && listItems.every((video) => selectedIds.has(video.id));

  async function handleRegen(v: api.AdminVideo) {
    try {
      await api.regenPreview(v.id);
      trackRegeneratingPreview([v]);
      show("已触发预览视频重生", "success");
    } catch (e) {
      show(e instanceof Error ? e.message : "触发失败", "error");
    }
  }

  async function handleBatchDelete() {
    if (selectedIds.size === 0) return;
    setBatchDeleteSource(false);
    setBatchDeleteOpen(true);
  }

  function trackRegeneratingPreview(videos: api.AdminVideo[]) {
    if (videos.length === 0) return;
    const startedAt = Date.now();
    setRegenPreviewById((current) => {
      const next = { ...current };
      for (const v of videos) {
        next[v.id] = {
          expiresAt: startedAt + REGEN_PREVIEW_TRACK_TIMEOUT_MS,
          originalUpdatedAt: videoUpdatedAtMs(v),
        };
      }
      return next;
    });
  }

  function isPreviewGenerating(v: api.AdminVideo) {
    return !!regenPreviewById[v.id] || v.previewStatus === REGEN_PREVIEW_STATUS;
  }

  async function confirmDeleteVideo() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleting(true);
    try {
      const result = await api.deleteVideo(target.id, { deleteSource });
      setDeleteTarget(null);
      setDeleteSource(false);
      setSelectedIds((ids) => {
        const next = new Set(ids);
        next.delete(target.id);
        return next;
      });
      show(result.deletedSource ? "已删除视频，并清理源文件" : "已删除视频", "success");
      if (listItems.length === 1 && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      } else {
        refresh();
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "删除失败", "error");
    } finally {
      setDeleting(false);
    }
  }

  async function confirmBatchDelete() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;
    setBatchDeleting(true);
    try {
      let success = 0;
      let deletedSources = 0;
      const deletedIds = new Set<string>();
      for (const id of ids) {
        try {
          const result = await api.deleteVideo(id, { deleteSource: batchDeleteSource });
          success++;
          deletedIds.add(id);
          if (result.deletedSource) deletedSources++;
        } catch {
          // Keep deleting the rest of the selected videos; report aggregate failure below.
        }
      }
      const failed = ids.length - success;
      if (failed === 0) {
        const extra = deletedSources > 0 ? `，其中 ${deletedSources} 个清理了源文件` : "";
        show(`批量删除完成，成功 ${success} 个${extra}`, "success");
      } else {
        show(
          `批量删除完成，成功 ${success} / ${ids.length} 个，失败 ${failed} 个`,
          success > 0 ? "info" : "error"
        );
      }
      setSelectedIds(new Set());
      setBatchDeleteOpen(false);
      setBatchDeleteSource(false);
      setSelectMode(false);
      const currentPageEmptied =
        listItems.length > 0 && listItems.every((video) => deletedIds.has(video.id));
      if (currentPageEmptied && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      } else {
        refresh();
      }
    } finally {
      setBatchDeleting(false);
    }
  }

  const toggleSelect = (id: string) => {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectPageVideos = () => {
    setSelectedIds((current) => {
      const next = new Set(current);
      listItems.forEach((video) => next.add(video.id));
      return next;
    });
  };

  const toggleSelectMode = () => {
    setSelectMode((active) => !active);
    setSelectedIds(new Set());
  };

  function handleSearchSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSearchKeyword(keyword);
    setPage(1);
  }

  function openAdvancedFilters() {
    setDraftFilters({ ...appliedFilters });
    setAdvancedFiltersOpen(true);
  }

  function applyAdvancedFilters(e: React.FormEvent) {
    e.preventDefault();
    const today = localDateInputValue(new Date());
    if (
      dateIsAfter(draftFilters.createdFrom, today) ||
      dateIsAfter(draftFilters.createdTo, today)
    ) {
      show("入库时间不能超过当天", "error");
      return;
    }
    if (dateRangeIsReversed(draftFilters.createdFrom, draftFilters.createdTo)) {
      show("入库时间开始日期不能晚于结束日期", "error");
      return;
    }
    if (numberRangeIsReversed(draftFilters.durationMinMinutes, draftFilters.durationMaxMinutes)) {
      show("视频时长最短值不能大于最长值", "error");
      return;
    }
    setAppliedFilters({ ...draftFilters });
    setPage(1);
    setAdvancedFiltersOpen(false);
  }

  function clearAdvancedFilters() {
    setDraftFilters({ ...EMPTY_VIDEO_FILTERS });
  }

  return (
    <div className={`admin-videos-current${selectMode ? " has-bulk-actions" : ""}`}>
      <div className="admin-page__actions admin-videos-filter admin-videos-filter--current">
        <SearchBox keyword={keyword} onChange={setKeyword} onSubmit={handleSearchSubmit} />
        <div className="admin-videos-filter__current-actions">
          <button
            type="button"
            className="admin-btn admin-video-advanced-toggle"
            onClick={openAdvancedFilters}
            aria-haspopup="dialog"
          >
            <span>高级筛选</span>
            {activeAdvancedFilterCount > 0 && (
              <span className="admin-video-advanced-toggle__count" aria-label={`已启用 ${activeAdvancedFilterCount} 项筛选`}>
                {activeAdvancedFilterCount}
              </span>
            )}
          </button>
          {hasVideoActions && (
            <button
              type="button"
              className={`admin-btn admin-videos-filter__batch admin-videos-filter__batch-select${selectMode ? " is-primary" : ""}`}
              onClick={toggleSelectMode}
              aria-pressed={selectMode}
            >
              <span>{selectMode ? "退出选择" : "批量选择"}</span>
            </button>
          )}
        </div>
      </div>
      {tabSelector}
      <Modal
        open={advancedFiltersOpen}
        title="高级筛选"
        onClose={() => setAdvancedFiltersOpen(false)}
        className="admin-modal--video-filters"
        footer={
          <>
            <button type="button" className="admin-btn admin-video-advanced-clear" onClick={clearAdvancedFilters}>
              清空筛选
            </button>
            <div className="admin-video-advanced-modal-actions">
              <button type="button" className="admin-btn" onClick={() => setAdvancedFiltersOpen(false)}>
                取消
              </button>
              <button type="submit" form="admin-video-advanced-filters" className="admin-btn is-primary">
                应用
              </button>
            </div>
          </>
        }
      >
        <AdvancedVideoFilters
          value={draftFilters}
          drives={drives}
          crawlers={crawlers}
          onChange={setDraftFilters}
          onSubmit={applyAdvancedFilters}
        />
      </Modal>

      {!loading && selectMode && (
        <div className="admin-videos-list-toolbar">
          <div className="admin-videos-bulk-actions">
            <span className="admin-videos-bulk-actions__count">已选择 {selectedIds.size} 项</span>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn"
              onClick={selectPageVideos}
              disabled={listItems.length === 0 || allPageSelected}
            >
              全选本页
            </button>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn"
              onClick={() => setSelectedIds(new Set())}
              disabled={selectedIds.size === 0}
            >
              取消选中
            </button>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn"
              onClick={handleBatchDelete}
              disabled={selectedIds.size === 0}
            >
              批量删除
            </button>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn admin-videos-bulk-actions__mobile-exit"
              onClick={toggleSelectMode}
            >
              退出选择
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <LoadingState />
      ) : loadError ? (
        <ErrorState message={loadError} onRetry={refresh} />
      ) : listItems.length === 0 ? (
        <AdminEmptyVisual
          variant={hasActiveSearch ? "no-results" : "empty"}
          text={hasActiveSearch ? "未查询到" : "当前库中没有视频"}
          className="admin-empty-state admin-empty-state--plain"
        />
      ) : (
        <>
          <table className={`admin-table is-selectable admin-videos-table${selectMode ? " is-row-select-mode" : ""}`}>
            <tbody>
              {listItems.map((v) => {
                const isSelected = selectedIds.has(v.id);

                return (
                  <tr
                    key={v.id}
                    className={isSelected ? "is-selected" : ""}
                    aria-selected={selectMode ? isSelected : undefined}
                    tabIndex={selectMode ? 0 : undefined}
                    onClick={(event) => {
                      if (!selectMode || isInteractiveTarget(event.target)) return;
                      toggleSelect(v.id);
                    }}
                    onKeyDown={(event) => {
                      if (!selectMode || isInteractiveTarget(event.target)) return;
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      toggleSelect(v.id);
                    }}
                  >
                    <td data-label="标题">
                      <VideoTitleCell video={v} />
                    </td>
                    <td data-label="作者">{v.author || <span className="admin-text-faint">—</span>}</td>
                    <td data-label="时长">{formatDur(v.durationSeconds)}</td>
                    <td data-label="来源" className="admin-mono-cell">
                      {driveNameMap.get(v.driveId) ?? v.driveId}
                    </td>
                    <td className="is-actions" data-label="操作">
                      <button type="button" className="admin-btn" onClick={() => setEditing(v)} title="编辑视频">
                        <Edit size={13} />
                      </button>{" "}
                      <button
                        type="button"
                        className="admin-btn is-danger"
                        onClick={() => {
                          setDeleteSource(false);
                          setDeleteTarget(v);
                        }}
                        title="删除视频"
                      >
                        <Trash2 size={13} />
                      </button>
                    </td>
                  </tr>
                );
              })}
              {Array.from({ length: placeholderRows }, (_, index) => (
                <tr
                  key={`placeholder-${index}`}
                  className="admin-video-placeholder-row"
                  aria-hidden="true"
                >
                  <td data-label="标题">
                    <div className="admin-video-title-cell">
                      <div className="admin-video-thumb-wrap" aria-hidden="true" />
                      <div className="admin-video-title-body">
                        <div className="admin-video-title">placeholder</div>
                        <div className="admin-video-filemeta-pills">
                          <span className="admin-video-filemeta-pill">placeholder</span>
                        </div>
                      </div>
                    </div>
                  </td>
                  <td data-label="作者">placeholder</td>
                  <td data-label="时长">placeholder</td>
                  <td data-label="来源" className="admin-mono-cell">
                    placeholder
                  </td>
                  <td className="is-actions" data-label="操作">
                    <span className="admin-btn">placeholder</span>
                    <span className="admin-btn">placeholder</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {showPagination && <Pagination page={page} totalPages={totalPages} onPage={setPage} />}
        </>
      )}

      {editingVideo && (
        <EditVideoModal
          video={editingVideo}
          availableTags={availableTags}
          previewGenerating={isPreviewGenerating(editingVideo)}
          onRegenPreview={() => handleRegen(editingVideo)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            refresh();
          }}
        />
      )}
      <ConfirmModal
        open={deleteTarget !== null}
        title="删除视频"
        message={deleteTarget ? `确定要删除「${deleteTarget.title}」吗？` : ""}
        confirmText="确认"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm admin-modal--video-delete-flat"
        loading={deleting}
        onCancel={() => {
          if (!deleting) {
            setDeleteTarget(null);
            setDeleteSource(false);
          }
        }}
        onConfirm={confirmDeleteVideo}
      >
        <DeleteSourceOption checked={deleteSource} disabled={deleting} onChange={setDeleteSource} />
      </ConfirmModal>
      <ConfirmModal
        open={batchDeleteOpen}
        title="批量删除视频"
        message={`确定要删除已选中的 ${selectedIds.size} 个视频吗？`}
        confirmText="确认"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm admin-modal--video-delete-flat"
        loading={batchDeleting}
        onCancel={() => {
          if (!batchDeleting) {
            setBatchDeleteOpen(false);
            setBatchDeleteSource(false);
          }
        }}
        onConfirm={confirmBatchDelete}
      >
        <DeleteSourceOption checked={batchDeleteSource} disabled={batchDeleting} onChange={setBatchDeleteSource} />
      </ConfirmModal>
    </div>
  );
}

// ---------- 拉黑视频 ----------

function BlacklistTab({
  tabSelector,
  page,
  setPage,
}: {
  tabSelector: ReactNode;
  page: number;
  setPage: PageSetter;
}) {
  const [list, setList] = useState<api.AdminDeletedVideo[]>([]);
  const [drives, setDrives] = useState<api.AdminDrive[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [keyword, setKeyword] = useState("");
  const [searchKeyword, setSearchKeyword] = useState("");
  const [total, setTotal] = useState(0);
  const [selectMode, setSelectMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [removeTarget, setRemoveTarget] = useState<api.AdminDeletedVideo | null>(null);
  const [removing, setRemoving] = useState(false);
  const [sourceDeleteStatus, setSourceDeleteStatus] = useState<api.BlacklistSourceDeleteStatus | null>(null);
  const [sourceDeleteOpen, setSourceDeleteOpen] = useState(false);
  const [sourceDeleteTarget, setSourceDeleteTarget] = useState<api.AdminDeletedVideo | null>(null);
  const [batchSourceDeleteOpen, setBatchSourceDeleteOpen] = useState(false);
  const [sourceDeleteStarting, setSourceDeleteStarting] = useState(false);
  const pageSize = useVideosPageSize();
  const previousPageSizeRef = useRef(pageSize);
  const { show } = useToast();

  async function refresh() {
    setLoading(true);
    setLoadError("");
    try {
      const [r, driveList] = await Promise.all([
        api.listBlacklist({ page, size: pageSize, keyword: searchKeyword }),
        api.listDrives(),
      ]);
      setList(r.items ?? []);
      setTotal(r.total ?? 0);
      setDrives(driveList ?? []);
      setSelectedIds(new Set());
    } catch (e) {
      const message = e instanceof Error ? e.message : "加载失败";
      setLoadError(message);
      show(message, "error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, [page, searchKeyword, pageSize]);

  useEffect(() => {
    let active = true;
    void api.getBlacklistSourceDeleteStatus()
      .then((status) => {
        if (active) setSourceDeleteStatus(status);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!sourceDeleteStatus?.running) return;
    let active = true;
    let timer = 0;

    const poll = async () => {
      try {
        const status = await api.getBlacklistSourceDeleteStatus();
        if (!active) return;
        setSourceDeleteStatus(status);
        if (status.running) {
          timer = window.setTimeout(poll, 2000);
          return;
        }
        show(
          status.failed > 0
            ? `源文件删除完成：成功 ${status.deleted}，失败 ${status.failed}`
            : `源文件删除完成：成功 ${status.deleted}`,
          status.failed > 0 ? "info" : "success"
        );
        void refresh();
      } catch {
        if (active) timer = window.setTimeout(poll, 2000);
      }
    };

    timer = window.setTimeout(poll, 1000);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [sourceDeleteStatus?.running]);

  useEffect(() => {
    if (previousPageSizeRef.current === pageSize) return;
    previousPageSizeRef.current = pageSize;
    setPage(1);
  }, [pageSize, setPage]);

  useEffect(() => {
    if (keyword === searchKeyword) return;
    const timer = window.setTimeout(() => {
      setSearchKeyword(keyword);
      setPage(1);
    }, ADMIN_SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [keyword, searchKeyword]);

  const driveNameMap = new Map(drives.map((d) => [d.id, d.name || d.id]));
  driveNameMap.set(LOCAL_UPLOAD_SOURCE_ID, "上传来源");
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  useEffect(() => {
    if (!loading && page > totalPages) setPage(totalPages);
  }, [loading, page, totalPages, setPage]);
  const showPagination = totalPages > 1;
  const placeholderRows = showPagination ? Math.max(0, pageSize - list.length) : 0;
  const sourceDeleteRunning = !!sourceDeleteStatus?.running;
  const hasActiveSearch = searchKeyword.trim().length > 0;
  const hasBlacklistActions = list.length > 0;

  async function confirmRemove() {
    if (!removeTarget) return;
    const target = removeTarget;
    setRemoving(true);
    try {
      await api.removeBlacklist(target.id);
      setRemoveTarget(null);
      show(
        target.restorePolicy === "crawler"
          ? "已取消拉黑，将在下次爬虫任务时生效"
          : "已取消拉黑，将在下次手动或定时扫盘时生效",
        "success"
      );
      if (list.length === 1 && page > 1) {
        setPage((p) => Math.max(1, p - 1));
      } else {
        refresh();
      }
    } catch (e) {
      show(e instanceof Error ? e.message : "操作失败", "error");
    } finally {
      setRemoving(false);
    }
  }

  async function startSourceDelete(
    options: { deleteAllSources?: boolean; ids?: string[] },
    onAccepted: () => void,
    startedMessage: string
  ) {
    setSourceDeleteStarting(true);
    try {
      const result = await api.startBlacklistSourceDelete(options);
      setSourceDeleteStatus(result.status);
      if (!result.accepted) {
        show(result.message || "源文件删除任务已在运行", "info");
        return;
      }
      onAccepted();
      show(startedMessage, "info");
    } catch (e) {
      show(e instanceof Error ? e.message : "启动删除任务失败", "error");
    } finally {
      setSourceDeleteStarting(false);
    }
  }

  async function confirmSourceDeleteAll() {
    await startSourceDelete(
      { deleteAllSources: true },
      () => setSourceDeleteOpen(false),
      "已开始后台顺序删除全部黑名单源文件"
    );
  }

  async function confirmSourceDeleteTarget() {
    if (!sourceDeleteTarget) return;
    const target = sourceDeleteTarget;
    await startSourceDelete(
      { ids: [target.id] },
      () => {
        setSourceDeleteTarget(null);
        setSelectedIds((ids) => {
          const next = new Set(ids);
          next.delete(target.id);
          return next;
        });
      },
      "已开始后台删除该拉黑视频源文件"
    );
  }

  async function confirmBatchSourceDelete() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;
    await startSourceDelete(
      { ids },
      () => {
        setBatchSourceDeleteOpen(false);
        setSelectedIds(new Set());
        setSelectMode(false);
      },
      `已开始后台顺序删除 ${ids.length} 个拉黑视频源文件`
    );
  }

  const toggleSelect = (v: api.AdminDeletedVideo) => {
    if (!canDeleteBlacklistSource(v)) return;
    const next = new Set(selectedIds);
    if (next.has(v.id)) next.delete(v.id);
    else next.add(v.id);
    setSelectedIds(next);
  };

  const toggleSelectMode = () => {
    setSelectMode((active) => !active);
    setSelectedIds(new Set());
  };

  function handleSearchSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSearchKeyword(keyword);
    setPage(1);
  }

  return (
    <div className={`admin-videos-blacklist${selectMode ? " has-bulk-actions" : ""}`}>
      <div className="admin-page__actions admin-videos-filter admin-videos-filter--blacklist">
        <SearchBox keyword={keyword} onChange={setKeyword} onSubmit={handleSearchSubmit} placeholder="搜索文件名" />
        {hasBlacklistActions && (
          <div className="admin-videos-filter__actions admin-blacklist-source-delete">
            {sourceDeleteStatus?.running && (
              <span className="admin-blacklist-source-delete__status">
                正在删除 {sourceDeleteStatus.processed}/{sourceDeleteStatus.total}
              </span>
            )}
            <button
              type="button"
              className="admin-btn admin-videos-filter__batch admin-blacklist-source-delete__button"
              disabled={sourceDeleteStatus?.running || (sourceDeleteStatus?.pending ?? total) <= 0}
              onClick={() => setSourceDeleteOpen(true)}
            >
              {sourceDeleteStatus?.running ? "删除中" : "删除全部"}
            </button>
            <button
              type="button"
              className={`admin-btn admin-videos-filter__batch admin-videos-filter__batch-select${selectMode ? " is-primary" : ""}`}
              onClick={toggleSelectMode}
              aria-pressed={selectMode}
            >
              <span>{selectMode ? "退出选择" : "批量选择"}</span>
            </button>
          </div>
        )}
      </div>
      {tabSelector}

      {!loading && selectMode && (
        <div className="admin-videos-list-toolbar admin-blacklist-bulk-toolbar">
          <div className="admin-videos-bulk-actions">
            <span className="admin-videos-bulk-actions__count">已选择 {selectedIds.size} 项</span>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn"
              onClick={() => setSelectedIds(new Set())}
              disabled={selectedIds.size === 0}
            >
              取消选中
            </button>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn"
              onClick={() => setBatchSourceDeleteOpen(true)}
              disabled={sourceDeleteRunning || selectedIds.size === 0}
            >
              批量删除
            </button>
            <button
              type="button"
              className="admin-btn admin-videos-bulk-actions__btn admin-videos-bulk-actions__mobile-exit"
              onClick={toggleSelectMode}
            >
              退出选择
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <LoadingState />
      ) : loadError ? (
        <ErrorState message={loadError} onRetry={refresh} />
      ) : list.length === 0 ? (
        <AdminEmptyVisual
          variant={hasActiveSearch ? "no-results" : "empty"}
          text={hasActiveSearch ? "未查询到" : "暂无拉黑视频"}
          className="admin-empty-state admin-empty-state--plain"
        />
      ) : (
        <>
          <table className={`admin-table is-selectable admin-blacklist-table${selectMode ? " is-row-select-mode" : ""}`}>
            <tbody>
              {list.map((v) => {
                const sourceDeletable = canDeleteBlacklistSource(v);
                const isSelected = selectedIds.has(v.id);
                const rowSelectable = selectMode && sourceDeletable && !sourceDeleteRunning;

                return (
                <tr
                  key={v.id}
                  className={`${isSelected ? "is-selected" : ""}${selectMode && !rowSelectable ? " is-disabled-select" : ""}`}
                  aria-selected={selectMode ? isSelected : undefined}
                  tabIndex={rowSelectable ? 0 : undefined}
                  onClick={(event) => {
                    if (!rowSelectable || isInteractiveTarget(event.target)) return;
                    toggleSelect(v);
                  }}
                  onKeyDown={(event) => {
                    if (!rowSelectable || isInteractiveTarget(event.target)) return;
                    if (event.key !== "Enter" && event.key !== " ") return;
                    event.preventDefault();
                    toggleSelect(v);
                  }}
                >
                  <td data-label="文件名">
                    <div className="admin-blacklist-filecell">
                      <span className="admin-blacklist-filename" title={v.fileName || undefined}>{v.fileName || <span className="admin-text-faint">（无文件名）</span>}</span>
                      {v.reason === "duplicate" && <span className="admin-blacklist-reason-pill">重复文件</span>}
                      {v.driveId === "local-upload" && (
                        <span className="admin-blacklist-reason-pill">本地上传</span>
                      )}
                    </div>
                  </td>
                  <td data-label="来源" className="admin-mono-cell">
                    {driveNameMap.get(v.driveId) ?? v.driveId}
                  </td>
                  <td className="is-actions" data-label="操作">
                    <div className="admin-blacklist-actions">
                      {v.restorePolicy !== "none" ? (
                        <button
                          type="button"
                          className="admin-btn"
                          onClick={() => setRemoveTarget(v)}
                          title="取消拉黑"
                        >
                          取消拉黑
                        </button>
                      ) : v.reason === "duplicate" ? (
                        v.canonicalVideoId && v.canonicalTitle ? (
                          <Link
                            className="admin-btn"
                            to={`/video/${encodeURIComponent(v.canonicalVideoId)}`}
                            title={v.canonicalTitle}
                          >
                            查看保留视频
                          </Link>
                        ) : null
                      ) : (
                        <span className="admin-blacklist-unavailable">
                          {v.driveId === "local-upload" ? "不可自动恢复" : "不可恢复"}
                        </span>
                      )}
                      {sourceDeletable && (
                        <button
                          type="button"
                          className="admin-btn is-danger admin-blacklist-delete-source-btn"
                          onClick={() => setSourceDeleteTarget(v)}
                          disabled={sourceDeleteRunning}
                          aria-label={`删除 ${v.fileName || v.id}`}
                          title="删除"
                        >
                          <Trash2 size={13} aria-hidden="true" />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
                );
              })}
              {Array.from({ length: placeholderRows }, (_, index) => (
                <tr
                  key={`placeholder-${index}`}
                  className="admin-video-placeholder-row"
                  aria-hidden="true"
                >
                  <td data-label="文件名">
                    <div className="admin-blacklist-filecell">
                      <span className="admin-blacklist-filename">placeholder</span>
                    </div>
                  </td>
                  <td data-label="来源" className="admin-mono-cell">
                    placeholder
                  </td>
                  <td className="is-actions" data-label="操作">
                    <div className="admin-blacklist-actions">
                      <span className="admin-btn">placeholder</span>
                      <span className="admin-btn admin-blacklist-delete-source-btn">placeholder</span>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {showPagination && <Pagination page={page} totalPages={totalPages} onPage={setPage} />}
        </>
      )}

      <ConfirmModal
        open={sourceDeleteOpen}
        title="删除全部黑名单源文件"
        message={`确定删除全部待处理的黑名单源文件吗？当前共 ${sourceDeleteStatus?.pending ?? total} 个。`}
        confirmText="确认"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm admin-modal--source-delete-flat"
        loading={sourceDeleteStarting}
        onCancel={() => {
          if (!sourceDeleteStarting) setSourceDeleteOpen(false);
        }}
        onConfirm={confirmSourceDeleteAll}
      />

      <ConfirmModal
        open={sourceDeleteTarget !== null}
        title="删除源文件"
        message={sourceDeleteTarget ? `确定删除「${sourceDeleteTarget.fileName || sourceDeleteTarget.id}」的源文件吗？` : ""}
        confirmText="确认"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm admin-modal--source-delete-flat"
        loading={sourceDeleteStarting}
        onCancel={() => {
          if (!sourceDeleteStarting) setSourceDeleteTarget(null);
        }}
        onConfirm={confirmSourceDeleteTarget}
      />

      <ConfirmModal
        open={batchSourceDeleteOpen}
        title="批量删除拉黑视频源文件"
        message={`确定删除当前页选中的 ${selectedIds.size} 个拉黑视频源文件吗？`}
        confirmText="确认"
        danger
        centerMessage
        modalClassName="admin-modal--delete-confirm admin-modal--source-delete-flat"
        loading={sourceDeleteStarting}
        onCancel={() => {
          if (!sourceDeleteStarting) setBatchSourceDeleteOpen(false);
        }}
        onConfirm={confirmBatchSourceDelete}
      />

      <ConfirmModal
        open={removeTarget !== null}
        title="取消拉黑"
        message={
          removeTarget
            ? removeTarget.restorePolicy === "crawler"
              ? `确定取消拉黑「${removeTarget.fileName || removeTarget.id}」吗？此操作不会立即运行爬虫，将在下次爬虫任务时生效。`
              : `确定取消拉黑「${removeTarget.fileName || removeTarget.id}」吗？视频将在下次扫盘时恢复`
            : ""
        }
        confirmText="取消拉黑"
        centerMessage
        loading={removing}
        onCancel={() => {
          if (!removing) setRemoveTarget(null);
        }}
        onConfirm={confirmRemove}
      />
    </div>
  );
}

// ---------- 共享小组件 ----------

type VideoSourcePickerKind = "drive" | "crawler" | null;

type VideoSourceMenuPosition = {
  placement: "top" | "bottom";
  left: number;
  top?: number;
  bottom?: number;
  width: number;
  maxHeight: number;
};

function VideoSourcePicker({
  driveId,
  crawlerId,
  drives,
  crawlers,
  onChange,
}: {
  driveId: string;
  crawlerId: string;
  drives: api.AdminDrive[];
  crawlers: api.AdminCrawler[];
  onChange: (kind: VideoSourcePickerKind, id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [menuPosition, setMenuPosition] = useState<VideoSourceMenuPosition | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const listboxId = useId();

  const selectedIsUpload = driveId === LOCAL_UPLOAD_SOURCE_ID;
  const selectedDrive = selectedIsUpload ? undefined : drives.find((drive) => drive.id === driveId);
  const selectedCrawler = crawlers.find((crawler) => crawler.id === crawlerId);
  const selectedKind: VideoSourcePickerKind = crawlerId ? "crawler" : driveId ? "drive" : null;
  const selectedName = selectedIsUpload
    ? "上传来源"
    : selectedCrawler?.name || selectedDrive?.name || crawlerId || driveId || "全部来源";
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const uploadMatchesQuery = ["上传来源", "上传", "本地文件", "视频直链"].some((text) =>
    text.toLocaleLowerCase().includes(normalizedQuery)
  );
  const filteredDrives = drives
    .filter((drive) => drive.id !== LOCAL_UPLOAD_SOURCE_ID)
    .filter((drive) =>
      [drive.name, drive.id, kindLabel[drive.kind]].some((text) =>
        (text || "").toLocaleLowerCase().includes(normalizedQuery)
      )
    );
  const filteredCrawlers = crawlers.filter((crawler) =>
    [crawler.name, crawler.id, "脚本爬虫"].some((text) =>
      text.toLocaleLowerCase().includes(normalizedQuery)
    )
  );
  const hasMatches = uploadMatchesQuery || filteredDrives.length > 0 || filteredCrawlers.length > 0;

  useEffect(() => {
    if (!open) return;
    function closeOnOutsidePointer(event: PointerEvent) {
      const target = event.target;
      if (target instanceof Node && !rootRef.current?.contains(target)) {
        setOpen(false);
        setMenuPosition(null);
      }
    }
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePointer);
  }, [open]);

  useLayoutEffect(() => {
    if (!open) return;

    const updateMenuPosition = () => {
      const trigger = triggerRef.current;
      if (!trigger) return;

      const rect = trigger.getBoundingClientRect();
      const viewportPadding = 12;
      const menuGap = 8;
      const preferredHeight = 356;
      const spaceBelow = Math.max(0, window.innerHeight - rect.bottom - menuGap - viewportPadding);
      const spaceAbove = Math.max(0, rect.top - menuGap - viewportPadding);
      const placement = spaceBelow < 240 && spaceAbove > spaceBelow ? "top" : "bottom";
      const availableHeight = placement === "top" ? spaceAbove : spaceBelow;
      const width = Math.min(rect.width, window.innerWidth - viewportPadding * 2);
      const left = Math.min(
        Math.max(viewportPadding, rect.left),
        Math.max(viewportPadding, window.innerWidth - viewportPadding - width)
      );

      setMenuPosition({
        placement,
        left: Math.round(left),
        top: placement === "bottom" ? Math.round(rect.bottom + menuGap) : undefined,
        bottom: placement === "top" ? Math.round(window.innerHeight - rect.top + menuGap) : undefined,
        width: Math.round(width),
        maxHeight: Math.max(96, Math.min(preferredHeight, Math.floor(availableHeight))),
      });
    };

    updateMenuPosition();
    window.addEventListener("resize", updateMenuPosition);
    window.addEventListener("scroll", updateMenuPosition, true);
    window.visualViewport?.addEventListener("resize", updateMenuPosition);
    window.visualViewport?.addEventListener("scroll", updateMenuPosition);
    const resizeObserver =
      typeof ResizeObserver === "undefined" ? null : new ResizeObserver(updateMenuPosition);
    if (triggerRef.current) resizeObserver?.observe(triggerRef.current);

    return () => {
      window.removeEventListener("resize", updateMenuPosition);
      window.removeEventListener("scroll", updateMenuPosition, true);
      window.visualViewport?.removeEventListener("resize", updateMenuPosition);
      window.visualViewport?.removeEventListener("scroll", updateMenuPosition);
      resizeObserver?.disconnect();
    };
  }, [open]);

  function optionElements(): HTMLButtonElement[] {
    return Array.from(listRef.current?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? []);
  }

  function openPicker(focusTarget?: "first" | "last") {
    setQuery("");
    setMenuPosition(null);
    setOpen(true);
    window.requestAnimationFrame(() => {
      const options = optionElements();
      listRef.current
        ?.querySelector<HTMLElement>('[role="option"][aria-selected="true"]')
        ?.scrollIntoView({ block: "nearest" });
      if (!focusTarget) return;
      (focusTarget === "first" ? options[0] : options[options.length - 1])?.focus();
    });
  }

  function closePicker(restoreFocus = false) {
    setOpen(false);
    setMenuPosition(null);
    if (restoreFocus) {
      window.requestAnimationFrame(() => triggerRef.current?.focus());
    }
  }

  function selectSource(kind: VideoSourcePickerKind, id: string) {
    onChange(kind, id);
    closePicker(true);
  }

  function handleKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape" && open) {
      event.preventDefault();
      event.stopPropagation();
      closePicker(true);
      return;
    }

    if (!open) {
      if (event.target === triggerRef.current && event.key === "ArrowDown") {
        event.preventDefault();
        openPicker("first");
      } else if (event.target === triggerRef.current && event.key === "ArrowUp") {
        event.preventDefault();
        openPicker("last");
      }
      return;
    }

    if (event.key === "Tab") {
      const pickerRoot = event.currentTarget;
      window.requestAnimationFrame(() => {
        const activeElement = document.activeElement;
        if (!(activeElement instanceof Node) || !pickerRoot.contains(activeElement)) {
          setOpen(false);
          setMenuPosition(null);
        }
      });
      return;
    }

    const options = optionElements();
    if (
      event.key === "Enter" &&
      event.target === searchRef.current &&
      normalizedQuery &&
      options.length > 0
    ) {
      event.preventDefault();
      options[0].click();
      return;
    }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key) || options.length === 0) {
      return;
    }

    event.preventDefault();
    const currentIndex = options.indexOf(document.activeElement as HTMLButtonElement);
    let nextIndex = currentIndex;
    if (event.key === "Home") nextIndex = 0;
    if (event.key === "End") nextIndex = options.length - 1;
    if (event.key === "ArrowDown") nextIndex = currentIndex < 0 ? 0 : (currentIndex + 1) % options.length;
    if (event.key === "ArrowUp") nextIndex = currentIndex <= 0 ? options.length - 1 : currentIndex - 1;
    options[nextIndex]?.focus();
  }

  return (
    <div
      ref={rootRef}
      className={`admin-video-source-picker${open ? " is-open" : ""}`}
      onKeyDown={handleKeyDown}
    >
      <button
        ref={triggerRef}
        type="button"
        role="combobox"
        className="admin-video-source-picker__trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listboxId : undefined}
        aria-label={`来源，当前为${selectedName}`}
        onClick={() => (open ? closePicker() : openPicker())}
      >
        <VideoSourcePickerIcon
          drive={selectedDrive}
          crawler={selectedCrawler}
          upload={selectedIsUpload}
        />
        <span className="admin-video-source-picker__selection">
          <strong>{selectedName}</strong>
        </span>
        <ChevronDown
          size={16}
          className={`admin-video-source-picker__chevron${open ? " is-open" : ""}`}
          aria-hidden="true"
        />
      </button>

      {open && (
        <div
          className="admin-video-source-picker__menu"
          data-placement={menuPosition?.placement ?? "bottom"}
          style={{
            left: menuPosition?.left,
            top: menuPosition?.top,
            bottom: menuPosition?.bottom,
            width: menuPosition?.width,
            maxHeight: menuPosition?.maxHeight,
            visibility: menuPosition ? "visible" : "hidden",
          }}
        >
          <div className="admin-video-source-picker__search">
            <Search size={14} aria-hidden="true" />
            <input
              ref={searchRef}
              type="search"
              value={query}
              placeholder="搜索网盘或爬虫"
              aria-label="搜索来源"
              aria-controls={listboxId}
              autoComplete="off"
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <div ref={listRef} id={listboxId} className="admin-video-source-picker__list" role="listbox" aria-label="来源">
            {!normalizedQuery && (
              <button
                type="button"
                role="option"
                tabIndex={-1}
                aria-selected={selectedKind === null}
                className={`admin-video-source-picker__option is-all${selectedKind === null ? " is-selected" : ""}`}
                onClick={() => selectSource(null, "")}
              >
                <VideoSourcePickerIcon />
                <span className="admin-video-source-picker__option-copy">
                  <strong>全部来源</strong>
                </span>
                {selectedKind === null && <Check size={16} aria-hidden="true" />}
              </button>
            )}

            {filteredDrives.length > 0 && (
              <div className="admin-video-source-picker__group" role="group" aria-label="网盘">
                <div className="admin-video-source-picker__group-title" aria-hidden="true">
                  <span>网盘</span>
                </div>
                {filteredDrives.map((drive) => {
                  const selected = selectedKind === "drive" && drive.id === driveId;
                  return (
                    <button
                      key={drive.id}
                      type="button"
                      role="option"
                      tabIndex={-1}
                      aria-selected={selected}
                      className={`admin-video-source-picker__option${selected ? " is-selected" : ""}`}
                      onClick={() => selectSource("drive", drive.id)}
                    >
                      <VideoSourcePickerIcon drive={drive} />
                      <span className="admin-video-source-picker__option-copy">
                        <strong>{drive.name || drive.id}</strong>
                      </span>
                      {selected && <Check size={16} aria-hidden="true" />}
                    </button>
                  );
                })}
              </div>
            )}

            {filteredCrawlers.length > 0 && (
              <div className="admin-video-source-picker__group" role="group" aria-label="爬虫">
                <div className="admin-video-source-picker__group-title" aria-hidden="true">
                  <span>爬虫</span>
                </div>
                {filteredCrawlers.map((crawler) => {
                  const selected = selectedKind === "crawler" && crawler.id === crawlerId;
                  return (
                    <button
                      key={crawler.id}
                      type="button"
                      role="option"
                      tabIndex={-1}
                      aria-selected={selected}
                      className={`admin-video-source-picker__option${selected ? " is-selected" : ""}`}
                      onClick={() => selectSource("crawler", crawler.id)}
                    >
                      <VideoSourcePickerIcon crawler={crawler} />
                      <span className="admin-video-source-picker__option-copy">
                        <strong>{crawler.name || crawler.id}</strong>
                      </span>
                      {selected && <Check size={16} aria-hidden="true" />}
                    </button>
                  );
                })}
              </div>
            )}

            {uploadMatchesQuery && (
              <button
                type="button"
                role="option"
                tabIndex={-1}
                aria-selected={selectedIsUpload}
                className={`admin-video-source-picker__option is-upload${selectedIsUpload ? " is-selected" : ""}`}
                onClick={() => selectSource("drive", LOCAL_UPLOAD_SOURCE_ID)}
              >
                <VideoSourcePickerIcon upload />
                <span className="admin-video-source-picker__option-copy">
                  <strong>上传来源</strong>
                </span>
                {selectedIsUpload && <Check size={16} aria-hidden="true" />}
              </button>
            )}

            {normalizedQuery && !hasMatches && (
              <div className="admin-video-source-picker__empty">没有匹配的来源</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

function VideoSourcePickerIcon({
  drive,
  crawler,
  upload,
}: {
  drive?: api.AdminDrive;
  crawler?: api.AdminCrawler;
  upload?: boolean;
}) {
  if (upload) {
    return (
      <span className="admin-video-source-picker__icon is-upload" aria-hidden="true">
        <Upload size={18} />
      </span>
    );
  }
  if (drive) {
    const iconSrc = driveKindIconPath(drive.kind);
    return (
      <span
        className={`admin-video-source-picker__icon is-drive${iconSrc ? " has-image" : ""}`}
        data-kind={drive.kind}
        aria-hidden="true"
      >
        {iconSrc ? <img src={iconSrc} alt="" /> : driveKindAbbr(drive.kind)}
      </span>
    );
  }
  if (crawler) {
    return (
      <span className="admin-video-source-picker__icon is-crawler" aria-hidden="true">
        <SpiderIcon />
      </span>
    );
  }
  return (
    <span className="admin-video-source-picker__icon is-all" aria-hidden="true">
      全
    </span>
  );
}

function SpiderIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5 4v2l5 5M2.5 9.5 4 11h6M4 19v-2l6-6" />
      <path d="M19 4v2l-5 5M21.5 9.5 20 11h-6M20 19v-2l-6-6" />
      <circle cx="12" cy="15" r="4" />
      <circle cx="12" cy="9" r="2" />
    </svg>
  );
}

function AdvancedVideoFilters({
  value,
  drives,
  crawlers,
  onChange,
  onSubmit,
}: {
  value: VideoAdvancedFilterValues;
  drives: api.AdminDrive[];
  crawlers: api.AdminCrawler[];
  onChange: (value: VideoAdvancedFilterValues) => void;
  onSubmit: (event: React.FormEvent) => void;
}) {
  function updateField(key: keyof VideoAdvancedFilterValues, nextValue: string) {
    onChange({ ...value, [key]: nextValue });
  }

  const today = localDateInputValue(new Date());

  return (
    <form
      id="admin-video-advanced-filters"
      className="admin-video-advanced-filters"
      aria-label="视频高级筛选"
      onSubmit={onSubmit}
    >
      <div className="admin-video-advanced-filters__grid">
        <div className="admin-video-advanced-field admin-video-advanced-field--source">
          <span>来源</span>
          <VideoSourcePicker
            driveId={value.driveId}
            crawlerId={value.crawlerId}
            drives={drives}
            crawlers={crawlers}
            onChange={(kind, id) => {
              onChange({
                ...value,
                driveId: kind === "drive" ? id : "",
                crawlerId: kind === "crawler" ? id : "",
              });
            }}
          />
        </div>

        <fieldset className="admin-video-advanced-range">
          <legend>入库时间</legend>
          <div className="admin-video-advanced-range__inputs is-date-range">
            <label>
              {!value.createdFrom && (
                <span className="admin-video-advanced-range__placeholder" aria-hidden="true">
                  年/月/日
                </span>
              )}
              <input
                type="date"
                className={!value.createdFrom ? "is-empty" : undefined}
                aria-label="入库开始日期"
                value={value.createdFrom}
                max={earlierDateInputValue(value.createdTo, today)}
                onClick={(event) => openNativeDatePicker(event.currentTarget)}
                onChange={(event) => updateField("createdFrom", event.target.value)}
              />
            </label>
            <span className="admin-video-advanced-range__separator">至</span>
            <label>
              {!value.createdTo && (
                <span className="admin-video-advanced-range__placeholder" aria-hidden="true">
                  年/月/日
                </span>
              )}
              <input
                type="date"
                className={!value.createdTo ? "is-empty" : undefined}
                aria-label="入库截止日期"
                value={value.createdTo}
                min={value.createdFrom || undefined}
                max={today}
                onClick={(event) => openNativeDatePicker(event.currentTarget)}
                onChange={(event) => updateField("createdTo", event.target.value)}
              />
            </label>
          </div>
        </fieldset>

        <fieldset className="admin-video-advanced-range">
          <legend>视频时长(分钟)</legend>
          <div className="admin-video-advanced-range__inputs is-duration-range">
            <label>
              <input
                type="number"
                aria-label="视频最短时长（分钟）"
                min={1}
                max={525600}
                step={1}
                inputMode="numeric"
                placeholder="不限"
                value={value.durationMinMinutes}
                onChange={(event) => updateField("durationMinMinutes", event.target.value)}
              />
            </label>
            <span className="admin-video-advanced-range__separator">至</span>
            <label>
              <input
                type="number"
                aria-label="视频最长时长（分钟）"
                min={value.durationMinMinutes || 1}
                max={525600}
                step={1}
                inputMode="numeric"
                placeholder="不限"
                value={value.durationMaxMinutes}
                onChange={(event) => updateField("durationMaxMinutes", event.target.value)}
              />
            </label>
          </div>
        </fieldset>
      </div>
    </form>
  );
}

function SearchBox({
  keyword,
  onChange,
  onSubmit,
  placeholder = "搜索标题 / 作者",
}: {
  keyword: string;
  onChange: (v: string) => void;
  onSubmit: (e: React.FormEvent) => void;
  placeholder?: string;
}) {
  return (
    <form className="admin-videos-filter__search" onSubmit={onSubmit}>
      <Search size={14} className="admin-videos-filter__search-icon" />
      <input
        aria-label={placeholder}
        value={keyword}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
      />
    </form>
  );
}

function Pagination({
  page,
  totalPages,
  onPage,
}: {
  page: number;
  totalPages: number;
  onPage: React.Dispatch<React.SetStateAction<number>>;
}) {
  return (
    <div className="admin-table-pagination">
      <button type="button" className="admin-btn" onClick={() => onPage(() => 1)} disabled={page <= 1}>
        首页
      </button>
      <button type="button" className="admin-btn" onClick={() => onPage((p) => Math.max(1, p - 1))} disabled={page <= 1}>
        上一页
      </button>
      <span className="admin-table-pagination__info">
        第 {page} / {totalPages} 页
      </span>
      <button
        type="button"
        className="admin-btn"
        onClick={() => onPage((p) => Math.min(totalPages, p + 1))}
        disabled={page >= totalPages}
      >
        下一页
      </button>
      <button type="button" className="admin-btn" onClick={() => onPage(() => totalPages)} disabled={page >= totalPages}>
        末页
      </button>
    </div>
  );
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="admin-error-state">
      <strong>加载失败</strong>
      <span>{message}</span>
      <button type="button" className="admin-btn" onClick={onRetry}>
        <RefreshCw size={13} /> 重试
      </button>
    </div>
  );
}

function LoadingState() {
  return <AdminLoading />;
}

function canDeleteBlacklistSource(v: api.AdminDeletedVideo) {
  return !v.sourceDeleted;
}

function isInteractiveTarget(target: EventTarget | null) {
  return (
    target instanceof Element &&
    target.closest("button, a, input, label, select, textarea, [role='button']") !== null
  );
}

function DeleteSourceOption({
  checked,
  disabled,
  onChange,
}: {
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="admin-delete-source-option">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>
        <strong>同时删除视频源文件</strong>
      </span>
    </label>
  );
}

function VideoTitleCell({ video: v }: { video: api.AdminVideo }) {
  return (
    <div className="admin-video-title-cell">
      <div className="admin-video-thumb-wrap" aria-hidden="true">
        {v.thumbnailUrl ? (
          <img className="admin-video-thumb" src={v.thumbnailUrl} alt="" loading="lazy" decoding="async" />
        ) : (
          <div className="admin-video-thumb-placeholder">
            <Image size={14} />
          </div>
        )}
      </div>
      <div className="admin-video-title-body">
        <div className="admin-video-title" title={v.title}>{v.title}</div>
        {fileMeta(v) && <div className="admin-video-filemeta">{fileMeta(v)}</div>}
        {(v.tags ?? []).length > 0 && (
          <div className="admin-pills admin-video-title-tags">
            {(v.tags ?? []).map((t) => (
              <span
                key={t}
                className="admin-pill admin-video-tag-source"
                data-source={v.tagSources?.[t] ?? "unknown"}
                title={tagAssignmentTitle(v, t)}
              >
                <span>{t}</span>
                {v.tagSources?.[t] && (
                  <small>{tagAssignmentSourceLabel(v.tagSources[t])}</small>
                )}
              </span>
            ))}
          </div>
        )}
        <VideoFileMetaPills video={v} />
      </div>
    </div>
  );
}

function PreviewStatus({ s }: { s: string }) {
  if (s === REGEN_PREVIEW_STATUS) return <span className="admin-status is-generating">生成中</span>;
  if (s === "ready") return <span className="admin-status is-ok">就绪</span>;
  if (s === "failed") return <span className="admin-status is-error">失败</span>;
  if (s === "disabled") return <span className="admin-status">已关闭</span>;
  if (s === "skipped") return <span className="admin-status">跳过</span>;
  return <span className="admin-status is-pending">待生成</span>;
}

function VideoFileMetaPills({ video }: { video: api.AdminVideo }) {
  const parts = fileMetaParts(video);
  if (parts.length === 0) return null;

  return (
    <div className="admin-video-filemeta-pills" aria-label="视频文件信息">
      {parts.map((part, index) => (
        <span key={`${part}-${index}`} className="admin-video-filemeta-pill">
          {part}
        </span>
      ))}
    </div>
  );
}

function formatDur(sec: number): string {
  if (!sec) return "—";
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${m.toString().padStart(2, "0")}:${s.toString().padStart(2, "0")}`;
}

function videoUpdatedAtMs(video?: api.AdminVideo): number {
  if (!video?.updatedAt) return 0;
  const value = Date.parse(video.updatedAt);
  return Number.isFinite(value) ? value : 0;
}

function useVideosPageSize() {
  const [pageSize, setPageSize] = useState(() =>
    window.matchMedia(VIDEOS_MOBILE_QUERY).matches ? MOBILE_VIDEOS_PAGE_SIZE : DESKTOP_VIDEOS_PAGE_SIZE
  );

  useEffect(() => {
    const media = window.matchMedia(VIDEOS_MOBILE_QUERY);
    const update = () => {
      setPageSize(media.matches ? MOBILE_VIDEOS_PAGE_SIZE : DESKTOP_VIDEOS_PAGE_SIZE);
    };
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return pageSize;
}

function EditVideoModal({
  video,
  availableTags,
  previewGenerating,
  onRegenPreview,
  onClose,
  onSaved,
}: {
  video: api.AdminVideo;
  availableTags: api.AdminTag[];
  previewGenerating: boolean;
  onRegenPreview: () => Promise<void>;
  onClose: () => void;
  onSaved: () => void;
}) {
  const idPrefix = useId();
  const [selectedTags, setSelectedTags] = useState(video.tags ?? []);
  const [saving, setSaving] = useState(false);
  const [regeningPreview, setRegeningPreview] = useState(false);
  const { show } = useToast();

  async function handleSave() {
    setSaving(true);
    try {
      await api.updateVideo(video.id, {
        tags: selectedTags,
      });
      show("已保存", "success");
      onSaved();
    } catch (e) {
      show(e instanceof Error ? e.message : "保存失败", "error");
    } finally {
      setSaving(false);
    }
  }

  async function handleRegenPreview() {
    setRegeningPreview(true);
    try {
      await onRegenPreview();
    } finally {
      setRegeningPreview(false);
    }
  }

  const previewBusy = previewGenerating || regeningPreview;

  return (
    <Modal
      open
      title="编辑视频"
      ariaLabel="编辑视频"
      className="admin-modal--video-edit"
      onClose={onClose}
      footer={
        <>
          <Link
            className="admin-btn admin-video-edit-view-link"
            to={`/video/${encodeURIComponent(video.id)}`}
            target="_blank"
            rel="noreferrer"
          >
            查看视频播放页
          </Link>
          <div className="admin-video-edit-footer-actions">
            <button type="button" className="admin-btn" onClick={onClose}>
              取消
            </button>
            <button type="button" className="admin-btn is-primary" onClick={handleSave} disabled={saving}>
              {saving ? "保存中..." : "保存"}
            </button>
          </div>
        </>
      }
    >
      <div className="admin-form admin-video-edit-form">
        <section className="admin-video-edit-section">
          <h3>基本信息</h3>
          <div className="admin-video-edit-basics">
            <div className="admin-form__row">
              <label htmlFor={`${idPrefix}-video-title`}>标题</label>
              <input id={`${idPrefix}-video-title`} value={video.title} readOnly aria-readonly="true" />
            </div>
            <div className="admin-form__row">
              <label htmlFor={`${idPrefix}-video-author`}>作者</label>
              <input id={`${idPrefix}-video-author`} value={video.author ?? ""} readOnly aria-readonly="true" />
            </div>
          </div>
        </section>

        <section className="admin-video-edit-section">
          <h3>标签</h3>
          <div className="admin-tag-picker admin-video-tag-picker">
            {availableTags.map((tag) => (
              <label key={tag.id} className="admin-check admin-video-tag-option">
                <input
                  type="checkbox"
                  checked={selectedTags.includes(tag.label)}
                  onChange={() => setSelectedTags(toggleTag(selectedTags, tag.label))}
                />
                <span className="admin-video-tag-option__label" title={tag.label}>{tag.label}</span>
              </label>
            ))}
          </div>
        </section>

        <section className="admin-video-edit-section">
          <h3>视频信息</h3>
          <dl className="admin-video-edit-meta">
            <div className="admin-video-edit-meta__item">
              <dt>来源盘</dt>
              <dd>{video.driveId}</dd>
            </div>
            <div className="admin-video-edit-meta__item">
              <dt>文件信息</dt>
              <dd>{fileMeta(video) || "—"}</dd>
            </div>
            <div className="admin-video-edit-meta__item is-preview">
              <dt>预览视频</dt>
              <dd className="admin-video-preview-actions">
                <PreviewStatus s={previewGenerating ? REGEN_PREVIEW_STATUS : video.previewStatus} />
                <button
                  type="button"
                  className="admin-btn admin-video-preview-button"
                  onClick={handleRegenPreview}
                  disabled={saving || previewBusy}
                >
                  {previewBusy ? "生成中..." : "重新生成预览"}
                </button>
              </dd>
            </div>
          </dl>
        </section>
      </div>
    </Modal>
  );
}

function tagAssignmentSourceLabel(source: string): string {
  if (source === "manual") return "人工";
  if (source === "auto") return "自动";
  if (source === "series") return "系列";
  if (source === "propagated") return "传播";
  if (source === "crawler") return "爬虫";
  if (source === "legacy") return "自动生成";
  return source || "未知";
}

function tagAssignmentTitle(video: api.AdminVideo, label: string): string {
  const source = video.tagSources?.[label];
  const evidence = video.tagEvidence?.[label];
  return [source ? `来源：${tagAssignmentSourceLabel(source)}` : "", evidence ? `依据：${evidence}` : ""]
    .filter(Boolean)
    .join("；");
}

function fileMeta(v: api.AdminVideo): string {
  return fileMetaParts(v).join(" · ");
}

function fileMetaParts(v: api.AdminVideo): string[] {
  return [normalizeExt(v.ext), v.quality, v.size > 0 ? formatBytes(v.size) : ""].filter(Boolean);
}

function normalizeExt(ext: string): string {
  const value = (ext ?? "").replace(/^\./, "").trim();
  return value ? value.toUpperCase() : "";
}

function countVideoAdvancedFilters(value: VideoAdvancedFilterValues): number {
  let count = 0;
  if (value.driveId || value.crawlerId) count++;
  if (value.createdFrom || value.createdTo) count++;
  if (value.durationMinMinutes || value.durationMaxMinutes) count++;
  return count;
}

function dateRangeIsReversed(from: string, to: string): boolean {
  return !!from && !!to && from > to;
}

function dateIsAfter(value: string, maximum: string): boolean {
  return !!value && value > maximum;
}

function earlierDateInputValue(value: string, maximum: string): string {
  return value && value < maximum ? value : maximum;
}

function localDateInputValue(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function numberRangeIsReversed(min: string, max: string): boolean {
  return !!min && !!max && Number(min) > Number(max);
}

function openNativeDatePicker(input: HTMLInputElement) {
  if (typeof input.showPicker !== "function") return;
  try {
    input.showPicker();
  } catch {
    // Some browsers expose showPicker but restrict when it may be called.
  }
}

function toggleTag(tags: string[], label: string): string[] {
  return tags.includes(label) ? tags.filter((tag) => tag !== label) : [...tags, label];
}
