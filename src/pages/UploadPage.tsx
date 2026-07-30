import {
  ChangeEvent,
  FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Link } from "react-router-dom";
import { Check, Link2, UploadCloud, X } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { SectionHeader } from "@/components/SectionHeader";
import {
  cancelRemoteUpload,
  createRemoteUpload,
  fetchRemoteUploads,
  uploadVideo,
  type RemoteUploadJob,
  type RemoteUploadState,
} from "@/data/videos";
import { defaultUploadTitleFromFileName } from "@/lib/uploadTitle";
import type { VideoItem } from "@/types";

const UPLOAD_TAGS = ["奶子", "女大", "人妻", "后入", "制服", "美臀", "口交"];
const ACTIVE_REMOTE_STATES = new Set<RemoteUploadState>([
  "queued",
  "downloading",
  "validating",
  "saving",
]);
const REMOTE_STATE_LABELS: Record<RemoteUploadState, string> = {
  queued: "排队中",
  downloading: "下载中",
  validating: "校验中",
  saving: "入库中",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
};

type UploadMode = "local" | "remote";

export default function UploadPage() {
  const [mode, setMode] = useState<UploadMode>("local");
  const [file, setFile] = useState<File | null>(null);
  const [remoteURL, setRemoteURL] = useState("");
  const [title, setTitle] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [uploaded, setUploaded] = useState<VideoItem | null>(null);
  const [submittedJob, setSubmittedJob] = useState<RemoteUploadJob | null>(null);
  const [jobs, setJobs] = useState<RemoteUploadJob[]>([]);
  const [jobsLoading, setJobsLoading] = useState(true);
  const [jobsError, setJobsError] = useState("");
  const [cancelingIDs, setCancelingIDs] = useState<Set<string>>(new Set());
  const [pageVisible, setPageVisible] = useState(
    () => document.visibilityState !== "hidden"
  );

  useEffect(() => {
    document.title = "上传视频";
  }, []);

  const refreshJobs = useCallback(async () => {
    try {
      const nextJobs = await fetchRemoteUploads(20);
      setJobs(Array.isArray(nextJobs) ? nextJobs : []);
      setJobsError("");
    } catch (refreshError) {
      setJobsError(
        refreshError instanceof Error ? refreshError.message : "任务列表加载失败"
      );
    } finally {
      setJobsLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshJobs();
  }, [refreshJobs]);

  useEffect(() => {
    function handleVisibilityChange() {
      const visible = document.visibilityState !== "hidden";
      setPageVisible(visible);
      if (visible) {
        void refreshJobs();
      }
    }
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [refreshJobs]);

  const hasActiveJobs = jobs.some((job) => ACTIVE_REMOTE_STATES.has(job.state));

  useEffect(() => {
    if (!pageVisible || !hasActiveJobs) return;
    const intervalID = window.setInterval(() => {
      if (document.visibilityState !== "hidden") {
        void refreshJobs();
      }
    }, 2000);
    return () => window.clearInterval(intervalID);
  }, [hasActiveJobs, pageVisible, refreshJobs]);

  const fileMeta = useMemo(() => {
    if (!file) return "";
    const mb = file.size / 1024 / 1024;
    return `${file.name} · ${mb >= 1 ? mb.toFixed(1) : mb.toFixed(2)} MB`;
  }, [file]);

  const remoteURLValid = useMemo(() => isValidRemoteVideoURL(remoteURL), [remoteURL]);
  const submitDisabled =
    saving || (mode === "local" ? !file : !remoteURLValid);

  function changeMode(nextMode: UploadMode) {
    if (nextMode === mode) return;
    setMode(nextMode);
    setFile(null);
    setRemoteURL("");
    setTitle("");
    setError("");
    setUploaded(null);
    setSubmittedJob(null);
  }

  function handleFileChange(event: ChangeEvent<HTMLInputElement>) {
    const nextFile = event.target.files?.[0] ?? null;
    setFile(nextFile);
    setTitle(nextFile ? defaultUploadTitleFromFileName(nextFile.name) : "");
    setUploaded(null);
    setSubmittedJob(null);
    setError("");
  }

  function toggleTag(tag: string) {
    setTags((current) =>
      current.includes(tag)
        ? current.filter((item) => item !== tag)
        : [...current, tag]
    );
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitDisabled) return;
    setSaving(true);
    setError("");
    setUploaded(null);
    setSubmittedJob(null);
    try {
      if (mode === "local" && file) {
        const video = await uploadVideo({ file, title, tags });
        setUploaded(video);
        setFile(null);
      } else if (mode === "remote") {
        const job = await createRemoteUpload({ url: remoteURL, title, tags });
        setSubmittedJob(job);
        setJobs((current) => [
          job,
          ...current.filter((item) => item.id !== job.id),
        ].slice(0, 20));
        setRemoteURL("");
      }
      setTitle("");
      setTags([]);
    } catch (submitError) {
      setError(
        submitError instanceof Error
          ? submitError.message
          : mode === "remote"
            ? "创建直链任务失败"
            : "上传失败，请检查文件格式后重试"
      );
    } finally {
      setSaving(false);
    }
  }

  async function handleCancel(jobID: string) {
    setCancelingIDs((current) => new Set(current).add(jobID));
    try {
      const updated = await cancelRemoteUpload(jobID);
      setJobs((current) =>
        current.map((item) => (item.id === updated.id ? updated : item))
      );
      setJobsError("");
      void refreshJobs();
    } catch (cancelError) {
      setJobsError(
        cancelError instanceof Error ? cancelError.message : "取消任务失败"
      );
    } finally {
      setCancelingIDs((current) => {
        const next = new Set(current);
        next.delete(jobID);
        return next;
      });
    }
  }

  return (
    <AppShell>
      <div className="container page-section">
        <SectionHeader title="上传视频" />
        <form className="upload-panel" onSubmit={handleSubmit}>
          <div className="upload-mode-switch" role="tablist" aria-label="上传方式">
            <button
              type="button"
              role="tab"
              aria-selected={mode === "local"}
              className={mode === "local" ? "is-active" : ""}
              onClick={() => changeMode("local")}
            >
              本地文件
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={mode === "remote"}
              className={mode === "remote" ? "is-active" : ""}
              onClick={() => changeMode("remote")}
            >
              视频直链
            </button>
          </div>

          {mode === "local" ? (
            <label className="upload-drop">
              <input
                type="file"
                accept="video/*,.avi,.mkv,.mov,.mp4,.webm"
                onChange={handleFileChange}
              />
              <span className="upload-drop__icon">
                <UploadCloud size={28} />
              </span>
              <span className="upload-drop__title">
                {file ? fileMeta : "选择视频文件"}
              </span>
            </label>
          ) : (
            <label className="upload-field upload-remote-url">
              <span>视频直链</span>
              <div className="upload-input-with-icon">
                <Link2 size={18} aria-hidden="true" />
                <input
                  type="url"
                  inputMode="url"
                  value={remoteURL}
                  onChange={(event) => {
                    setRemoteURL(event.target.value);
                    setError("");
                    setSubmittedJob(null);
                  }}
                  placeholder="https://example.com/video.mp4"
                  autoComplete="url"
                  required
                />
              </div>
              <small>
                仅支持无需 Referer、Cookie 或自定义请求头的公网 HTTP/HTTPS 文件直链
              </small>
            </label>
          )}

          <label className="upload-field">
            <span>视频名</span>
            <input
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder={
                mode === "local"
                  ? "选择文件后自动填入"
                  : "可选，留空时从远程文件名生成"
              }
              maxLength={120}
            />
          </label>

          <div className="upload-field">
            <span>标签</span>
            <div className="upload-tags">
              {UPLOAD_TAGS.map((tag) => {
                const active = tags.includes(tag);
                return (
                  <button
                    key={tag}
                    type="button"
                    className={`upload-tag ${active ? "is-active" : ""}`}
                    onClick={() => toggleTag(tag)}
                    aria-pressed={active}
                  >
                    {active ? <Check size={14} /> : null}
                    {tag}
                  </button>
                );
              })}
            </div>
          </div>

          {error ? <div className="upload-message is-error">{error}</div> : null}
          {uploaded ? (
            <div className="upload-message is-success">
              <Check size={16} />
              <Link to={uploaded.href}>查看 {uploaded.title}</Link>
            </div>
          ) : null}
          {submittedJob ? (
            <div className="upload-message is-success">
              <Check size={16} />
              任务已加入后台队列，关闭页面不会中断下载
            </div>
          ) : null}

          <div className="upload-actions">
            <button
              className="upload-submit"
              type="submit"
              disabled={submitDisabled}
            >
              {saving
                ? mode === "remote"
                  ? "创建中"
                  : "上传中"
                : mode === "remote"
                  ? "创建任务"
                  : "上传"}
            </button>
          </div>
        </form>

        <section className="remote-upload-section" aria-labelledby="remote-upload-jobs">
          <SectionHeader title="直链任务" />
          <div id="remote-upload-jobs" className="remote-upload-list">
            {jobsError ? (
              <div className="upload-message is-error">{jobsError}</div>
            ) : null}
            {jobsLoading && jobs.length === 0 ? (
              <div className="remote-upload-empty">正在加载任务…</div>
            ) : null}
            {!jobsLoading && jobs.length === 0 ? (
              <div className="remote-upload-empty">暂无直链任务</div>
            ) : null}
            {jobs.map((job) => {
              const canceling = cancelingIDs.has(job.id);
              const percent =
                job.totalBytes > 0
                  ? Math.min(100, Math.max(0, (job.bytesDownloaded / job.totalBytes) * 100))
                  : null;
              return (
                <article
                  key={job.id}
                  className={`remote-upload-job is-${job.state}`}
                >
                  <div className="remote-upload-job__main">
                    <div className="remote-upload-job__heading">
                      <strong>{job.title || job.sourceLabel}</strong>
                      <span className="remote-upload-job__state">
                        {job.cancelRequested
                          ? "正在取消"
                          : REMOTE_STATE_LABELS[job.state]}
                      </span>
                    </div>
                    {job.title ? (
                      <div className="remote-upload-job__source">
                        {job.sourceLabel}
                      </div>
                    ) : null}
                    <div className="remote-upload-job__progress-copy">
                      {progressLabel(job, percent)}
                    </div>
                    {percent !== null && ACTIVE_REMOTE_STATES.has(job.state) ? (
                      <div
                        className="remote-upload-progress"
                        role="progressbar"
                        aria-valuemin={0}
                        aria-valuemax={100}
                        aria-valuenow={Math.round(percent)}
                      >
                        <span style={{ width: `${percent}%` }} />
                      </div>
                    ) : null}
                    {job.error ? (
                      <div className="remote-upload-job__error">{job.error}</div>
                    ) : null}
                    <time dateTime={job.createdAt}>
                      {formatJobTime(job.createdAt)}
                    </time>
                  </div>
                  <div className="remote-upload-job__actions">
                    {job.canCancel ? (
                      <button
                        type="button"
                        className="remote-upload-cancel"
                        disabled={canceling}
                        onClick={() => void handleCancel(job.id)}
                      >
                        <X size={15} />
                        {canceling ? "取消中" : "取消"}
                      </button>
                    ) : null}
                    {job.state === "completed" && job.videoHref ? (
                      <Link className="remote-upload-detail" to={job.videoHref}>
                        查看视频
                      </Link>
                    ) : null}
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      </div>
    </AppShell>
  );
}

function isValidRemoteVideoURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim());
    if (
      (parsed.protocol !== "http:" && parsed.protocol !== "https:") ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password
    ) {
      return false;
    }
    return !parsed.pathname.toLowerCase().endsWith(".m3u8");
  } catch {
    return false;
  }
}

function progressLabel(
  job: RemoteUploadJob,
  percent: number | null
): string {
  if (job.state === "queued") return "等待前序任务完成";
  if (job.state === "validating") return "正在确认视频流与文件格式";
  if (job.state === "saving") return "正在写入视频库并创建媒体任务";
  if (job.state === "canceled") return "任务已取消，临时文件已清理";
  if (job.state === "failed") return formatBytes(job.bytesDownloaded);
  if (job.state === "completed") {
    return `${formatBytes(job.bytesDownloaded)} · 下载和入库完成`;
  }
  if (percent === null) {
    return `${formatBytes(job.bytesDownloaded)} · 远程大小未知`;
  }
  return `${percent.toFixed(1)}% · ${formatBytes(job.bytesDownloaded)} / ${formatBytes(job.totalBytes)}`;
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const index = Math.min(
    units.length - 1,
    Math.floor(Math.log(bytes) / Math.log(1024))
  );
  const value = bytes / 1024 ** index;
  return `${value >= 10 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`;
}

function formatJobTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}
