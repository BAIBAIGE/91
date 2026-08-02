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
  Search,
  SlidersHorizontal,
} from "lucide-react";
import { parseDocument, type Document } from "yaml";
import * as api from "./api";
import { AdminLoading } from "./AdminLoading";
import { useToast } from "./ToastContext";
import { ConfigDiffModal } from "./settings/ConfigDiffModal";
import { SettingsRow, SettingsSection } from "./settings/SettingsSection";

type SettingsDraft = {
  nightlyStartTime: string;
};

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
type SectionID = "config-automation";
type VisualField = keyof SettingsDraft;

const DEFAULT_DRAFT: SettingsDraft = {
  nightlyStartTime: "01:00",
};

const SECTION_META: Array<{
  id: SectionID;
  index: string;
  title: string;
  keywords: string;
}> = [
  {
    id: "config-automation",
    index: "01",
    title: "自动任务",
    keywords: "自动任务 定时任务 每日启动时间 nightly start time 调度",
  },
];

function isValidStartTime(value: string) {
  if (!/^\d{2}:\d{2}$/.test(value)) return false;
  const [hour, minute] = value.split(":").map(Number);
  return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59;
}

function configDocument(source: string): Document {
  const document = parseDocument(source, { prettyErrors: true });
  if (document.errors.length > 0) {
    throw new Error(document.errors[0].message);
  }
  const root = document.toJS();
  if (root !== null && (typeof root !== "object" || Array.isArray(root))) {
    throw new Error("config.yaml 顶层必须是映射对象");
  }
  return document;
}

function draftFromDocument(document: Document): SettingsDraft {
  const configuredStart = document.getIn(["nightly", "start_time"]);
  let nightlyStartTime = DEFAULT_DRAFT.nightlyStartTime;
  if (configuredStart !== undefined && configuredStart !== null) {
    if (typeof configuredStart !== "string" || !isValidStartTime(configuredStart)) {
      throw new Error("nightly.start_time 必须是 HH:mm 格式的有效时间");
    }
    nightlyStartTime = configuredStart;
  } else {
    const legacyHour = document.getIn(["nightly", "cron_hour"]);
    if (typeof legacyHour === "number" && legacyHour >= 1 && legacyHour <= 23) {
      nightlyStartTime = `${String(legacyHour).padStart(2, "0")}:00`;
    }
  }
  return { nightlyStartTime };
}

function parseConfig(source: string) {
  const document = configDocument(source);
  return { document, draft: draftFromDocument(document) };
}

function stringifyConfig(document: Document) {
  return document.toString({ lineWidth: 0 });
}

function applyVisualFields(
  source: string,
  draft: SettingsDraft,
  fields: ReadonlySet<VisualField>
) {
  const document = configDocument(source);
  if (fields.has("nightlyStartTime")) {
    document.setIn(["nightly", "start_time"], draft.nightlyStartTime);
    document.deleteIn(["nightly", "cron_hour"]);
  }
  return stringifyConfig(document);
}

function changedVisualFields(saved: SettingsDraft, draft: SettingsDraft) {
  const fields = new Set<VisualField>();
  if (saved.nightlyStartTime !== draft.nightlyStartTime) {
    fields.add("nightlyStartTime");
  }
  return fields;
}

export function SettingsPage() {
  const { show } = useToast();
  const sourceGutterRef = useRef<HTMLDivElement>(null);
  const [loaded, setLoaded] = useState<LoadedConfig | null>(null);
  const [draft, setDraft] = useState<SettingsDraft>(DEFAULT_DRAFT);
  const [workingYAML, setWorkingYAML] = useState("");
  const [sourceTouched, setSourceTouched] = useState(false);
  const [activeTab, setActiveTab] = useState<EditorTab>("visual");
  const [activeSection, setActiveSection] = useState<SectionID>("config-automation");
  const [searchQuery, setSearchQuery] = useState("");
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
  const timeValid = isValidStartTime(draft.nightlyStartTime);
  const normalizedSearch = searchQuery.trim().toLocaleLowerCase();
  const visibleSections = useMemo(
    () =>
      normalizedSearch
        ? SECTION_META.filter((section) =>
            `${section.title} ${section.keywords}`.toLocaleLowerCase().includes(normalizedSearch)
          )
        : SECTION_META,
    [normalizedSearch]
  );

  async function load() {
    setLoading(true);
    setLoadError("");
    try {
      const next = await api.getConfigYAML();
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
    } catch (error) {
      const message = error instanceof Error ? error.message : "加载配置失败";
      setLoadError(message);
      show(message, "error");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    document.title = "配置管理";
    void load();
  }, []);

  useEffect(() => {
    if (!dirty) return;
    const warnBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warnBeforeUnload);
    return () => window.removeEventListener("beforeunload", warnBeforeUnload);
  }, [dirty]);

  useEffect(() => {
    const firstVisible = visibleSections[0];
    if (!firstVisible) return;
    setActiveSection(firstVisible.id);
  }, [visibleSections]);

  function candidateForLatest(latest: api.ConfigYAMLDocument) {
    if (!loaded) throw new Error("配置尚未加载");
    if (sourceTouched) return workingYAML;
    return applyVisualFields(latest.content, draft, visualDirtyFields);
  }

  async function prepareSave(event: FormEvent) {
    event.preventDefault();
    if (!loaded || !dirty || !timeValid || sourceError || saving) return;
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
      setLoaded({ content: pendingSave.after, version: response.version, visual });
      setWorkingYAML(pendingSave.after);
      setDraft(visual);
      setSourceTouched(false);
      setSourceError("");
      setPendingSave(null);
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

  if (loading) return <AdminLoading />;

  if (loadError || !loaded) {
    return (
      <div className="admin-config-page admin-config-page--error">
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
      <form className="admin-config-page" onSubmit={prepareSave}>
        <header className="admin-config-header">
          <h1>配置管理</h1>
          <div className="admin-config-tabs" role="tablist" aria-label="配置编辑模式">
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "visual"}
              className={activeTab === "visual" ? "is-active" : ""}
              onClick={() => handleTabChange("visual")}
            >
              可视化编辑
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={activeTab === "source"}
              className={activeTab === "source" ? "is-active" : ""}
              onClick={() => handleTabChange("source")}
            >
              源码编辑
            </button>
          </div>
        </header>

        {activeTab === "visual" ? (
          <div className="admin-config-visual" role="tabpanel">
            <div className="admin-config-search">
              <label htmlFor="config-search" className="sr-only">
                搜索配置项
              </label>
              <input
                id="config-search"
                type="search"
                value={searchQuery}
                placeholder="搜索配置项..."
                onChange={(event) => setSearchQuery(event.target.value)}
              />
              <Search size={16} aria-hidden="true" />
            </div>

            {visibleSections.length > 0 ? (
              <>
                <nav
                  className="admin-config-section-nav"
                  role="tablist"
                  aria-label="配置分组"
                >
                  {visibleSections.map((section) => {
                    const Icon = Clock3;
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
                  {activeSection === "config-automation" &&
                    visibleSections.some((section) => section.id === "config-automation") && (
                    <SettingsSection
                      id="config-automation"
                      index="01"
                      icon={<Clock3 size={16} />}
                      title="自动任务"
                      description="控制每日维护流水线的自动执行时间。"
                    >
                      <SettingsRow
                        label="每日启动时间"
                        htmlFor="nightly-start-time"
                        description="按服务器本地时区执行，每天最多自动触发一次；保存后无需重启。"
                      >
                        <div className="admin-config-control admin-config-control--time">
                          <input
                            id="nightly-start-time"
                            type="time"
                            step={60}
                            value={draft.nightlyStartTime}
                            aria-invalid={!timeValid}
                            aria-describedby="nightly-start-time-hint"
                            onChange={(event) =>
                              updateVisualField("nightlyStartTime", event.target.value)
                            }
                          />
                          <span
                            id="nightly-start-time-hint"
                            className={!timeValid ? "is-error" : undefined}
                          >
                            {timeValid ? "24 小时制 · HH:mm" : "请选择有效时间"}
                          </span>
                        </div>
                      </SettingsRow>
                    </SettingsSection>
                  )}
                </div>
              </>
            ) : (
              <div className="admin-config-search-empty">
                <Search size={20} aria-hidden="true" />
                <strong>没有匹配的配置项</strong>
                <span>换一个关键词试试</span>
              </div>
            )}
          </div>
        ) : (
          <div className="admin-config-source" role="tabpanel">
            <div className="admin-config-source__toolbar">
              <span>config.yaml</span>
              <small>真实配置文件 · 未知字段与注释会保留</small>
            </div>
            <div className={`admin-config-source__editor ${sourceError ? "has-error" : ""}`}>
              <div
                ref={sourceGutterRef}
                className="admin-config-source__gutter"
                aria-hidden="true"
              >
                {workingYAML.split("\n").map((_, index) => (
                  <span key={index}>{index + 1}</span>
                ))}
              </div>
              <textarea
                aria-label="config.yaml 源码"
                value={workingYAML}
                spellCheck={false}
                onChange={(event) => handleSourceChange(event.target.value)}
                onScroll={(event) => {
                  if (sourceGutterRef.current) {
                    sourceGutterRef.current.style.transform = `translateY(-${event.currentTarget.scrollTop}px)`;
                  }
                }}
              />
            </div>
            <p className={`admin-config-source__hint ${sourceError ? "is-error" : ""}`}>
              {sourceError || "保存时会使用与服务启动相同的解析规则校验完整 YAML。"}
            </p>
          </div>
        )}

        <div className="admin-config-actions" role="status" aria-live="polite">
          <span
            className={`admin-config-actions__status ${
              sourceError ? "is-error" : dirty ? "is-dirty" : "is-saved"
            }`}
          >
            {sourceError ? "配置有误" : saving ? "处理中" : dirty ? "有未保存更改" : "配置已加载"}
          </span>
          <button
            type="button"
            className="admin-config-actions__button"
            onClick={resetDraft}
            disabled={saving || !dirty}
            title="还原更改"
            aria-label="还原更改"
          >
            <RefreshCw size={16} aria-hidden="true" />
          </button>
          <button
            type="submit"
            className="admin-config-actions__button"
            disabled={saving || !dirty || !timeValid || Boolean(sourceError)}
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
