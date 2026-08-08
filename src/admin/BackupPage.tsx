import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
} from "react";
import { useNavigate } from "react-router";
import {
  Archive,
  Check,
  CircleAlert,
  Loader2,
} from "lucide-react";
import * as api from "./api";
import { useAuth } from "./AuthContext";
import { ConfirmModal } from "./ConfirmModal";
import { Modal } from "./Modal";
import { useToast } from "./ToastContext";
import { useAdminRouteActive } from "./AdminRouteCache";

const RESUME_KEY = "video-site-91-backup-upload-v1";
const RESTORE_CONFIRMATION_GRACE_MS = 30_000;
const RESTORE_POLL_INTERVAL_MS = 1_200;

type ResumeState = {
  id: string;
  fileName: string;
  size: number;
  lastModified: number;
};

const BACKUP_SELECTION_OPTIONS: Array<{
  key: keyof api.BackupSelection;
  label: string;
}> = [
  { key: "cloudDrives", label: "网盘凭证和对应视频资源" },
  { key: "crawlerScripts", label: "爬虫脚本和对应的视频资源" },
  { key: "uploadStorage", label: "上传存储和对应视频资源" },
  { key: "localStorage", label: "本地存储和对应的视频资源" },
  { key: "userInfo", label: "用户信息" },
];

const EMPTY_BACKUP_SELECTION: api.BackupSelection = {
  cloudDrives: false,
  crawlerScripts: false,
  uploadStorage: false,
  localStorage: false,
  userInfo: false,
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

function backupSelectionLabels(selection: api.BackupSelection | undefined) {
  if (!selection) return ["范围不可用"];
  const labels = BACKUP_SELECTION_OPTIONS.filter((option) => selection[option.key]).map(
    (option) => option.label
  );
  return labels.length > 0 ? labels : ["未选择内容"];
}

function taskActive(task: api.BackupTask | undefined) {
  return task?.state === "queued" || task?.state === "running" || task?.state === "canceling";
}

function shouldConfirmRestoreAfterTransportError(error: unknown) {
  if (error instanceof api.UnauthorizedError) return false;
  return !(error instanceof api.APIResponseError) || error.status >= 500;
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
      return "进行完整校验";
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

type ChecklistState = "done" | "active" | "pending";

type ChecklistStep = {
  title: string;
  state: ChecklistState;
};

function checklistState(index: number, activeIndex: number, complete = false): ChecklistState {
  if (complete || index < activeIndex) return "done";
  return index === activeIndex ? "active" : "pending";
}

function operationPercent(progress: api.BackupOperationProgress | undefined) {
  if (!progress?.totalBytes) return null;
  return Math.min(100, Math.max(0, (progress.processedBytes / progress.totalBytes) * 100));
}

function operationDetail(percent: number | null) {
  if (percent === null) return "处理中";
  return `${percent.toFixed(1)}%`;
}

function BackupOperationChecklist({
  title,
  steps,
  progress,
}: {
  title: string;
  steps: ChecklistStep[];
  progress?: api.BackupOperationProgress;
}) {
  const percent = operationPercent(progress);

  return (
    <section className="backup-operation-checklist" role="status" aria-live="polite">
      <div className="backup-operation-checklist__head">
        <strong>{title}</strong>
      </div>
      <ol className="backup-operation-steps">
        {steps.map((step, index) => (
          <li key={step.title} className={`is-${step.state}`}>
            <span className="backup-operation-step__marker" aria-hidden="true">
              {step.state === "done" ? (
                <Check size={14} strokeWidth={2.6} />
              ) : step.state === "active" ? (
                <Loader2 size={14} className="admin-spin" />
              ) : (
                index + 1
              )}
            </span>
            <div className="backup-operation-step__content">
              <strong>{step.title}</strong>
              <span className="sr-only">
                {step.state === "done" ? "已完成" : step.state === "active" ? "进行中" : "等待"}
              </span>
              {step.state === "active" && (
                <div className="backup-operation-step__progress">
                  <div
                    className={`backup-progress ${percent === null ? "is-indeterminate" : ""}`}
                    role={percent === null ? undefined : "progressbar"}
                    aria-valuemin={percent === null ? undefined : 0}
                    aria-valuemax={percent === null ? undefined : 100}
                    aria-valuenow={percent === null ? undefined : Math.round(percent)}
                  >
                    <span style={percent === null ? undefined : { width: `${percent}%` }} />
                  </div>
                  <span>{operationDetail(percent)}</span>
                </div>
              )}
            </div>
          </li>
        ))}
      </ol>
    </section>
  );
}

function uploadFinalizeStepIndex(phase: string | undefined) {
  switch (phase) {
    case "inspecting-archive":
    case "verifying-archive":
    case "verifying-database":
      return 2;
    case "publishing":
      return 3;
    default:
      return 1;
  }
}

function restorePrepareStepIndex(phase: string | undefined) {
  switch (phase) {
    case "extracting":
      return 1;
    case "checking-database":
      return 2;
    case "rewriting":
      return 3;
    case "preparing-switch":
    case "ready":
      return 4;
    default:
      return 0;
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
  const routeActive = useAdminRouteActive();
  const { invalidateSession } = useAuth();
  const { show } = useToast();
  const [data, setData] = useState<api.BackupList | null>(null);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [backupSelection, setBackupSelection] = useState<api.BackupSelection>(
    EMPTY_BACKUP_SELECTION
  );
  const [deleteTarget, setDeleteTarget] = useState<api.BackupRecord | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<api.BackupRecord | null>(null);
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
  const restoreConfirmationStartedAt = useRef<number | null>(null);

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
    if (!routeActive) return;
    void refresh();
    const timer = window.setInterval(() => void refresh(true), 2000);
    return () => window.clearInterval(timer);
  }, [routeActive]);

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
    if (!finalizing || !upload?.id) return;
    let active = true;
    let timer = 0;
    const poll = async () => {
      try {
        const session = await api.getBackupUpload(upload.id);
        if (active) setUpload(session);
      } catch {
        // The finalize request owns success and error reporting. A 404 here
        // normally means it just published and removed the upload session.
      }
      if (active) timer = window.setTimeout(poll, 450);
    };
    void poll();
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [finalizing, upload?.id]);

  useEffect(() => {
    if (!restoreSubmitting) return;
    let active = true;
    let timer = 0;
    const poll = async () => {
      try {
        const next = await api.listBackups();
        if (active) setData(next);
      } catch {
        // The restore request surfaces the authoritative error.
      }
      if (active) timer = window.setTimeout(poll, 500);
    };
    void poll();
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [restoreSubmitting]);

  useEffect(() => {
    if (!restoring) {
      restoreConfirmationStartedAt.current = null;
      return;
    }
    let active = true;
    const startedAt = restoreConfirmationStartedAt.current ?? Date.now();
    restoreConfirmationStartedAt.current = startedAt;
    const redirectToLogin = () => {
      if (!active) return;
      // Restore deliberately clears every server session. Keep the central
      // auth state in sync before LoginPage decides whether to redirect.
      invalidateSession();
      navigate("/login", { replace: true });
    };
    const poll = async () => {
      try {
        const state = await api.me();
        if (active && !state.authenticated) {
          redirectToLogin();
          return;
        }
        if (active && state.authenticated) {
          const backupState = await api.listBackups();
          if (!active) return;
          setData(backupState);
          // A request can lose its response while the service is preparing to
          // restart. Keep confirming while preparation or a pending marker is
          // visible; only report failure after a bounded grace period.
          const restoreInProgress =
            backupState.pendingRestore || Boolean(backupState.restoreProgress);
          if (
            !restoreInProgress &&
            Date.now() - startedAt >= RESTORE_CONFIRMATION_GRACE_MS
          ) {
            setRestoring(false);
            setRestoreReport(null);
            show("未确认恢复已启动，当前数据保持不变，请重试", "error");
            return;
          }
        }
      } catch (error) {
        if (active && error instanceof api.UnauthorizedError) {
          redirectToLogin();
          return;
        }
      }
      if (active) window.setTimeout(poll, RESTORE_POLL_INTERVAL_MS);
    };
    const timer = window.setTimeout(poll, 1000);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [restoring, navigate, invalidateSession, show]);

  const current = data?.current;
  const progress = useMemo(() => {
    if (!current?.totalBytes) return 0;
    return Math.min(100, Math.max(0, (current.processedBytes / current.totalBytes) * 100));
  }, [current?.processedBytes, current?.totalBytes]);

  function handleCreate() {
    setBackupSelection({ ...EMPTY_BACKUP_SELECTION });
    setCreateOpen(true);
  }

  async function handleConfirmCreate() {
    if (!Object.values(backupSelection).some(Boolean)) return;
    setCreating(true);
    try {
      await api.createBackup(backupSelection);
      setCreateOpen(false);
      show("备份任务已开始", "success");
      await refresh(true);
    } catch (error) {
      show(error instanceof Error ? error.message : "创建备份失败", "error");
    } finally {
      setCreating(false);
    }
  }

  function toggleBackupSelection(key: keyof api.BackupSelection) {
    setBackupSelection((currentSelection) => ({
      ...currentSelection,
      [key]: !currentSelection[key],
    }));
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
      setUpload({
        ...session,
        state: "finalizing",
        progress: {
          phase: "preparing",
          processedBytes: 0,
          totalBytes: session.size,
          processedFiles: 0,
          totalFiles: session.totalChunks,
        },
      });
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
    setData((currentData) =>
      currentData ? { ...currentData, restoreProgress: undefined } : currentData
    );
    setRestoreSubmitting(true);
    try {
      const result = await api.restoreBackup(restoreTarget.id, {
        confirmation: restoreText,
      });
      setRestartManaged(result.restartManaged);
      setRestoreReport(result.report);
      setRestoreTarget(null);
      setRestoreText("");
      restoreConfirmationStartedAt.current = Date.now();
      setRestoring(true);
      show("恢复已通过校验，服务正在切换数据并重启", "success");
    } catch (error) {
      if (shouldConfirmRestoreAfterTransportError(error)) {
        // The server may already have staged the restore and restarted before
        // the browser received its acknowledgement. Confirm the durable state
        // instead of falsely reporting a failed restore.
        setRestoreTarget(null);
        setRestoreText("");
        restoreConfirmationStartedAt.current = Date.now();
        setRestoring(true);
        show("连接暂时中断，正在确认恢复状态", "success");
      } else {
        show(error instanceof Error ? error.message : "恢复失败", "error");
      }
    } finally {
      setRestoreSubmitting(false);
    }
  }

  function closeRestore() {
    if (restoreSubmitting || restoring) return;
    setRestoreTarget(null);
    setRestoreText("");
  }

  const estimate = data?.estimate;
  const receivedBytes =
    upload?.received.reduce((sum, chunk) => sum + chunk.size, 0) ?? 0;
  const uploadPercent = upload?.size ? Math.min(100, (receivedBytes / upload.size) * 100) : 0;
  const uploadFinalizeIndex = uploadFinalizeStepIndex(upload?.progress?.phase);
  const uploadActiveProgress =
    upload?.progress?.phase === "hashing" || upload?.progress?.phase === "verifying-archive"
      ? upload.progress
      : undefined;
  const uploadFinalizeSteps: ChecklistStep[] = [
    {
      title: "分片写入暂存文件",
      state: "done",
    },
    {
      title: "校验完整文件",
      state: checklistState(1, uploadFinalizeIndex),
    },
    {
      title: "校验备份内容",
      state: checklistState(2, uploadFinalizeIndex),
    },
    {
      title: "原子入库",
      state: checklistState(3, uploadFinalizeIndex),
    },
  ];
  const restoreProgress = data?.restoreProgress;
  const restoreActiveProgress = restoreProgress?.phase === "extracting" ? restoreProgress : undefined;
  const restoreReady = restoreProgress?.phase === "ready";
  const restorePrepareIndex = restorePrepareStepIndex(restoreProgress?.phase);
  const restorePrepareSteps: ChecklistStep[] = [
    {
      title: "读取归档清单",
      state: checklistState(0, restorePrepareIndex, restoreReady),
    },
    {
      title: "校验并解压暂存",
      state: checklistState(1, restorePrepareIndex, restoreReady),
    },
    {
      title: "检查暂存数据库",
      state: checklistState(2, restorePrepareIndex, restoreReady),
    },
    {
      title: "适配本机运行数据",
      state: checklistState(3, restorePrepareIndex, restoreReady),
    },
    {
      title: "准备原子切换",
      state: checklistState(4, restorePrepareIndex, restoreReady),
    },
  ];
  const restoreWarnings = [
    ...(restoreReport?.localStorageWarnings ?? []),
    ...(restoreReport?.missingAssets ?? []),
    ...(restoreReport?.warnings ?? []),
  ];

  return (
    <div
      className="admin-page backup-page"
      aria-busy={loading || undefined}
    >
      <section className="backup-overview" aria-label="备份空间概览">
        <div className="backup-stat">
          <span>预计数据量</span>
          <strong>{data ? formatBytes(estimate?.totalBytes) : null}</strong>
        </div>
        <div className="backup-stat">
          <span>服务器可用空间</span>
          <strong>{data ? formatBytes(estimate?.availableBytes) : null}</strong>
        </div>
        <div className="backup-stat">
          <span>备份数量</span>
          <strong>{data ? data.backups.length : null}</strong>
        </div>
      </section>

      {current && taskActive(current) && (
        <section className={`backup-task ${current.state === "failed" ? "is-error" : ""}`}>
          <div className="backup-task__head">
            <div className="backup-task__title">
              <strong>{taskPhase(current.phase)}</strong>
            </div>
          </div>
          <div className="backup-task__progress-row">
            <div className="backup-task__meta">
              <span>
                {current.processedFiles}/{current.fileCount} 文件 · {formatBytes(current.processedBytes)} /{" "}
                {formatBytes(current.totalBytes)} · {formatBytes(current.bytesPerSecond)}/s
              </span>
            </div>
            <strong className="backup-task__percent">{progress.toFixed(1)}%</strong>
            {taskActive(current) && current.cancellable && (
              <button
                type="button"
                className="admin-btn is-transparent"
                onClick={handleCancelBackup}
              >
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
          {current.error && <p className="backup-task__error">{current.error}</p>}
        </section>
      )}

      <div className="backup-grid">
        <section className="admin-card backup-upload-card">
          <div className="backup-section-heading">
            <h2>上传备份包</h2>
          </div>
          <div className="backup-upload-controls">
            <label
              className="backup-file-picker"
              title={
                resumeHint
                  ? `检测到未完成上传：${resumeHint.fileName}，重新选择同一文件继续`
                  : "16 MiB 分片上传，支持暂停与断点续传"
              }
            >
              <span>{file ? file.name : "选择 ZIP 备份包"}</span>
              <input
                type="file"
                accept=".zip,application/zip"
                onChange={chooseFile}
                disabled={!data || uploading || finalizing}
              />
            </label>
            <div className="backup-upload-actions">
              {finalizing ? null : !uploading ? (
                <button
                  type="button"
                  className="admin-btn backup-upload-submit"
                  onClick={handleUpload}
                  disabled={!data || !file || finalizing}
                >
                  {upload?.received.length ? "继续上传" : "开始上传"}
                </button>
              ) : (
                <button type="button" className="admin-btn" onClick={handlePause}>
                  暂停
                </button>
              )}
              {upload && (
                <button
                  type="button"
                  className="admin-btn"
                  onClick={handleCancelUpload}
                  disabled={finalizing}
                >
                  取消
                </button>
              )}
            </div>
          </div>
          {upload && finalizing ? (
            <BackupOperationChecklist
              title="校验并入库"
              steps={uploadFinalizeSteps}
              progress={uploadActiveProgress}
            />
          ) : upload ? (
            <div className="backup-upload-progress">
              <div className="backup-progress">
                <span style={{ width: `${uploadPercent}%` }} />
              </div>
              <div className="backup-upload-progress__meta">
                <span>
                  {upload.received.length}/{upload.totalChunks} 分片 · {formatBytes(receivedBytes)} /{" "}
                  {formatBytes(upload.size)} · {uploading ? "上传中" : "已暂停"}
                </span>
              </div>
            </div>
          ) : null}
        </section>

        <section className="backup-list-section">
          <div className="backup-section-heading">
            <h2>备份列表</h2>
            <button
              type="button"
              className="admin-btn is-transparent"
              onClick={handleCreate}
              disabled={!data || creating || taskActive(current) || data.pendingRestore}
            >
              {creating ? <Loader2 size={15} className="admin-spin" /> : null}
              创建备份
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
                      <span>{formatTime(record.createdAt)}</span>
                      {record.imported && <span className="backup-badge">迁移</span>}
                      <span className={`backup-verify is-${record.verificationStatus}`}>
                        {record.verificationStatus === "verified"
                          ? "已校验"
                          : record.verificationStatus === "invalid"
                            ? "校验失败"
                            : "待校验"}
                      </span>
                    </div>
                    <div className="backup-scope-list" aria-label="备份范围">
                      {backupSelectionLabels(record.selection).map((label) => (
                        <span className="backup-scope" key={label}>{label}</span>
                      ))}
                    </div>
                    {record.verificationError && (
                      <div className="backup-record__error">{record.verificationError}</div>
                    )}
                  </div>
                  <div className="backup-record__actions">
                    <a className="admin-btn" href={api.backupDownloadURL(record.id)}>
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
                      恢复
                    </button>
                    <button
                      type="button"
                      className="admin-btn is-danger"
                      onClick={() => setDeleteTarget(record)}
                      disabled={data.pendingRestore}
                    >
                      删除
                    </button>
                  </div>
                </article>
              ))}
            </div>
          ) : data ? (
            <div className="backup-empty">
              <Archive size={28} />
              <span>当前没有备份包</span>
            </div>
          ) : null}
        </section>
      </div>

      <ConfirmModal
        open={deleteTarget !== null}
        title="删除备份"
        message={`确定要永久删除「${deleteTarget?.name ?? ""}」吗？`}
        danger
        hideIcon
        loading={deleting}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={handleDelete}
      />

      <Modal
        open={createOpen}
        title="选择备份内容"
        className="admin-modal--backup-create"
        onClose={() => {
          if (!creating) setCreateOpen(false);
        }}
        footer={
          <>
            <button
              type="button"
              className="admin-btn"
              onClick={() => setCreateOpen(false)}
              disabled={creating}
            >
              取消
            </button>
            <button
              type="button"
              className="admin-btn"
              onClick={handleConfirmCreate}
              disabled={creating || !Object.values(backupSelection).some(Boolean)}
            >
              {creating ? <Loader2 size={14} className="admin-spin" /> : null}
              确认
            </button>
          </>
        }
      >
        <fieldset className="backup-selection-list" aria-label="备份内容">
          {BACKUP_SELECTION_OPTIONS.map((option) => (
            <label className="backup-selection-option" key={option.key}>
              <input
                type="checkbox"
                checked={backupSelection[option.key]}
                onChange={() => toggleBackupSelection(option.key)}
                disabled={creating}
              />
              <span>{option.label}</span>
            </label>
          ))}
        </fieldset>
      </Modal>

      <Modal
        open={restoreTarget !== null || restoring}
        title={restoring ? "应用恢复并重启服务" : "确认恢复"}
        className="admin-modal--backup-restore"
        onClose={closeRestore}
        footer={
          restoring ? undefined : (
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
                disabled={restoreText !== "确认恢复" || restoreSubmitting}
                onClick={handleRestore}
              >
                {restoreSubmitting ? <Loader2 size={14} className="admin-spin" /> : null}
                确认
              </button>
            </>
          )
        }
      >
        {restoreTarget && restoreSubmitting ? (
          <BackupOperationChecklist
            title="恢复中"
            steps={restorePrepareSteps}
            progress={restoreActiveProgress}
          />
        ) : restoring ? (
          <>
            <BackupOperationChecklist
              title="应用恢复并重启服务"
              steps={[
                {
                  title: "校验并暂存备份数据",
                  state: "done",
                },
                {
                  title: "准备可回滚切换",
                  state: "done",
                },
                {
                  title: "切换持久数据",
                  state: "active",
                },
                {
                  title: "重启并重新登录",
                  state: "pending",
                },
              ]}
            />
            <span className="sr-only">
              {restartManaged ? "服务就绪后返回登录页" : "请手动重启后端，页面会继续检测"}；
              {restoreWarnings.slice(0, 6).join("；")}
            </span>
          </>
        ) : restoreTarget ? (
          <div className="backup-restore-form">
            <div className="backup-restore-warning">
              <CircleAlert size={18} />
              <span>恢复所选内容并重启</span>
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
                <dd>
                  {restoreTarget.verificationStatus === "verified"
                    ? "已完整校验"
                    : "恢复前将重新完整校验"}
                </dd>
              </div>
              <div className="backup-restore-summary__scope">
                <dt>恢复内容</dt>
                <dd>
                  <span className="backup-scope-list">
                    {backupSelectionLabels(restoreTarget.selection).map((label) => (
                      <span className="backup-scope" key={label}>{label}</span>
                    ))}
                  </span>
                </dd>
              </div>
            </dl>
            <label className="backup-field">
              <span>输入“确认恢复”</span>
              <input
                className="admin-input"
                value={restoreText}
                onChange={(event) => setRestoreText(event.target.value)}
                placeholder="确认恢复"
                autoComplete="off"
              />
            </label>
          </div>
        ) : null}
      </Modal>
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
