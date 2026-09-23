import { useEffect, useState } from "react";
import { QrCode } from "lucide-react";
import * as api from "../api";
import { useToast } from "@/components/ToastContext";

export function P123QRCodeLogin({ onToken }: { onToken: (token: string) => void }) {
  const { show } = useToast();
  const [session, setSession] = useState<api.P123QRSession | null>(null);
  const [status, setStatus] = useState<api.P123QRStatus | null>(null);
  const [starting, setStarting] = useState(false);
  const [pollingError, setPollingError] = useState("");
  const [completed, setCompleted] = useState(false);

  async function start() {
    setStarting(true);
    setPollingError("");
    setCompleted(false);
    setStatus(null);
    try {
      const next = await api.startP123QRLogin();
      setSession(next);
    } catch (e) {
      setSession(null);
      show(e instanceof Error ? e.message : "生成二维码失败", "error");
    } finally {
      setStarting(false);
    }
  }

  useEffect(() => {
    if (!session || completed) return;
    const activeSession = session;
    let stopped = false;
    let inFlight = false;
    let timer: number | undefined;

    async function poll() {
      if (stopped || inFlight) return;
      inFlight = true;
      try {
        const next = await api.getP123QRStatus(activeSession.uniID, activeSession.loginUuid);
        if (stopped) return;
        setStatus(next);
        setPollingError("");
        if (next.accessToken) {
          stopped = true;
          if (timer) window.clearInterval(timer);
          setCompleted(true);
          onToken(next.accessToken);
          show("扫码成功，保存后生效", "success");
          return;
        }
        if (next.loginStatus === 2 || next.loginStatus === 4) {
          stopped = true;
          if (timer) window.clearInterval(timer);
        }
      } catch (e) {
        if (stopped) return;
        setPollingError(e instanceof Error ? e.message : "查询扫码状态失败");
      } finally {
        inFlight = false;
      }
    }

    poll();
    timer = window.setInterval(poll, 1800);
    return () => {
      stopped = true;
      if (timer) window.clearInterval(timer);
    };
  }, [session, completed, onToken, show]);

  return (
    <div className="admin-form__row">
      <label>方式一</label>
      <div className="admin-p123-qr">
        <div className="admin-p123-qr__actions">
          <button
            type="button"
            className="admin-btn"
            onClick={start}
            disabled={starting}
          >
            <QrCode size={14} />
            {starting ? "生成中..." : session ? "重新生成二维码" : "生成二维码"}
          </button>
        </div>

        {session && (
          <div className="admin-p123-qr__body">
            <img
              className="admin-p123-qr__image"
              src={session.qrImageDataUrl}
              alt="123网盘扫码登录二维码"
            />
            <div className="admin-p123-qr__meta">
              {pollingError && (
                <div className="admin-form__help">
                  {pollingError}
                </div>
              )}
              {session.expiresAt && (
                <div className="admin-form__help">
                  过期时间：{new Date(session.expiresAt).toLocaleTimeString("zh-CN", {
                    hour: "2-digit",
                    minute: "2-digit",
                    second: "2-digit",
                  })}
                </div>
              )}
              {(status?.loginStatus === 2 || status?.loginStatus === 4) && (
                <div className="admin-form__help">
                  当前二维码{status.loginStatus === 2 ? "已被拒绝" : "已过期"}，请重新生成。
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
