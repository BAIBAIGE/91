import type { VideoCollection } from "@/types";

// Confirmed deletions live for this document's lifetime. Keeping them after a
// cache eviction also prevents requests started before deletion reviving a card.
let deletedVideoIDs: ReadonlySet<string> = new Set();
const listeners = new Set<(id: string) => void>();

export function getDeletedVideoIDs(): ReadonlySet<string> {
  return deletedVideoIDs;
}

export function isVideoDeleted(id: string): boolean {
  return deletedVideoIDs.has(id);
}

export function subscribeVideoDeletions(listener: (id: string) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function markVideoDeleted(id: string) {
  if (deletedVideoIDs.has(id)) return;
  deletedVideoIDs = new Set([...deletedVideoIDs, id]);
  for (const listener of listeners) listener(id);
}

export function filterDeletedVideos<T extends { id: string }>(
  videos: T[],
  deleted: ReadonlySet<string> = deletedVideoIDs
): T[] {
  return videos.some((video) => deleted.has(video.id))
    ? videos.filter((video) => !deleted.has(video.id))
    : videos;
}

export function filterDeletedCollection(
  collection: VideoCollection,
  deleted: ReadonlySet<string> = deletedVideoIDs
): VideoCollection {
  const items = filterDeletedVideos(collection.items, deleted);
  if (items === collection.items) return collection;
  const currentID = collection.items[collection.currentIndex - 1]?.id;
  return {
    ...collection,
    items,
    total: items.length,
    currentIndex: items.findIndex((item) => item.id === currentID) + 1,
  };
}
