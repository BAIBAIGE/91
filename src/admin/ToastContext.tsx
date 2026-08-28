import {
  ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { copyTextToClipboard } from "../lib/clipboard";

type ToastKind = "info" | "success" | "error";
type Toast = { id: number; kind: ToastKind; text: string; copyable: boolean };

type Ctx = {
  show: (text: string, kind?: ToastKind) => void;
};

const ToastCtx = createContext<Ctx | null>(null);
const TOAST_DISMISS_MS = 2500;
const TOAST_MAX_VISIBLE = 2;
const TOAST_COPY_SUCCESS_TEXT = "已复制到剪贴板";
const TOAST_COPY_ERROR_TEXT = "复制失败，请手动复制";

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);
  const itemsRef = useRef<Toast[]>([]);
  const timers = useRef(new Map<number, ReturnType<typeof window.setTimeout>>());
  const idsByText = useRef(new Map<string, number>());

  const clearDismissTimer = useCallback((id: number) => {
    const timer = timers.current.get(id);
    if (!timer) return;
    window.clearTimeout(timer);
    timers.current.delete(id);
  }, []);

  const setToastItems = useCallback((next: Toast[]) => {
    itemsRef.current = next;
    setItems(next);
  }, []);

  const forgetToast = useCallback(
    (toast: Toast) => {
      clearDismissTimer(toast.id);
      if (idsByText.current.get(toast.text) === toast.id) {
        idsByText.current.delete(toast.text);
      }
    },
    [clearDismissTimer]
  );

  const removeToast = useCallback(
    (id: number, text: string) => {
      clearDismissTimer(id);
      if (idsByText.current.get(text) === id) {
        idsByText.current.delete(text);
      }
      setToastItems(itemsRef.current.filter((t) => t.id !== id));
    },
    [clearDismissTimer, setToastItems]
  );

  const scheduleDismiss = useCallback(
    (id: number, text: string) => {
      clearDismissTimer(id);
      timers.current.set(
        id,
        window.setTimeout(() => removeToast(id, text), TOAST_DISMISS_MS)
      );
    },
    [clearDismissTimer, removeToast]
  );

  const addToast = useCallback(
    (text: string, kind: ToastKind = "info", copyable = true) => {
      const existingID = idsByText.current.get(text);
      const id = existingID ?? Date.now() + Math.random();
      const toast = { id, kind, text, copyable };
      const withoutExisting =
        existingID === undefined
          ? itemsRef.current
          : itemsRef.current.filter((t) => t.id !== existingID);
      const withNewToast = [...withoutExisting, toast];
      const visible = withNewToast.slice(-TOAST_MAX_VISIBLE);
      const evicted = withNewToast.slice(
        0,
        Math.max(0, withNewToast.length - TOAST_MAX_VISIBLE)
      );

      for (const item of evicted) {
        forgetToast(item);
      }

      idsByText.current.set(text, id);
      setToastItems(visible);
      scheduleDismiss(id, text);
    },
    [forgetToast, scheduleDismiss, setToastItems]
  );

  // Keep only the two newest toasts; repeated text is refreshed and moved last.
  const show = useCallback(
    (text: string, kind: ToastKind = "info") => {
      addToast(text, kind, true);
    },
    [addToast]
  );

  const copyToastText = useCallback(
    (text: string) => {
      void copyTextToClipboard(text).then((copied) => {
        if (copied) {
          addToast(TOAST_COPY_SUCCESS_TEXT, "success", false);
          return;
        }
        addToast(TOAST_COPY_ERROR_TEXT, "error", false);
      });
    },
    [addToast]
  );

  useEffect(() => {
    return () => {
      for (const timer of timers.current.values()) {
        window.clearTimeout(timer);
      }
      timers.current.clear();
      idsByText.current.clear();
      itemsRef.current = [];
    };
  }, []);

  const contextValue = useMemo(() => ({ show }), [show]);

  return (
    <ToastCtx.Provider value={contextValue}>
      {children}
      {createPortal(
        <div className="admin-toast-stack" role="status" aria-live="polite">
          {items.map((t) => (
            <div
              key={t.id}
              className={`admin-toast is-${t.kind}${
                t.copyable ? " is-copyable" : ""
              }`}
              role={t.copyable ? "button" : undefined}
              tabIndex={t.copyable ? 0 : undefined}
              aria-label={t.copyable ? `复制提示：${t.text}` : undefined}
              onClick={t.copyable ? () => copyToastText(t.text) : undefined}
              onKeyDown={
                t.copyable
                  ? (event) => {
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      copyToastText(t.text);
                    }
                  : undefined
              }
            >
              <span className="admin-toast__text">{t.text}</span>
            </div>
          ))}
        </div>,
        document.body
      )}
    </ToastCtx.Provider>
  );
}

export function useToast(): Ctx {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error("useToast must be used inside <ToastProvider>");
  return ctx;
}

// 小工具：自动关闭的 toast 倒计时，用于某些异步提示展示后返回
export function useFlashError(): [string | null, (msg: string | null) => void] {
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    if (!err) return;
    const t = window.setTimeout(() => setErr(null), 4000);
    return () => window.clearTimeout(t);
  }, [err]);
  return [err, setErr];
}
