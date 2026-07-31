import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Clock, HardDrive, RefreshCw, ThumbsUp, Eye } from "lucide-react";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { ConfirmModal } from "./ConfirmModal";
import { AdminEmptyVisual } from "./AdminEmptyVisual";
import { AdminLoading } from "./AdminLoading";
import { formatBytes } from "./storageFormat";

// 疑似重复复核：夜间内容级查重把拿不准的视频对（SSIM 0.80~0.92）排进队列，
// 这里并排展示两个版本，人工裁决"保留哪个"或"不是重复"。
// 保留一方 = 另一方按重复墓碑删除（与夜间自动去重同一条删除路径）。

type Tab = "pending" | "merged" | "dismissed";

const TAB_LABELS: Record<Tab, string> = {
  pending: "待复核",
  merged: "已去重",
  dismissed: "已忽略",
};

function formatDuration(totalSeconds: number): string {
  if (!Number.isFinite(totalSeconds) || totalSeconds <= 0) return "0:00";
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = Math.floor(totalSeconds % 60);
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function formatTime(ts: number) {
  return new Date(ts).toLocaleString("zh-CN");
}

type MergeConfirm = {
  pair: api.DuplicateReviewPair;
  action: "keep-left" | "keep-right";
  keep: api.DuplicateReviewVideo;
  remove: api.DuplicateReviewVideo;
};

export function DuplicateReviewsPage() {
  const [tab, setTab] = useState<Tab>("pending");
  const [items, setItems] = useState<api.DuplicateReviewPair[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [resolvingId, setResolvingId] = useState<number | null>(null);
  const [mergeConfirm, setMergeConfirm] = useState<MergeConfirm | null>(null);
  const { show } = useToast();

  const pageSize = 20;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const res = await api.listDuplicateReviews(tab, page);
      setItems(res.items ?? []);
      setTotal(res.total ?? 0);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }, [tab, page]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  function switchTab(next: Tab) {
    setTab(next);
    setPage(1);
  }

  async function resolve(pair: api.DuplicateReviewPair, action: "keep-left" | "keep-right" | "dismiss") {
    setResolvingId(pair.id);
    try {
      await api.resolveDuplicateReview(pair.id, action);
      show(action === "dismiss" ? "已标记为非重复" : "已保留所选版本，重复版本已拉黑", "success");
      setMergeConfirm(null);
      await refresh();
    } catch (e) {
      show(e instanceof Error ? e.message : "操作失败", "error");
    } finally {
      setResolvingId(null);
    }
  }

  function askMerge(pair: api.DuplicateReviewPair, action: "keep-left" | "keep-right") {
    const keep = action === "keep-left" ? pair.left : pair.right;
    const remove = action === "keep-left" ? pair.right : pair.left;
    if (!keep || !remove) return;
    setMergeConfirm({ pair, action, keep, remove });
  }

  return (
    <div className="admin-page admin-duperev-page">
      <div className="admin-users-toolbar admin-duperev-toolbar">
        <div className="admin-tags-filter-tabs" role="tablist" aria-label="复核状态">
          {(Object.keys(TAB_LABELS) as Tab[]).map((key) => (
            <button
              key={key}
              type="button"
              role="tab"
              aria-selected={tab === key}
              className={`admin-tags-filter-tab ${tab === key ? "is-active" : ""}`}
              onClick={() => switchTab(key)}
            >
              <span className="admin-tags-filter-tab__text">{TAB_LABELS[key]}</span>
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <AdminLoading />
      ) : error ? (
        <div className="admin-error-state">
          <strong>加载失败</strong>
          <span>{error}</span>
          <button type="button" className="admin-btn" onClick={() => refresh()}>
            <RefreshCw size={13} /> 重试
          </button>
        </div>
      ) : items.length === 0 ? (
        <AdminEmptyVisual
          variant="empty"
          text={
            tab === "pending"
              ? "暂无待复核的疑似重复，夜间维护发现拿不准的视频对时会自动排进来"
              : "暂无记录"
          }
          className="admin-empty-state admin-empty-state--plain"
        />
      ) : (
        <div className="admin-duperev-list">
          {items.map((pair) => (
            <div key={pair.id} className="admin-duperev-card">
              <div className="admin-duperev-card__meta">
                {tab === "pending" && (
                  <span className="admin-status is-pending">
                    相似度中位 {(pair.medianSsim * 100).toFixed(1)}%
                  </span>
                )}
                <span className="admin-duperev-card__meta-item">
                  最低 {(pair.minSsim * 100).toFixed(1)}% · {pair.comparisons} 帧对比
                </span>
                <span className="admin-duperev-card__meta-item">
                  <Clock size={12} /> {formatTime(pair.updatedAt)}
                </span>
              </div>
              <div className="admin-duperev-card__videos">
                {[
                  { video: pair.left, action: "keep-left" as const },
                  { video: pair.right, action: "keep-right" as const },
                ].map(({ video, action }, idx) => (
                  <div key={idx} className="admin-duperev-video">
                    {video ? (
                      <>
                        <Link
                          className="admin-duperev-video__thumb"
                          to={`/video/${encodeURIComponent(video.id)}`}
                          target="_blank"
                          title="在新窗口打开视频"
                        >
                          {video.thumbnailUrl ? (
                            <img src={video.thumbnailUrl} alt="" loading="lazy" decoding="async" />
                          ) : (
                            <span className="admin-duperev-video__thumb-empty">无封面</span>
                          )}
                          <span className="admin-duperev-video__duration">
                            {formatDuration(video.durationSeconds)}
                          </span>
                        </Link>
                        <div className="admin-duperev-video__info">
                          <div className="admin-duperev-video__title" title={video.title}>
                            {video.title}
                          </div>
                          <div className="admin-duperev-video__facts">
                            <span><HardDrive size={12} /> {video.driveId}</span>
                            <span>{formatBytes(video.size)}</span>
                            <span><Eye size={12} /> {video.views}</span>
                            <span><ThumbsUp size={12} /> {video.likes}</span>
                          </div>
                          <div className="admin-duperev-video__facts admin-duperev-video__facts--dim">
                            <span>入库 {formatTime(video.createdAt)}</span>
                          </div>
                          {tab === "pending" && (
                            <button
                              type="button"
                              className="admin-btn admin-btn--small admin-duperev-video__keep"
                              disabled={resolvingId === pair.id}
                              onClick={() => askMerge(pair, action)}
                            >
                              保留此版本
                            </button>
                          )}
                        </div>
                      </>
                    ) : (
                      <div className="admin-duperev-video__missing">视频已拉黑</div>
                    )}
                  </div>
                ))}
              </div>
              {tab === "pending" && (
                <div className="admin-duperev-card__actions">
                  <button
                    type="button"
                    className="admin-btn admin-btn--small"
                    disabled={resolvingId === pair.id}
                    onClick={() => resolve(pair, "dismiss")}
                  >
                    不是重复并忽略
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {!loading && !error && totalPages > 1 && (
        <div className="admin-table-pagination">
          <button type="button" className="admin-btn" onClick={() => setPage(1)} disabled={page <= 1}>
            首页
          </button>
          <button
            type="button"
            className="admin-btn"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
          >
            上一页
          </button>
          <span className="admin-table-pagination__info">
            第 {page} / {totalPages} 页（共 {total} 对）
          </span>
          <button
            type="button"
            className="admin-btn"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
          >
            下一页
          </button>
          <button
            type="button"
            className="admin-btn"
            onClick={() => setPage(totalPages)}
            disabled={page >= totalPages}
          >
            末页
          </button>
        </div>
      )}

      <ConfirmModal
        open={mergeConfirm !== null}
        danger
        hideIcon
        title="视频去重"
        message={mergeConfirm ? `保留「${mergeConfirm.keep.title}」，拉黑「${mergeConfirm.remove.title}」？` : ""}
        loading={mergeConfirm !== null && resolvingId === mergeConfirm.pair.id}
        onCancel={() => setMergeConfirm(null)}
        onConfirm={() => {
          if (mergeConfirm) {
            resolve(mergeConfirm.pair, mergeConfirm.action);
          }
        }}
      />
    </div>
  );
}
