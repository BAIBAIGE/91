import { useEffect, useSyncExternalStore } from "react";
import * as api from "../api";
import {
  getTelegramAvailability,
  resetTelegramAvailability,
  subscribeTelegramAvailability,
  syncTelegramAvailability,
} from "./availability";

export function useTelegramAvailability() {
  return useSyncExternalStore(
    subscribeTelegramAvailability,
    getTelegramAvailability,
    getTelegramAvailability,
  );
}

// The admin layout owns synchronization for navigation and retained pages.
export function useSyncTelegramAvailability(pathname: string) {
  useEffect(() => () => resetTelegramAvailability(), []);

  useEffect(() => {
    let request: AbortController | undefined;
    const refresh = () => {
      if (document.visibilityState === "hidden") return;
      request?.abort();
      request = new AbortController();
      const signal = request.signal;
      void syncTelegramAvailability(
        async () => (await api.getTelegramStatus(signal)).enabled,
        signal,
      );
    };
    refresh();
    const timer = window.setInterval(refresh, 15_000);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      request?.abort();
      window.clearInterval(timer);
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [pathname]);
}
