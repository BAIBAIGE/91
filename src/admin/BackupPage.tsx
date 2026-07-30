import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from "react";
import { useNavigate } from "react-router-dom";
import {
  Archive,
  CircleAlert,
  Download,
  HardDriveDownload,
  Loader2,
  Pause,
  Play,
  RefreshCw,
  RotateCcw,
  ShieldAlert,
  Trash2,
  UploadCloud,
  X,
} from "lucide-react";
import * as api from "./api";
import { ConfirmModal } from "./ConfirmModal";
import { Modal } from "./Modal";
import { PasswordInput } from "./PasswordInput";
import { useToast } from "./ToastContext";

const RESUME_KEY = "video-site-91-backup-upload-v1";

type ResumeState = {
  id: string;
  fileName: string;
  size: number;
  lastModified: number;
};

function formatBytes(value: number | undefined) {
  if (!value || value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 100 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`;
}

function formatTime(value: string | undefined) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN");
}

function taskActive(task: api.BackupTask | undefined) {
  return task?.state === "queued" || task?.state === "running" || task?.state === "canceling";
}

function taskPhase(phase: string | undefined) {
  switch (phase) {
    case "estimating":
      return "统计数据";
    case "snapshotting":
      return "建立一致性快照";
    case "hashing":
      return "计算文件校验值";
    case "compressing":
      return "写入备份包";
    case "verifying":
      return "完整校验";
    case "canceling":
      return "正在取消";
    case "completed":
      return "已完成";
    case "canceled":
      return "已取消";
    case "failed":
      return "失败";
    default:
      return "准备中";
  }
}

function readResumeState(): ResumeState | null {
  try {
    const parsed = JSON.parse(localStorage.getItem(RESUME_KEY) ?? "null") as ResumeState | null;
    if (
      parsed &&
      typeof parsed.id === "string" &&
      typeof parsed.fileName === "string" &&
      typeof parsed.size === "number"
    ) {
      return parsed;
    }
  } catch {
    // Ignore a damaged local resume hint.
  }
  return null;
}

export function BackupPage() {
  const navigate = useNavigate();
  const { show } = useToast();
  const [data, setData] = useState<api.BackupList | null>(null);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<api.BackupRecord | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<api.BackupRecord | null>(null);
  const [restorePassword, setRestorePassword] = useState("");
  const [restoreText, setRestoreText] = useState("");
  const [restoreSubmitting, setRestoreSubmitting] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [restartManaged, setRestartManaged] = useState(true);
  const [restoreReport, setRestoreReport] = useState<api.RestoreReport | null>(null);

  const [file, setFile] = useState<File | null>(null);
  const [upload, setUpload] = useState<api.BackupUploadSession | null>(null);
  const [uploading, setUploading] = useState(false);
  const [finalizing, setFinalizing] = useState(false);
  const [resumeHint, setResumeHint] = useState<ResumeState | null>(() => readResumeState());
  const uploadAbort = useRef<AbortController | null>(null);
  const pauseRequested = useRef(false);

  const refresh = async (silent = false) => {
    try {
      const next = await api.listBackups();
      setData(next);
    } catch (error) {
      if (!silent) show(error instanceof Error ? error.message : "加载备份列表失败", "error");
    } finally {
      if (!silent) setLoading(false);
    }
  };

  useEffect(() => {
    document.title = "备份恢复";
    void refresh();
    const timer = window.setInterval(() => void refresh(true), 2000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!resumeHint) return;
    let active = true;
    api
      .getBackupUpload(resumeHint.id)
      .then((session) => {
        if (active) setUpload(session);
      })
      .catch(() => {
        if (!active) return;
        localStorage.removeItem(RESUME_KEY);
        setResumeHint(null);
      });
    return () => {
      active = false;
    };
  }, [resumeHint?.id]);

  useEffect(() => {
    if (!restoring) return;
    let active = true;
    const poll = async () => {
      try {
        const state = await api.me();
        if (active && !state.authenticated) {
          navigate("/login", { replace: true });
          return;
        }
        if (active && state.authenticated) {
          const backupState = await api.listBackups();
          if (!active) return;
          setData(backupState);
          // A successful restore clears this session. If the old session is
          // still valid and the marker is gone, startup rejected the restored
          // data and switched back to the previous installation.
          if (!backupState.pendingRestore) {
            setRestoring(false);
            setRestoreReport(null);
            show("恢复未能启动，旧数据已自动回滚", "error");
            return;
          }
        }
      } catch (error) {
        if (active && error instanceof api.UnauthorizedError) {
          navigate("/login", { replace: true });
          return;
        }
      }
      if (active) window.setTimeout(poll, 1200);
    };
    const timer = window.setTimeout(poll, 1000);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [restoring, navigate]);

  const current = data?.current;
  const progress = useMemo(() => {
    if (!current?.totalBytes) return 0;
    return Math.min(100, Math.max(0, (current.processedBytes / current.totalBytes) * 100));
  }, [current?.processedBytes, current?.totalBytes]);

  async function handleCreate() {
    setCreating(true);
    try {
      await api.createBackup();
      show("备份任务已开始", "success");
      await refresh(true);
    } catch (error) {
      show(error instanceof Error ? error.message : "创建备份失败", "error");
    } finally {
      setCreating(false);
    }
  }

  async function handleCancelBackup() {
    try {
      await api.cancelBackup();
      show("正在取消备份任务", "info");
      await refresh(true);
    } catch (error) {
      show(error instanceof Error ? error.message : "取消失败", "error");
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await api.deleteBackup(deleteTarget.id);
      show("备份已删除", "success");
      setDeleteTarget(null);
      await refresh(true);
    } catch (error) {
      show(error instanceof Error ? error.message : "删除备份失败", "error");
    } finally {
      setDeleting(false);
    }
  }

  function chooseFile(event: ChangeEvent<HTMLInputElement>) {
    const next = event.target.files?.[0] ?? null;
    setFile(next);
    if (!next) return;
    const hint = readResumeState();
    if (hint && (hint.fileName !== next.name || hint.size !== next.size)) {
      setUpload(null);
    }
  }

  async function ensureUploadSession(selected: File) {
    const hint = readResumeState();
    if (
      hint &&
      hint.fileName === selected.name &&
      hint.size === selected.size &&
      hint.lastModified === selected.lastModified
    ) {
      try {
        const existing = await api.getBackupUpload(hint.id);
        setUpload(existing);
        return existing;
      } catch {
        localStorage.removeItem(RESUME_KEY);
        setResumeHint(null);
      }
    }
    const created = await api.beginBackupUpload({
      fileName: selected.name,
      size: selected.size,
    });
    const saved: ResumeState = {
      id: created.id,
      fileName: selected.name,
      size: selected.size,
      lastModified: selected.lastModified,
    };
    localStorage.setItem(RESUME_KEY, JSON.stringify(saved));
    setResumeHint(saved);
    setUpload(created);
    return created;
  }

  async function handleUpload() {
    if (!file || uploading || finalizing) return;
    setUploading(true);
    pauseRequested.current = false;
    try {
      let session = await ensureUploadSession(file);
      const received = new Set(session.received.map((chunk) => chunk.index));
      for (let index = 0; index < session.totalChunks; index += 1) {
        if (received.has(index)) continue;
        if (pauseRequested.current) return;
        const start = index * session.chunkSize;
        const end = Math.min(file.size, start + session.chunkSize);
        const blob = file.slice(start, end);
        const hash = await sha256Blob(blob);
        let lastError: unknown;
        for (let attempt = 0; attempt < 3; attempt += 1) {
          if (pauseRequested.current) return;
          const controller = new AbortController();
          uploadAbort.current = controller;
          try {
            session = await api.putBackupUploadChunk(
              session.id,
              index,
              blob,
              hash,
              controller.signal
            );
            setUpload(session);
            lastError = undefined;
            break;
          } catch (error) {
            lastError = error;
            if (pauseRequested.current || controller.signal.aborted) return;
            if (attempt < 2) await delay(500 * (attempt + 1));
          }
        }
        if (lastError) throw lastError;
      }
      setFinalizing(true);
      const completed = await api.finalizeBackupUpload(session.id);
      localStorage.removeItem(RESUME_KEY);
      setResumeHint(null);
      setUpload(null);
      setFile(null);
      show(`迁移备份 ${completed.name} 已完成校验`, "success");
      await refresh(true);
    } catch (error) {
      show(error instanceof Error ? error.message : "迁移上传失败，可稍后重试", "error");
    } finally {
      uploadAbort.current = null;
      setUploading(false);
      setFinalizing(false);
    }
  }

  function handlePause() {
    pauseRequested.current = true;
    uploadAbort.current?.abort();
    setUploading(false);
    show("上传已暂停，已完成分片会保留 24 小时", "info");
  }

  async function handleCancelUpload() {
    if (!upload) return;
    pauseRequested.current = true;
    uploadAbort.current?.abort();
    try {
      await api.cancelBackupUpload(upload.id);
      localStorage.removeItem(RESUME_KEY);
      setResumeHint(null);
      setUpload(null);
      setFile(null);
      show("迁移上传已取消并清理", "success");
    } catch (error) {
      show(error instanceof Error ? error.message : "取消上传失败", "error");
    }
  }

  async function handleRestore() {
    if (!restoreTarget || restoreSubmitting || restoring) return;
    setRestoreSubmitting(true);
    try {
      const result = await api.restoreBackup(restoreTarget.id, {
        password: restorePassword,
        confirmation: restoreText,
      });
      setRestartManaged(result.restartManaged);
      setRestoreReport(result.report);
      setRestoreTarget(null);
      setRestorePassword("");
      setRestoreText("");
      setRestoring(true);
      show("恢复已通过校验，服务正在切换数据并重启", "success");
    } catch (error) {
      show(error instanceof Error ? error.message : "恢复失败", "error");
    } finally {
      setRestoreSubmitting(false);
    }
  }

  function closeRestore() {
    if (restoreSubmitting || restoring) return;
    setRestoreTarget(null);
    setRestorePassword("");
    setRestoreText("");
  }

  if (loading && !data) {
    return (
      <div className="admin-page backup-page">
        <div className="admin-loading-state admin-page-loading">
          <RefreshCw size={20} className="admin-spin" />
          <span>加载中...</span>
        </div>
      </div>
    );
  }

  const estimate = data?.estimate;
  const receivedBytes =
    upload?.received.reduce((sum, chunk) => sum + chunk.size, 0) ?? 0;
  const uploadPercent = upload?.size ? Math.min(100, (receivedBytes / upload.size) * 100) : 0;

  return (
    <div className="admin-page backup-page">
      <div className="admin-page__header backup-page__header">
        <div>
          <h1 className="admin-page__title">备份恢复</h1>
          <p className="admin-page__subtitle">
            完整保存数据库、配置、上传视频、封面、预览、帧签名与爬虫数据。
          </p>
        </div>
        <button
          type="button"
          className="admin-btn is-primary"
          onClick={handleCreate}
          disabled={creating || taskActive(current) || data?.pendingRestore}
        >
          {creating ? <Loader2 size={15} className="admin-spin" /> : <Archive size={15} />}
          创建备份
        </button>
      </div>

      <div className="backup-security-notice" role="note">
        <ShieldAlert size={19} />
        <div>
          <strong>备份包不加密</strong>
          <span>
            其中包含网盘令牌、账号数据和媒体文件。请只通过 HTTPS 传输，并将下载文件保存在可信位置。
          </span>
        </div>
      </div>

      <section className="backup-overview" aria-label="备份空间概览">
        <div className="backup-stat">
          <span>预计数据量</span>
          <strong>{formatBytes(estimate?.totalBytes)}</strong>
          <small>{estimate?.fileCount ?? 0} 个文件</small>
        </div>
        <div className="backup-stat">
          <span>服务器可用空间</span>
          <strong>{formatBytes(estimate?.availableBytes)}</strong>
          <small>安全创建需要 {formatBytes(estimate?.requiredBytes)}</small>
        </div>
        <div className="backup-stat">
          <span>保留策略</span>
          <strong>永久保留</strong>
          <small>仅管理员手动删除</small>
        </div>
      </section>

      {current && (
        <section className={`backup-task ${current.state === "failed" ? "is-error" : ""}`}>
          <div className="backup-task__head">
            <div>
              <span className="backup-eyebrow">当前任务</span>
              <strong>{taskPhase(current.phase)}</strong>
            </div>
            {taskActive(current) && current.cancellable && (
              <button type="button" className="admin-btn is-stop" onClick={handleCancelBackup}>
                <X size={14} />
                取消
              </button>
            )}
          </div>
          <div
            className="backup-progress"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(progress)}
          >
            <span style={{ width: `${progress}%` }} />
          </div>
          <div className="backup-task__meta">
            <span>{progress.toFixed(1)}%</span>
            <span>
              {current.processedFiles}/{current.fileCount} 个文件
            </span>
            <span>
              {formatBytes(current.processedBytes)} / {formatBytes(current.totalBytes)}
            </span>
            <span>{formatBytes(current.bytesPerSecond)}/s</span>
          </div>
          {current.error && <p className="backup-task__error">{current.error}</p>}
        </section>
      )}

      <section className="admin-card backup-upload-card">
        <div className="backup-section-heading">
          <div>
            <span className="backup-eyebrow">服务器迁移</span>
            <h2>分片上传备份包</h2>
          </div>
          <UploadCloud size={21} />
        </div>
        <p>
          使用 16 MiB 分片，支持暂停、自动重试、乱序补传和刷新页面后续传。未完成分片保留 24 小时。
        </p>
        {resumeHint && !file && (
          <div className="backup-resume-hint">
            <RotateCcw size={16} />
            检测到未完成上传：{resumeHint.fileName}。请重新选择同一个本地文件继续。
          </div>
        )}
        <div className="backup-upload-controls">
          <label className="backup-file-picker">
            <HardDriveDownload size={16} />
            <span>{file ? file.name : "选择 ZIP 备份包"}</span>
            <input
              type="file"
              accept=".zip,application/zip"
              onChange={chooseFile}
              disabled={uploading || finalizing}
            />
          </label>
          {finalizing ? (
            <button type="button" className="admin-btn" disabled>
              <Loader2 size={14} className="admin-spin" />
              正在校验
            </button>
          ) : !uploading ? (
            <button
              type="button"
              className="admin-btn is-primary"
              onClick={handleUpload}
              disabled={!file || finalizing}
            >
              <Play size={14} />
              {upload?.received.length ? "继续上传" : "开始上传"}
            </button>
          ) : (
            <button type="button" className="admin-btn" onClick={handlePause}>
              <Pause size={14} />
              暂停
            </button>
          )}
          {upload && (
            <button
              type="button"
              className="admin-btn is-danger"
              onClick={handleCancelUpload}
              disabled={finalizing}
            >
              <Trash2 size={14} />
              取消上传
            </button>
          )}
        </div>
        {upload && (
          <div className="backup-upload-progress">
            <div className="backup-progress">
              <span style={{ width: `${uploadPercent}%` }} />
            </div>
            <div>
              <span>
                {upload.received.length}/{upload.totalChunks} 个分片
              </span>
              <span>
                {formatBytes(receivedBytes)} / {formatBytes(upload.size)}
              </span>
              <span>{finalizing ? "正在合并并完整校验…" : uploading ? "上传中" : "已暂停"}</span>
            </div>
          </div>
        )}
      </section>

      <section className="backup-list-section">
        <div className="backup-section-heading">
          <div>
            <span className="backup-eyebrow">服务器备份</span>
            <h2>已完成备份</h2>
          </div>
          <button type="button" className="admin-btn" onClick={() => refresh()} aria-label="刷新备份列表">
            <RefreshCw size={15} />
            刷新
          </button>
        </div>
        {data?.backups.length ? (
          <div className="backup-list">
            {data.backups.map((record) => (
              <article className="backup-record" key={record.id}>
                <div className="backup-record__icon">
                  <Archive size={21} />
                </div>
                <div className="backup-record__body">
                  <div className="backup-record__name">{record.name}</div>
                  <div className="backup-record__meta">
                    <span>{formatBytes(record.size)}</span>
                    <span>{record.fileCount ?? 0} 个文件</span>
                    <span>{formatTime(record.createdAt)}</span>
                    <span>来源版本 {record.appVersion || "unknown"}</span>
                    {record.imported && <span className="backup-badge">迁移上传</span>}
                    <span className={`backup-verify is-${record.verificationStatus}`}>
                      {record.verificationStatus === "verified"
                        ? "已完整校验"
                        : record.verificationStatus === "invalid"
                          ? "校验失败"
                          : "待校验"}
                    </span>
                  </div>
                  {record.verificationError && (
                    <div className="backup-record__error">{record.verificationError}</div>
                  )}
                </div>
                <div className="backup-record__actions">
                  <a className="admin-btn" href={api.backupDownloadURL(record.id)}>
                    <Download size={14} />
                    下载
                  </a>
                  <button
                    type="button"
                    className="admin-btn"
                    onClick={() => {
                      setRestoreReport(null);
                      setRestoreTarget(record);
                    }}
                    disabled={record.verificationStatus === "invalid" || data.pendingRestore}
                  >
                    <RotateCcw size={14} />
                    恢复
                  </button>
                  <button
                    type="button"
                    className="admin-btn is-danger"
                    onClick={() => setDeleteTarget(record)}
                    disabled={data.pendingRestore}
                  >
                    <Trash2 size={14} />
                    删除
                  </button>
                </div>
              </article>
            ))}
          </div>
        ) : (
          <div className="backup-empty">
            <Archive size={28} />
            <span>还没有备份</span>
          </div>
        )}
      </section>

      <ConfirmModal
        open={deleteTarget !== null}
        title="删除备份"
        message={`确定要永久删除「${deleteTarget?.name ?? ""}」吗？`}
        danger
        loading={deleting}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />

      <Modal
        open={restoreTarget !== null}
        title="确认完整恢复"
        className="admin-modal--backup-restore"
        onClose={closeRestore}
        footer={
          <>
            <button
              type="button"
              className="admin-btn"
              disabled={restoreSubmitting}
              onClick={closeRestore}
            >
              取消
            </button>
            <button
              type="button"
              className="admin-btn is-danger"
              disabled={!restorePassword || restoreText !== "确认恢复" || restoreSubmitting}
              onClick={handleRestore}
            >
              {restoreSubmitting ? <Loader2 size={14} className="admin-spin" /> : <RotateCcw size={14} />}
              {restoreSubmitting ? "校验并暂存中…" : "确认恢复"}
            </button>
          </>
        }
      >
        {restoreTarget && (
          <div className="backup-restore-form">
            <div className="backup-restore-warning">
              <CircleAlert size={18} />
              <span>
                服务会停止后台任务、切换全部持久数据并重启。现有会话、一次性分享和未完成远程上传将被清空。
              </span>
            </div>
            <dl className="backup-restore-summary">
              <div>
                <dt>来源版本</dt>
                <dd>{restoreTarget.appVersion || "unknown"}</dd>
              </div>
              <div>
                <dt>创建时间</dt>
                <dd>{formatTime(restoreTarget.createdAt)}</dd>
              </div>
              <div>
                <dt>校验状态</dt>
                <dd>{restoreTarget.verificationStatus === "verified" ? "已完整校验" : "恢复前将重新完整校验"}</dd>
              </div>
              <div>
                <dt>包含数据</dt>
                <dd>{restoreTarget.included?.join("、") || "全部持久数据"}</dd>
              </div>
              <div>
                <dt>路径处理</dt>
                <dd>数据库内的预览、爬虫与删除载荷路径会改写到本机数据目录</dd>
              </div>
              <div>
                <dt>本地存储</dt>
                <dd>目标路径不存在的本地盘会标记为未连接，不阻止恢复</dd>
              </div>
            </dl>
            <label className="backup-field">
              <span>当前管理员密码</span>
              <PasswordInput
                className="admin-input"
                value={restorePassword}
                onChange={(event) => setRestorePassword(event.target.value)}
                autoComplete="current-password"
              />
            </label>
            <label className="backup-field">
              <span>输入“确认恢复”</span>
              <input
                className="admin-input"
                value={restoreText}
                onChange={(event) => setRestoreText(event.target.value)}
                autoComplete="off"
              />
            </label>
          </div>
        )}
      </Modal>

      {restoring && !restoreTarget && (
        <div className="backup-restarting" role="status">
          <Loader2 size={28} className="admin-spin" />
          <strong>正在应用恢复并重启服务</strong>
          <span>
            {restartManaged
              ? "服务恢复后会自动返回登录页，所有用户需要重新登录。"
              : "当前运行方式没有进程守护，请在服务器上手动重启后端；页面会继续检测。"}
          </span>
          {restoreReport && (
            <div className="backup-restart-report">
              <span>
                备份校验：
                {restoreReport.verificationStatus === "verified"
                  ? "通过"
                  : restoreReport.verificationStatus}
              </span>
              {!!restoreReport.pathRewrites?.length && (
                <span>已准备 {restoreReport.pathRewrites.length} 项本地路径改写</span>
              )}
              {[
                ...(restoreReport.localStorageWarnings ?? []),
                ...(restoreReport.missingAssets ?? []),
                ...(restoreReport.warnings ?? []),
              ]
                .slice(0, 6)
                .map((warning, index) => (
                  <span key={`${index}-${warning}`}>{warning}</span>
                ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

async function sha256Blob(blob: Blob) {
  const digest = await crypto.subtle.digest("SHA-256", await blob.arrayBuffer());
  return Array.from(new Uint8Array(digest))
    .map((value) => value.toString(16).padStart(2, "0"))
    .join("");
}

function delay(milliseconds: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
}
