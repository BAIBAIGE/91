import { useCallback, useEffect, useState } from "react";
import { ChevronRight, FolderX } from "lucide-react";
import * as api from "../api";
import { useToast } from "../ToastContext";

type SkipDirsPanelProps = {
  drive: api.AdminDrive;
  onSaved: (saved: { id: string; skipDirIds: string[] }) => void;
};

export function SkipDirsPanel({ drive, onSaved }: SkipDirsPanelProps) {
  const { show } = useToast();
  const [selected, setSelected] = useState<Set<string>>(
    () => new Set(drive.skipDirIds ?? [])
  );
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setSelected(new Set(drive.skipDirIds ?? []));
  }, [drive.id, drive.skipDirIds]);

  const toggle = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  async function handleSave() {
    setSaving(true);
    try {
      const ids = Array.from(selected);
      const resp = await api.setDriveSkipDirIds(drive.id, ids);
      onSaved({ id: drive.id, skipDirIds: resp.skipDirIds });
    } catch (e) {
      show(e instanceof Error ? e.message : "保存失败", "error");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="admin-detail-card">
      <header className="admin-detail-card__title">
        <div className="admin-detail-card__title-left">
          <FolderX size={16} />
          <span>扫描跳过目录</span>
        </div>
        <button
          className="admin-btn"
          onClick={handleSave}
          disabled={saving}
          style={{ padding: "4px 10px", fontSize: "12px", height: "auto" }}
        >
          {saving ? "保存中..." : "保存更改"}
        </button>
      </header>

      <div className="admin-detail-tree-container">
        <DirTreeNode
          driveId={drive.id}
          id=""
          name={drive.name || "存储"}
          depth={0}
          initiallyOpen
          ancestorSkipped={false}
          selected={selected}
          onToggle={toggle}
        />
      </div>
    </div>
  );
}

type DirTreeNodeProps = {
  driveId: string;
  id: string;
  name: string;
  depth: number;
  initiallyOpen?: boolean;
  ancestorSkipped: boolean;
  selected: Set<string>;
  onToggle: (id: string) => void;
};

function DirTreeNode({
  driveId,
  id,
  name,
  depth,
  initiallyOpen,
  ancestorSkipped,
  selected,
  onToggle,
}: DirTreeNodeProps) {
  const [open, setOpen] = useState(!!initiallyOpen);
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [children, setChildren] = useState<api.DriveDirEntry[]>([]);
  const [error, setError] = useState("");

  const isRoot = depth === 0;
  const isSelected = id !== "" && selected.has(id);
  const dimmed = ancestorSkipped;

  const loadChildren = useCallback(async () => {
    if (loaded || loading) return;
    setLoading(true);
    setError("");
    try {
      const data = await api.listDriveDirChildren(driveId, id || undefined);
      setChildren(data ?? []);
      setLoaded(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "加载失败");
    } finally {
      setLoading(false);
    }
  }, [driveId, id, loaded, loading]);

  useEffect(() => {
    if (open && !loaded) {
      void loadChildren();
    }
  }, [open, loaded, loadChildren]);

  function handleToggleOpen() {
    setOpen((v) => !v);
  }

  return (
    <div>
      {!isRoot && (
        <div
          className={`admin-skipdirs-row${dimmed && !isSelected ? " is-dimmed" : ""}`}
        >
          <button
            type="button"
            onClick={handleToggleOpen}
            className={`admin-skipdirs-toggle${open ? " is-open" : ""}`}
            aria-label={open ? "折叠" : "展开"}
            aria-expanded={open}
          >
            <ChevronRight size={14} />
          </button>

          <input
            type="checkbox"
            className="admin-skipdirs-checkbox"
            checked={isSelected}
            onChange={() => onToggle(id)}
            aria-label={`跳过目录 ${name}`}
          />

          <span className="admin-skipdirs-name" title={name} onClick={handleToggleOpen}>
            {name}
          </span>

          {isSelected && <span className="admin-skipdirs-flag">已跳过</span>}
        </div>
      )}

      {open && (
        <div className={isRoot ? undefined : "admin-skipdirs-children"}>
          {loading && (
            <div className="admin-skipdirs-status">
              <span className="lds-ellipsis is-xs" aria-hidden="true">
                <span />
                <span />
                <span />
                <span />
              </span>
              加载中
            </div>
          )}
          {error && <div className="admin-skipdirs-status is-error">{error}</div>}
          {loaded && !error && children.length === 0 && (
            <div className="admin-skipdirs-status">（无子目录）</div>
          )}
          {children.map((child) => (
            <DirTreeNode
              key={child.id}
              driveId={driveId}
              id={child.id}
              name={child.name}
              depth={depth + 1}
              ancestorSkipped={ancestorSkipped || isSelected}
              selected={selected}
              onToggle={onToggle}
            />
          ))}
        </div>
      )}
    </div>
  );
}
