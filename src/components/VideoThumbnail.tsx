import { useEffect, useRef, useState } from "react";

type Props = {
  src: string;
  eager?: boolean;
  highPriority?: boolean;
};

type ThumbnailState = "loading" | "retrying" | "ready" | "failed";

const MAX_LOCAL_THUMBNAIL_RETRIES = 8;

export function VideoThumbnail({
  src,
  eager = false,
  highPriority = false,
}: Props) {
  const [state, setState] = useState<ThumbnailState>(src ? "loading" : "failed");
  const [retry, setRetry] = useState(0);
  const retryTimerRef = useRef<number | null>(null);

  useEffect(() => {
    setRetry(0);
    setState(src ? "loading" : "failed");
    clearRetryTimer();
    return clearRetryTimer;
  }, [src]);

  function clearRetryTimer() {
    if (retryTimerRef.current !== null) {
      window.clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  }

  function handleLoad() {
    clearRetryTimer();
    setState("ready");
  }

  function handleError() {
    const canRetry =
      src.startsWith("/p/thumb/") && retry < MAX_LOCAL_THUMBNAIL_RETRIES;
    if (!canRetry) {
      clearRetryTimer();
      setState("failed");
      return;
    }
    if (retryTimerRef.current !== null) return;

    setState("retrying");
    retryTimerRef.current = window.setTimeout(() => {
      retryTimerRef.current = null;
      setRetry((current) => current + 1);
    }, Math.min(1000 + retry * 750, 5000));
  }

  const thumbnailSrc = retry === 0 ? src : withRetryParam(src, retry);

  return (
    <>
      <span
        className="thumb-placeholder"
        data-state={state}
        aria-hidden="true"
      >
        <span className="thumb-placeholder__mark" />
      </span>
      {src && (
        <img
          key={thumbnailSrc}
          className={`thumb-image ${state === "ready" ? "is-ready" : ""}`}
          src={thumbnailSrc}
          alt=""
          loading={eager || highPriority ? "eager" : "lazy"}
          fetchPriority={highPriority ? "high" : "auto"}
          decoding="async"
          onLoad={handleLoad}
          onError={handleError}
        />
      )}
    </>
  );
}

function withRetryParam(src: string, retry: number): string {
  const sep = src.includes("?") ? "&" : "?";
  return `${src}${sep}r=${retry}`;
}
