import { useCallback, useEffect, useRef, useState } from "react";
import { AlertCircle, CheckCircle2, Info, X } from "lucide-react";
import { copyTextToClipboard } from "../lib/clipboard";

export type ToastKind = "info" | "success" | "error";
export type ToastMessage = { id: number; kind: ToastKind; text: string };

const DISMISS_MS: Record<ToastKind, number> = {
  success: 3000,
  info: 4000,
  error: 6000,
};
const EXIT_MS = 180;
const COPY_FEEDBACK_MS = 1500;
const ICONS = { info: Info, success: CheckCircle2, error: AlertCircle };

export function Toast({
  toast,
  onDismiss,
}: {
  toast: ToastMessage;
  onDismiss: (id: number) => void;
}) {
  const [isLeaving, setIsLeaving] = useState(false);
  const [copyResult, setCopyResult] = useState<{ copied: boolean } | null>(null);
  const copyRequest = useRef(0);
  const Icon = ICONS[toast.kind];

  const dismiss = useCallback(() => {
    copyRequest.current += 1;
    setIsLeaving(true);
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(dismiss, DISMISS_MS[toast.kind]);
    return () => {
      window.clearTimeout(timer);
      // Ignore clipboard work that finishes after this card has been replaced.
      copyRequest.current += 1;
    };
  }, [dismiss, toast.kind]);

  useEffect(() => {
    if (!isLeaving) return;
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const timer = window.setTimeout(() => onDismiss(toast.id), reducedMotion ? 0 : EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [isLeaving, onDismiss, toast.id]);

  useEffect(() => {
    if (!copyResult) return;
    const timer = window.setTimeout(() => setCopyResult(null), COPY_FEEDBACK_MS);
    return () => window.clearTimeout(timer);
  }, [copyResult]);

  async function copyText() {
    const request = ++copyRequest.current;
    const copied = await copyTextToClipboard(toast.text);
    if (request === copyRequest.current) setCopyResult({ copied });
  }

  return (
    <div
      className={`toast is-${toast.kind}${isLeaving ? " is-leaving" : ""}`}
      aria-hidden={isLeaving || undefined}
    >
      <Icon className="toast__icon" size={18} aria-hidden="true" />
      <div className="toast__content">
        <button
          type="button"
          className="toast__copy"
          aria-label={`复制提示：${toast.text}`}
          title="点击复制提示"
          disabled={isLeaving}
          onClick={() => void copyText()}
        >
          <span className="toast__text" role={toast.kind === "error" ? "alert" : undefined}>
            {toast.text}
          </span>
        </button>
        <span
          className={`toast__feedback${copyResult ? (copyResult.copied ? " is-success" : " is-error") : ""}`}
          role="status"
          aria-live="polite"
        >
          {copyResult && (copyResult.copied ? "已复制" : "复制失败，请手动复制")}
        </span>
      </div>
      <button
        type="button"
        className="toast__close"
        aria-label="关闭提示"
        disabled={isLeaving}
        onClick={dismiss}
      >
        <X size={16} aria-hidden="true" />
      </button>
    </div>
  );
}
