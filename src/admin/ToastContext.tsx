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
import { Toast, type ToastKind, type ToastMessage } from "./Toast";

type Ctx = {
  show: (text: string, kind?: ToastKind) => void;
};

const ToastCtx = createContext<Ctx | null>(null);
const TOAST_MAX_VISIBLE = 2;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastMessage[]>([]);
  const nextID = useRef(0);

  const show = useCallback((text: string, kind: ToastKind = "info") => {
    // Replacing repeated text also resets the card's timers and copy feedback.
    const toast = { id: ++nextID.current, kind, text };
    setItems((current) =>
      [...current.filter((item) => item.text !== text), toast].slice(-TOAST_MAX_VISIBLE)
    );
  }, []);

  const removeToast = useCallback((id: number) => {
    setItems((current) => current.filter((item) => item.id !== id));
  }, []);

  const contextValue = useMemo(() => ({ show }), [show]);

  return (
    <ToastCtx.Provider value={contextValue}>
      {children}
      {createPortal(
        <div className="admin-toast-stack" role="region" aria-label="通知" aria-live="polite">
          {items.map((toast) => (
            <Toast key={toast.id} toast={toast} onDismiss={removeToast} />
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
