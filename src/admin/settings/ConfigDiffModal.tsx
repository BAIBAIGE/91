import { useMemo } from "react";
import { FileText, Loader2 } from "lucide-react";
import { Modal } from "../Modal";
import { buildConfigDiff } from "./configDiff";

type Props = {
  open: boolean;
  before: string;
  after: string;
  saving: boolean;
  onClose: () => void;
  onConfirm: () => void;
};

const STAT_BLOCK_COUNT = 5;

function ChangeStatBar({ additions, deletions }: { additions: number; deletions: number }) {
  const total = additions + deletions;
  if (total === 0) return null;
  const additionBlocks = Math.round((additions / total) * STAT_BLOCK_COUNT);

  return (
    <span className="admin-config-diff-statbar" aria-hidden="true">
      {Array.from({ length: STAT_BLOCK_COUNT }, (_, index) => (
        <span
          key={index}
          className={index < additionBlocks ? "is-addition" : "is-deletion"}
        />
      ))}
    </span>
  );
}

export function ConfigDiffModal({
  open,
  before,
  after,
  saving,
  onClose,
  onConfirm,
}: Props) {
  const diff = useMemo(() => buildConfigDiff(before, after), [before, after]);
  const hasChanges = diff.additions + diff.deletions > 0;

  return (
    <Modal
      open={open}
      title="确认变更"
      onClose={saving ? () => undefined : onClose}
      className="admin-config-diff-modal"
      footer={
        <>
          <button type="button" className="admin-btn" onClick={onClose} disabled={saving}>
            返回修改
          </button>
          <button
            type="button"
            className="admin-btn is-primary"
            onClick={onConfirm}
            disabled={saving || !hasChanges}
          >
            {saving && <Loader2 size={14} className="admin-spin" aria-hidden="true" />}
            确认保存
          </button>
        </>
      }
    >
      {!hasChanges ? (
        <div className="admin-config-diff-empty">未检测到变更</div>
      ) : (
        <div
          className="admin-config-diff-file"
          role="region"
          aria-label="config.yaml 变更对比"
        >
          <div className="admin-config-diff-file__header">
            <FileText size={16} aria-hidden="true" />
            <span>config.yaml</span>
            <div
              className="admin-config-diff-stats"
              aria-label={`${diff.additions} 行新增，${diff.deletions} 行删除`}
            >
              <span className="is-addition">+{diff.additions}</span>
              <span className="is-deletion">-{diff.deletions}</span>
              <ChangeStatBar additions={diff.additions} deletions={diff.deletions} />
            </div>
          </div>

          <div className="admin-config-diff-body">
            {diff.hunks.map((hunk, hunkIndex) => (
              <div className="admin-config-diff-hunk" key={hunkIndex}>
                <div className="admin-config-diff-hunk__header">
                  <span aria-hidden="true" />
                  <span aria-hidden="true" />
                  <code>
                    @@ -{hunk.oldStart},{hunk.oldCount} +{hunk.newStart},{hunk.newCount} @@
                  </code>
                </div>
                {hunk.lines.map((line, lineIndex) => (
                  <div
                    className={`admin-config-diff-line is-${line.kind}`}
                    key={`${hunkIndex}-${lineIndex}`}
                  >
                    <span className="admin-config-diff-line__number">{line.oldLine ?? ""}</span>
                    <span className="admin-config-diff-line__number">{line.newLine ?? ""}</span>
                    <span className="admin-config-diff-line__prefix" aria-hidden="true">
                      {line.kind === "deletion" ? "-" : line.kind === "addition" ? "+" : " "}
                    </span>
                    <code>{line.content || " "}</code>
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </Modal>
  );
}
