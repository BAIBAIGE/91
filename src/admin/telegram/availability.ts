type TelegramAvailability = {
  enabled: boolean | null;
  error: string;
};

let snapshot: TelegramAvailability = { enabled: null, error: "" };
let revision = 0;
const listeners = new Set<() => void>();

export const getTelegramAvailability = () => snapshot;

export function subscribeTelegramAvailability(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function publish(next: TelegramAvailability) {
  snapshot = next;
  listeners.forEach((listener) => listener());
}

export function applyTelegramEnabled(enabled: boolean) {
  revision += 1;
  publish({ enabled, error: "" });
}

export function resetTelegramAvailability() {
  revision += 1;
  publish({ enabled: null, error: "" });
}

export async function syncTelegramAvailability(
  fetchEnabled: () => Promise<boolean>,
  signal: AbortSignal,
) {
  const requestRevision = ++revision;
  try {
    const enabled = await fetchEnabled();
    if (signal.aborted || requestRevision !== revision) return;
    if (typeof enabled !== "boolean")
      throw new Error("无效的 Telegram 配置状态");
    publish({ enabled, error: "" });
  } catch {
    if (signal.aborted || requestRevision !== revision) return;
    publish({ ...snapshot, error: "无法读取 Telegram 配置状态，请刷新重试。" });
  }
}
