import { previewController } from "./previewController";

const REFRESH_INTERVAL_MS = 15_000;
const REFRESH_COOLDOWN_MS = 2_000;

let revision = 0;

export function applyPreviewEnabled(enabled: boolean): void {
  revision += 1;
  previewController.setEnabled(enabled);
}

export async function syncPreviewSettings(signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) return;
  const requestRevision = ++revision;
  const controller = new AbortController();
  const cancel = () => controller.abort();
  signal?.addEventListener("abort", cancel, { once: true });
  const timeout = setTimeout(() => controller.abort(), 10_000);
  try {
    const response = await fetch("/api/settings/preview", {
      credentials: "include",
      cache: "no-store",
      signal: controller.signal,
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const data: { previewEnabled?: unknown } = await response.json();
    if (requestRevision === revision && !signal?.aborted) {
      previewController.setEnabled(data.previewEnabled === true);
    }
  } catch {
    if (requestRevision === revision && !signal?.aborted) {
      previewController.setEnabled(false);
    }
  } finally {
    clearTimeout(timeout);
    signal?.removeEventListener("abort", cancel);
  }
}

// The route owns one watcher, independently of how many preview cards it renders.
export function watchPreviewSettings(): () => void {
  let stopped = false;
  let timer: number | undefined;
  let activeRequest: AbortController | null = null;
  let lastRefreshAt = -Infinity;

  function schedule(delay: number) {
    window.clearTimeout(timer);
    timer = undefined;
    if (stopped || document.visibilityState === "hidden") return;
    timer = window.setTimeout(() => void refresh(), delay);
  }

  async function refresh() {
    timer = undefined;
    if (stopped || document.visibilityState === "hidden" || activeRequest) return;
    const elapsed = Date.now() - lastRefreshAt;
    if (elapsed < REFRESH_COOLDOWN_MS) {
      schedule(REFRESH_INTERVAL_MS - elapsed);
      return;
    }

    const request = new AbortController();
    activeRequest = request;
    await syncPreviewSettings(request.signal);
    if (stopped) return;
    activeRequest = null;
    lastRefreshAt = Date.now();
    schedule(REFRESH_INTERVAL_MS);
  }

  // Queue lifecycle events together, including React's setup/cleanup replay.
  const requestRefresh = () => schedule(0);
  requestRefresh();
  window.addEventListener("focus", requestRefresh);
  document.addEventListener("visibilitychange", requestRefresh);
  return () => {
    stopped = true;
    window.clearTimeout(timer);
    window.removeEventListener("focus", requestRefresh);
    document.removeEventListener("visibilitychange", requestRefresh);
    activeRequest?.abort();
  };
}
