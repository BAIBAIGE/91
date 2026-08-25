import { useLayoutEffect, useRef, useState } from "react";

type Props = {
  src: string;
  eager?: boolean;
  highPriority?: boolean;
  enabled?: boolean;
};

type ThumbnailState = "loading" | "ready" | "failed";

export function VideoThumbnail({
  src,
  eager = false,
  highPriority = false,
  enabled = true,
}: Props) {
  if (!enabled) {
    return (
      <span
        className="thumb-placeholder"
        data-state="deferred"
        aria-hidden="true"
      />
    );
  }

  // One component instance owns exactly one source lifecycle. This makes a src
  // change synchronous instead of resetting state later in an effect, which can
  // otherwise overwrite an already-fired load event from the browser cache.
  return (
    <ThumbnailResource
      key={src}
      src={src}
      eager={eager}
      highPriority={highPriority}
    />
  );
}

function ThumbnailResource({
  src,
  eager = false,
  highPriority = false,
}: Props) {
  const [state, setState] = useState<ThumbnailState>(src ? "loading" : "failed");
  const imageRef = useRef<HTMLImageElement | null>(null);

  function handleLoad() {
    setState("ready");
  }

  function handleError() {
    setState("failed");
  }

  // A cached image can finish between DOM insertion and React's passive effects,
  // and some browsers do not replay that load event. Reconcile the DOM state
  // before paint so a completed image can never remain hidden at opacity: 0.
  useLayoutEffect(() => {
    const image = imageRef.current;
    if (!image?.complete) return;
    if (image.naturalWidth > 0) {
      handleLoad();
    } else {
      handleError();
    }
  }, [src]);

  return (
    <>
      <span
        className="thumb-placeholder"
        data-state={state}
        aria-hidden="true"
      />
      {src && (
        <img
          ref={imageRef}
          className={`thumb-image ${state === "ready" ? "is-ready" : ""}`}
          src={src}
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
