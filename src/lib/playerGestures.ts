export type PlayerGestureFrame = {
  left: number;
  top: number;
  width: number;
  height: number;
  rotated: boolean;
};

export type PlayerGesturePoint = {
  x: number;
  y: number;
  width: number;
  height: number;
};

export function getPlayerGesturePoint(
  touch: { clientX: number; clientY: number },
  frame: PlayerGestureFrame
): PlayerGesturePoint {
  const x = touch.clientX - frame.left;
  const y = touch.clientY - frame.top;
  // Web fullscreen rotates the player clockwise; touch coordinates do not rotate.
  return frame.rotated
    ? { x: y, y: frame.width - x, width: frame.height, height: frame.width }
    : { x, y, width: frame.width, height: frame.height };
}

export function setPlayerGestureVolume(
  media: Pick<HTMLMediaElement, "volume" | "muted">,
  target: number
): number | null {
  if (!Number.isFinite(target)) return null;
  const volume = Math.round(Math.min(1, Math.max(0, target)) * 100) / 100;
  try {
    media.volume = volume;
    const actual = media.volume;
    // Some browsers accept the assignment without changing playback volume.
    if (!Number.isFinite(actual) || Math.abs(actual - volume) > 0.005) {
      return null;
    }
    media.muted = actual <= 0;
    return actual;
  } catch {
    return null;
  }
}
