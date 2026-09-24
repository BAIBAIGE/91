type VideoIdentity = { id: string };

export type VideoGridAnchor = {
  videos: readonly VideoIdentity[];
  index: number;
  viewportOffset: number;
};

/** Preserve the first visible card, or the next surviving neighbor. The saved
 * array is immutable and shared with the feed, so scrolling never copies it. */
export function resolveVideoGridAnchor(
  anchor: VideoGridAnchor,
  videos: readonly VideoIdentity[]
): { index: number; viewportOffset: number } | null {
  if (anchor.videos === videos || videos.length === 0) return null;
  const indices = new Map(videos.map((video, index) => [video.id, index]));
  if (anchor.videos.every((video) => indices.has(video.id))) return null;
  for (let index = anchor.index; index < anchor.videos.length; index += 1) {
    const nextIndex = indices.get(anchor.videos[index].id);
    if (nextIndex !== undefined) {
      return { index: nextIndex, viewportOffset: anchor.viewportOffset };
    }
  }
  for (let index = anchor.index - 1; index >= 0; index -= 1) {
    const nextIndex = indices.get(anchor.videos[index].id);
    if (nextIndex !== undefined) {
      return { index: nextIndex, viewportOffset: anchor.viewportOffset };
    }
  }
  return null;
}
