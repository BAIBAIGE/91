import { useEffect, useRef, useState, type FormEvent } from "react";
import { CloudUpload, Loader2 } from "lucide-react";
import * as api from "../api";
import { useAdminRouteActive } from "../AdminRouteCache";
import { useToast } from "../ToastContext";
import {
  applyVisualFields,
  changedVisualFields,
  parseConfig,
  type SettingsDraft,
} from "../settings/configYaml";

export function TelegramUploadSettings() {
  const active = useAdminRouteActive();
  const { show } = useToast();
  const [loaded, setLoaded] = useState<SettingsDraft>();
  const [draft, setDraft] = useState<SettingsDraft>();
  const [targets, setTargets] = useState<api.AdminDrive[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const dirty =
    !!loaded && !!draft && changedVisualFields(loaded, draft).size > 0;
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;

  useEffect(() => {
    if (!active) return;
    let disposed = false;
    setLoading(true);
    void Promise.all([api.getConfigYAML(), api.listDrives()])
      .then(([config, drives]) => {
        if (disposed) return;
        const next = parseConfig(config.content).draft;
        setTargets(drives.filter((drive) => drive.canUpload));
        if (!dirtyRef.current) {
          setLoaded(next);
          setDraft(next);
        }
        setError("");
      })
      .catch((err: unknown) => {
        if (!disposed)
          setError(err instanceof Error ? err.message : "无法加载网盘转存设置");
      })
      .finally(() => {
        if (!disposed) setLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, [active, refresh]);

  useEffect(() => {
    if (!dirty) return;
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warnBeforeUnload);
    return () => window.removeEventListener("beforeunload", warnBeforeUnload);
  }, [dirty]);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!loaded || !draft || !dirty || loading || saving) return;
    setSaving(true);
    try {
      const latest = await api.getConfigYAML();
      const content = applyVisualFields(
        latest.content,
        draft,
        changedVisualFields(loaded, draft),
      );
      const next = parseConfig(content).draft;
      await api.updateConfigYAML(content, latest.version);
      setLoaded(next);
      setDraft(next);
      setError("");
      show("网盘转存设置已保存", "success");
    } catch (err) {
      const message =
        err instanceof api.ConfigConflictError
          ? "配置已被其他操作修改，请重新保存。"
          : err instanceof Error
            ? err.message
            : "保存网盘转存设置失败";
      setError(message);
      show(message, "error");
    } finally {
      setSaving(false);
    }
  }

  const disabled = loading || saving || !draft || !active;
  return (
    <section
      className="tg-panel tg-upload-panel"
      aria-labelledby="tg-upload-title"
      aria-busy={loading || saving}
    >
      <div className="tg-heading">
        <h3 id="tg-upload-title">
          <CloudUpload size={16} aria-hidden="true" />网盘转存
        </h3>
        {dirty && (
          <button
            type="button"
            className="tg-cancel-changes"
            disabled={disabled}
            onClick={() => setDraft(loaded)}
          >
            取消更改
          </button>
        )}
        <button
          type="submit"
          form="tg-upload-form"
          className="admin-btn"
          disabled={disabled || !dirty}
        >
          {(loading || saving) && <Loader2 size={14} className="tg-spin" />}
          {loading ? "正在加载…" : saving ? "正在保存…" : "保存"}
        </button>
      </div>
      {error && (
        <div className="tg-actions">
          <p className="tg-error" role="alert">
            {error}
          </p>
          <button
            type="button"
            className="admin-btn"
            disabled={loading || saving}
            onClick={() => setRefresh((n) => n + 1)}
          >
            重新加载
          </button>
        </div>
      )}
      <form id="tg-upload-form" onSubmit={(event) => void save(event)}>
        <fieldset disabled={disabled}>
          <div className="tg-fields">
            <label>
              目标网盘
              <select
                value={draft?.telegramUploadDriveId ?? ""}
                onChange={(event) =>
                  setDraft(
                    (current) =>
                      current && {
                        ...current,
                        telegramUploadDriveId: event.target.value,
                      },
                  )
                }
              >
                <option value="">仅保存在本地</option>
                {draft?.telegramUploadDriveId &&
                  !targets.some(
                    (drive) => drive.id === draft.telegramUploadDriveId,
                  ) && (
                    <option value={draft.telegramUploadDriveId}>
                      当前目标不可用（{draft.telegramUploadDriveId}）
                    </option>
                  )}
                {targets.map((drive) => (
                  <option key={drive.id} value={drive.id}>
                    {drive.name || drive.id}
                  </option>
                ))}
              </select>
            </label>
            <label>
              网盘目录
              <input
                value={draft?.telegramUploadDirectory ?? ""}
                disabled={!draft?.telegramUploadDriveId}
                onChange={(event) =>
                  setDraft(
                    (current) =>
                      current && {
                        ...current,
                        telegramUploadDirectory: event.target.value,
                      },
                  )
                }
                placeholder="Telegram"
              />
            </label>
            <label className="tg-field-wide">
              上传代理
              <input
                value={draft?.telegramUploadProxy ?? ""}
                disabled={!draft?.telegramUploadDriveId}
                onChange={(event) =>
                  setDraft(
                    (current) =>
                      current && {
                        ...current,
                        telegramUploadProxy: event.target.value,
                      },
                  )
                }
                placeholder="支持 HTTP/HTTPS 和 SOCKS5/SOCKS5H"
                autoComplete="off"
                spellCheck={false}
              />
            </label>
          </div>
        </fieldset>
      </form>
    </section>
  );
}
