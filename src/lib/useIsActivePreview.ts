import { useSyncExternalStore } from "react";
import { previewController } from "@/lib/previewController";

/** Subscribes one card to the shared single-active-preview controller. */
export function useIsActivePreview(videoID: string): boolean {
  return useSyncExternalStore(
    previewController.subscribe,
    () => previewController.getActiveId() === videoID,
    () => false
  );
}
