import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  Check,
  Clock3,
  Loader2,
  RefreshCw,
  SlidersHorizontal,
  Tags,
  type LucideIcon,
} from "lucide-react";
import { invalidateTagsCache } from "@/data/videos";
import * as api from "./api";
import { useToast } from "./ToastContext";
import { ConfigDiffModal } from "./settings/ConfigDiffModal";
import {
  ConfigSourceWorkspace,
  preloadConfigSourceEditor,
} from "./settings/ConfigSourceWorkspace";
import { useAdminFloatingActionSpace } from "./useAdminFloatingActionSpace";
import { useAdminRouteRevalidation } from "./AdminRouteCache";
import { SettingsRow, SettingsSection } from "./settings/SettingsSection";
import {
  DEFAULT_DRAFT,
  applyVisualFields,
  changedVisualFields,
  isValidStartTime,
  isValidTimezone,
  parseConfig,
  type SettingsDraft,
  type VisualField,
} from "./settings/configYaml";

type LoadedConfig = {
  content: string;
  version: string;
  visual: SettingsDraft;
};

type PendingSave = {
  before: string;
  after: string;
  version: string;
};

type EditorTab = "visual" | "source";
type SectionID = "config-automation" | "config-tags";

const NIGHTLY_TIMEZONE_OPTIONS = [
  "Asia/Shanghai",
  "Etc/UTC",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Europe/London",
  "America/New_York",
  "America/Los_Angeles",
] as const;

const SECTION_META: Array<{
  id: SectionID;
  index: string;
  title: string;
  icon: LucideIcon;
}> = [
  {
    id: "config-automation",
    index: "01",
    title: "定时任务",
    icon: Clock3,
  },
  {
    id: "config-tags",
    index: "02",
    title: "内置标签",
    icon: Tags,
  },
];

export function SettingsPage() {
  const floatingActionPageRef = useAdminFloatingActionSpace<HTMLFormElement>();
  const { show } = useToast();
  const [loaded, setLoaded] = useState<LoadedConfig | null>(null);
  const [draft, setDraft] = useState<SettingsDraft>(DEFAULT_DRAFT);
  const [workingYAML, setWorkingYAML] = useState("");
  const [sourceTouched, setSourceTouched] = useState(false);
  const [activeTab, setActiveTab] = useState<EditorTab>("visual");
  const [activeSection, setActiveSection] = useState<SectionID>("config-automation");
  const [sourceError, setSourceError] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [saving, setSaving] = useState(false);
  const [pendingSave, setPendingSave] = useState<PendingSave | null>(null);

  const visualDirtyFields = useMemo(
    () => (loaded ? changedVisualFields(loaded.visual, draft) : new Set<VisualField>()),
    [draft, loaded]
  );
  const dirty =
    loaded !== null &&
    (sourceTouched ? workingYAML !== loaded.content : visualDirtyFields.size > 0);
  const dirtyRef = useRef(dirty);
  dirtyRef.current = dirty;
  const timeValid = isValidStartTime(draft.nightlyStartTime);
  const timezoneValid = isValidTimezone(draft.nightlyTimezone);
  const timezoneIsBuiltIn = NIGHTLY_TIMEZONE_OPTIONS.some(
    (timezone) => timezone === draft.nightlyTimezone
  );
  const controlsDisabled = loading || saving || loaded === null;
  const statusClass = loading
    ? ""
    : sourceError
      ? "is-error"
      : dirty
        ? "is-dirty"
        : "is-saved";
  const statusText = loading
    ? "加载配置中"
    : sourceError
      ? "配置有误"
      : saving
        ? "处理中"
        : dirty
          ? "有未保存更改"
          : "配置已加载";

  async function load(silent = false) {
    if (!silent) {
      setLoading(true);
      setLoadError("");
    }
    try {
      const next = await api.getConfigYAML();
      if (silent && dirtyRef.current) return;
      try {
        const parsed = parseConfig(next.content);
        const snapshot = { ...next, visual: parsed.draft };
        setLoaded(snapshot);
        setDraft(parsed.draft);
        setWorkingYAML(next.content);
        setSourceTouched(false);
        setSourceError("");
      } catch (parseError) {
        // Keep an externally damaged file editable. The runtime manager still
        // serves its last known-good snapshot while the source editor repairs
        // the bytes currently on disk.
        const message =
          parseError instanceof Error ? parseError.message : "config.yaml 格式无效";
        setLoaded({ ...next, visual: DEFAULT_DRAFT });
        setDraft(DEFAULT_DRAFT);
        setWorkingYAML(next.content);
        setSourceTouched(true);
        setSourceError(message);
        setActiveTab("source");
        show("config.yaml 当前无效，请在源码模式修正后保存", "error");
      }
      setPendingSave(null);
      setLoadError("");
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载配置失败";
      if (!silent) {
        setLoadError(message);
        show(message, "error");
      }
    } finally {
      if (!silent) setLoading(false);
    }
  }

  useEffect(() => {
    void load();
  }, []);

  useAdminRouteRevalidation(() => {
    if (!dirty) void load(true);
  });

  useEffect(() => {
    if (loading) return;

    const preload = () => {
      void preloadConfigSourceEditor().catch(() => undefined);
    };
    if (typeof window.requestIdleCallback === "function") {
      const idleID = window.requestIdleCallback(preload, { timeout: 1_500 });
      return () => window.cancelIdleCallback(idleID);
    }

    const timeoutID = window.setTimeout(preload, 250);
    return () => window.clearTimeout(timeoutID);
  }, [loading]);

  useEffect(() => {
    if (!dirty) return;
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warnBeforeUnload);
    return () => window.removeEventListener("beforeunload", warnBeforeUnload);
  }, [dirty]);

  function candidateForLatest(latest: api.ConfigYAMLDocument) {
    if (!loaded) throw new Error("配置尚未加载");
    if (sourceTouched) return workingYAML;
    return applyVisualFields(latest.content, draft, visualDirtyFields);
  }

  async function prepareSave(event: FormEvent) {
    event.preventDefault();
    if (!loaded || !dirty || !timeValid || !timezoneValid || sourceError || saving) return;
    try {
      parseConfig(workingYAML);
    } catch (error) {
      const message = error instanceof Error ? error.message : "config.yaml 格式无效";
      setSourceError(message);
      show(message, "error");
      return;
    }

    setSaving(true);
    try {
      const latest = await api.getConfigYAML();
      const candidate = candidateForLatest(latest);
      parseConfig(candidate);
      if (candidate === latest.content) {
        show("磁盘配置已经包含这些更改", "info");
        const visual = parseConfig(latest.content).draft;
        setLoaded({ ...latest, visual });
        setWorkingYAML(latest.content);
        setDraft(visual);
        setSourceTouched(false);
        return;
      }
      setPendingSave({
        before: latest.content,
        after: candidate,
        version: latest.version,
      });
    } catch (error) {
      show(error instanceof Error ? error.message : "准备配置差异失败", "error");
    } finally {
      setSaving(false);
    }
  }

  async function confirmSave() {
    if (!pendingSave || !loaded || saving) return;
    setSaving(true);
    try {
      // The preview can stay open for an arbitrary amount of time. Re-read
      // before committing so confirmation never authorizes a stale overwrite.
      const latest = await api.getConfigYAML();
      if (latest.version !== pendingSave.version) {
        const rebased = candidateForLatest(latest);
        parseConfig(rebased);
        setPendingSave({ before: latest.content, after: rebased, version: latest.version });
        show("config.yaml 又发生了变化，差异已更新，请重新确认", "info");
        return;
      }

      const response = await api.updateConfigYAML(pendingSave.after, pendingSave.version);
      const visual = parseConfig(pendingSave.after).draft;
      const builtinTagsChanged =
        loaded.visual.builtinTagsEnabled !== visual.builtinTagsEnabled;
      setLoaded({ content: pendingSave.after, version: response.version, visual });
      setWorkingYAML(pendingSave.after);
      setDraft(visual);
      setSourceTouched(false);
      setSourceError("");
      setPendingSave(null);
      if (builtinTagsChanged) invalidateTagsCache();
      show(
        response.restartRequired
          ? "配置已保存；部分字段需重启服务后生效"
          : "配置已保存并生效",
        response.restartRequired ? "info" : "success"
      );
    } catch (error) {
      if (error instanceof api.ConfigConflictError) {
        try {
          const latest = await api.getConfigYAML();
          const rebased = candidateForLatest(latest);
          setPendingSave({ before: latest.content, after: rebased, version: latest.version });
          show("保存前检测到并发修改，差异已更新，请重新确认", "info");
        } catch (refreshError) {
          show(refreshError instanceof Error ? refreshError.message : "刷新配置失败", "error");
        }
      } else {
        show(error instanceof Error ? error.message : "保存配置失败", "error");
      }
    } finally {
      setSaving(false);
    }
  }

  function resetDraft() {
    if (!loaded) return;
    setWorkingYAML(loaded.content);
    try {
      const parsed = parseConfig(loaded.content);
      setDraft(parsed.draft);
      setSourceTouched(false);
      setSourceError("");
    } catch (error) {
      setDraft(DEFAULT_DRAFT);
      setSourceTouched(true);
      setSourceError(error instanceof Error ? error.message : "config.yaml 格式无效");
      setActiveTab("source");
    }
  }

  function handleTabChange(nextTab: EditorTab) {
    if (nextTab === activeTab) return;
    if (nextTab === "visual") {
      try {
        const parsed = parseConfig(workingYAML);
        setDraft(parsed.draft);
        setSourceError("");
      } catch (error) {
        const message = error instanceof Error ? error.message : "config.yaml 格式无效";
        setSourceError(message);
        show("请先修正 YAML 错误再切换到可视化编辑", "error");
        return;
      }
    }
    setActiveTab(nextTab);
  }

  function handleSourceChange(value: string) {
    setWorkingYAML(value);
    setSourceTouched(true);
    try {
      const parsed = parseConfig(value);
      setDraft(parsed.draft);
      setSourceError("");
    } catch (error) {
      setSourceError(error instanceof Error ? error.message : "config.yaml 格式无效");
    }
  }

  function updateVisualField<Field extends VisualField>(field: Field, value: SettingsDraft[Field]) {
    const next = { ...draft, [field]: value };
    try {
      if (loaded && !sourceTouched && changedVisualFields(loaded.visual, next).size === 0) {
        setDraft(next);
        setWorkingYAML(loaded.content);
        setSourceError("");
        return;
      }
      const nextYAML = applyVisualFields(workingYAML, next, new Set<VisualField>([field]));
      setDraft(next);
      setWorkingYAML(nextYAML);
      setSourceError("");
    } catch (error) {
      show(error instanceof Error ? error.message : "更新配置失败", "error");
    }
  }

  if (!loading && (loadError || !loaded)) {
    return (
      <div className="admin-page admin-config-page admin-config-page--error">
        <SlidersHorizontal size={26} aria-hidden="true" />
        <strong>配置加载失败</strong>
        <span>{loadError || "暂时无法读取 config.yaml"}</span>
        <button type="button" className="admin-btn is-primary" onClick={() => void load()}>
          重新加载
        </button>
      </div>
    );
  }

  return (
    <>
      <form
        ref={floatingActionPageRef}
        className="admin-page admin-page--with-floating-actions admin-config-page"
        aria-busy={loading}
        onSubmit={prepareSave}
      >
        <header className="admin-config-header">
          <div className="admin-config-tabs" role="tablist" aria-label="配置编辑模式">
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "visual"}
              className={activeTab === "visual" ? "is-active" : ""}
              disabled={loading || saving}
              onClick={() => handleTabChange("visual")}
            >
              可视化编辑
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "source"}
              className={activeTab === "source" ? "is-active" : ""}
              disabled={loading || saving}
              onClick={() => handleTabChange("source")}
            >
              源码编辑
            </button>
          </div>
        </header>

        {activeTab === "visual" ? (
          <div className="admin-config-visual" role="tabpanel">
            <nav
              className="admin-config-section-nav"
              role="tablist"
              aria-label="配置分组"
            >
              {SECTION_META.map((section) => {
                const Icon = section.icon;
                return (
                  <button
                    key={section.id}
                    id={`${section.id}-tab`}
                    type="button"
                    role="tab"
                    aria-selected={activeSection === section.id}
                    aria-controls={section.id}
                    className={activeSection === section.id ? "is-active" : ""}
                    onClick={() => setActiveSection(section.id)}
                  >
                    <span className="admin-config-section-nav__index">{section.index}</span>
                    <span className="admin-config-section-nav__icon" aria-hidden="true">
                      <Icon size={16} />
                    </span>
                    <span>{section.title}</span>
                  </button>
                );
              })}
            </nav>

            <div className="admin-config-sections">
              {activeSection === "config-automation" && (
                <SettingsSection
                  id="config-automation"
                  index="01"
                  icon={<Clock3 size={16} />}
                  title="定时任务"
                  description="按指定时区控制每日扫盘和库内视频维护时间"
                >
                  <SettingsRow
                    label="启动时间"
                    htmlFor="nightly-start-time"
                    layout="inline"
                  >
                    <div className="admin-config-control admin-config-control--picker">
                      <div
                        className={`admin-config-picker-field admin-config-picker-field--time${
                          !timeValid ? " is-invalid" : ""
                        }${controlsDisabled ? " is-disabled" : ""}`}
                      >
                        <span
                          className="admin-config-picker-field__value admin-config-picker-field__value--time"
                          aria-hidden="true"
                        >
                          {draft.nightlyStartTime || "--:--"}
                        </span>
                        <input
                          id="nightly-start-time"
                          type="time"
                          step={60}
                          value={draft.nightlyStartTime}
                          disabled={controlsDisabled}
                          aria-invalid={!timeValid}
                          aria-describedby={!timeValid ? "nightly-start-time-hint" : undefined}
                          onClick={(event) => {
                            try {
                              event.currentTarget.showPicker();
                            } catch {
                              // The input's native click behavior remains the fallback.
                            }
                          }}
                          onChange={(event) =>
                            updateVisualField("nightlyStartTime", event.target.value)
                          }
                        />
                      </div>
                      {!timeValid && (
                        <span id="nightly-start-time-hint" className="is-error">
                          请选择有效时间
                        </span>
                      )}
                    </div>
                  </SettingsRow>
                  <SettingsRow
                    label="任务时区"
                    htmlFor="nightly-timezone"
                    layout="inline"
                  >
                    <div className="admin-config-control admin-config-control--picker">
                      <div
                        className={`admin-config-picker-field admin-config-picker-field--timezone${
                          !timezoneValid ? " is-invalid" : ""
                        }${controlsDisabled ? " is-disabled" : ""}`}
                      >
                        <span className="admin-config-picker-field__value" aria-hidden="true">
                          {draft.nightlyTimezone || "--"}
                        </span>
                        <select
                          id="nightly-timezone"
                          value={draft.nightlyTimezone}
                          disabled={controlsDisabled}
                          aria-invalid={!timezoneValid}
                          aria-describedby={!timezoneValid ? "nightly-timezone-hint" : undefined}
                          onChange={(event) =>
                            updateVisualField("nightlyTimezone", event.target.value)
                          }
                        >
                          {!timezoneIsBuiltIn && (
                            <option value={draft.nightlyTimezone}>
                              {draft.nightlyTimezone || "无效时区"}
                            </option>
                          )}
                          {NIGHTLY_TIMEZONE_OPTIONS.map((timezone) => (
                            <option key={timezone} value={timezone}>
                              {timezone}
                            </option>
                          ))}
                        </select>
                      </div>
                      {!timezoneValid && (
                        <span id="nightly-timezone-hint" className="is-error">
                          请输入有效的 IANA 时区名
                        </span>
                      )}
                    </div>
                  </SettingsRow>
                </SettingsSection>
              )}
              {activeSection === "config-tags" && (
                <SettingsSection
                  id="config-tags"
                  index="02"
                  icon={<Tags size={16} />}
                  title="内置标签"
                  description="管理系统内置标签"
                >
                  <SettingsRow
                    label="内置标签"
                    labelID="builtin-tags-label"
                    layout="inline"
                  >
                    <div className="admin-config-control admin-config-control--switch">
                      <span className="admin-config-control__status">
                        {visualDirtyFields.has("builtinTagsEnabled")
                          ? draft.builtinTagsEnabled
                            ? "待恢复"
                            : "待移除"
                          : draft.builtinTagsEnabled
                            ? "已启用"
                            : "已移除"}
                      </span>
                      <button
                        id="builtin-tags-toggle"
                        type="button"
                        className={`toggle-switch ${draft.builtinTagsEnabled ? "is-on" : ""}`}
                        role="switch"
                        aria-checked={draft.builtinTagsEnabled}
                        aria-labelledby="builtin-tags-label"
                        disabled={controlsDisabled}
                        onClick={() =>
                          updateVisualField("builtinTagsEnabled", !draft.builtinTagsEnabled)
                        }
                      >
                        <span className="toggle-switch__dot" />
                      </button>
                    </div>
                  </SettingsRow>
                </SettingsSection>
              )}
            </div>
          </div>
        ) : (
          <div role="tabpanel">
            <ConfigSourceWorkspace
              value={workingYAML}
              error={sourceError}
              disabled={controlsDisabled}
              onChange={handleSourceChange}
            />
          </div>
        )}

        <div
          className="admin-config-actions"
          data-admin-floating-actions
          role="status"
          aria-live="polite"
        >
          <span className={`admin-config-actions__status ${statusClass}`}>{statusText}</span>
          <button
            type="button"
            className="admin-config-actions__button"
            onClick={resetDraft}
            disabled={controlsDisabled || !dirty}
            title="还原更改"
            aria-label="还原更改"
          >
            <RefreshCw size={16} aria-hidden="true" />
          </button>
          <button
            type="submit"
            className="admin-config-actions__button"
            disabled={
              controlsDisabled ||
              !dirty ||
              !timeValid ||
              !timezoneValid ||
              Boolean(sourceError)
            }
            title="预览并保存配置"
            aria-label="预览并保存配置"
          >
            {saving ? (
              <Loader2 size={16} className="admin-spin" aria-hidden="true" />
            ) : (
              <Check size={16} aria-hidden="true" />
            )}
            {dirty && <span className="admin-config-actions__dirty-dot" aria-hidden="true" />}
          </button>
        </div>
      </form>

      <ConfigDiffModal
        open={pendingSave !== null}
        before={pendingSave?.before ?? ""}
        after={pendingSave?.after ?? ""}
        saving={saving}
        onClose={() => setPendingSave(null)}
        onConfirm={() => void confirmSave()}
      />
    </>
  );
}
