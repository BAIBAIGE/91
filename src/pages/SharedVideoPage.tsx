import { useEffect, useState } from "react";
import { useLocation } from "react-router";
import linkUsedImage from "@/assets/share-link-used.webp";
import { VideoInfoPanel } from "@/components/VideoInfoPanel";
import { VideoMetaHeader } from "@/components/VideoMetaHeader";
import { VideoPlayer } from "@/components/VideoPlayer";
import {
  consumeVideoShare,
  fetchSharedVideoSubtitles,
  recordSharedVideoView,
  VideoShareUnavailableError,
  type VideoShareClaim,
} from "@/data/videos";

type LoadState = "loading" | "ready" | "unavailable" | "error";

export default function SharedVideoPage() {
  const location = useLocation();
  const token = location.hash.startsWith("#") ? location.hash.slice(1) : "";
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [claim, setClaim] = useState<VideoShareClaim | null>(null);

  useEffect(() => {
    let active = true;
    window.scrollTo({ top: 0, behavior: "auto" });
    setClaim(null);
    setLoadState("loading");

    if (!token) {
      setLoadState("unavailable");
      document.title = "分享链接已失效";
      return () => {
        active = false;
      };
    }

    consumeVideoShare(token)
      .then((result) => {
        if (!active) return;
        window.history.replaceState(
          window.history.state,
          "",
          `${window.location.pathname}${window.location.search}`
        );
        setClaim(result);
        setLoadState("ready");
        document.title = `${result.video.title} - 视频分享`;
      })
      .catch((error: unknown) => {
        if (!active) return;
        if (error instanceof VideoShareUnavailableError) {
          setLoadState("unavailable");
          document.title = "分享链接已失效";
          return;
        }
        setLoadState("error");
        document.title = "分享视频加载失败";
      });

    return () => {
      active = false;
    };
  }, [token]);

  function handleFirstPlay() {
    if (!claim) return;
    recordSharedVideoView(claim.shareId).catch(() => undefined);
  }

  function loadSubtitles() {
    if (!claim) return Promise.resolve([]);
    return fetchSharedVideoSubtitles(claim.shareId);
  }

  return (
    <div className="share-page">
      <header className="share-page__header">
        <div className="container share-page__header-inner">
          <div className="share-page__brand" aria-label="91">
            <img src="/icon.png" alt="" />
          </div>
        </div>
      </header>

      <main
        className={`container share-page__main${
          loadState === "loading" ||
          loadState === "unavailable" ||
          loadState === "error"
            ? " share-page__main--centered"
            : ""
        }`}
      >
        {loadState === "loading" && (
          <div
            className="share-page__state share-page__state--bare"
            aria-busy="true"
            aria-label="正在加载"
          >
            <span className="share-page__spinner" aria-hidden="true" />
          </div>
        )}

        {loadState === "unavailable" && (
          <div
            className="share-page__state share-page__state--bare"
            role="alert"
          >
            <img
              className="share-page__state-image"
              src={linkUsedImage}
              alt=""
              aria-hidden="true"
            />
            <p className="share-page__state-message">当前链接已失效</p>
          </div>
        )}

        {loadState === "error" && (
          <div
            className="share-page__state share-page__state--bare"
            role="alert"
          >
            <img
              className="share-page__state-image"
              src={linkUsedImage}
              alt=""
              aria-hidden="true"
            />
            <h1>暂时无法加载</h1>
          </div>
        )}

        {loadState === "ready" && claim && (
          <article className="share-page__video">
            <div className="vd-player-wrap">
              <div className="vd-player">
                <VideoPlayer
                  id={claim.video.id}
                  src={claim.video.videoSrc}
                  poster={claim.video.poster}
                  previewSrc={claim.video.previewSrc}
                  loadSubtitles={loadSubtitles}
                  title={claim.video.title}
                  onFirstPlay={handleFirstPlay}
                />
              </div>
            </div>

            <section className="vd-summary" aria-label="分享的视频">
              <VideoMetaHeader video={claim.video} />
            </section>

            <VideoInfoPanel video={claim.video} />
          </article>
        )}
      </main>

      <footer className="share-page__footer">
        <a
          href="https://github.com/nianzhibai/91"
          target="_blank"
          rel="noopener noreferrer"
        >
          © {new Date().getFullYear()} 91
        </a>
      </footer>
    </div>
  );
}
