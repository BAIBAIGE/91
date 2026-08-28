import { ADMIN_PAGE_TITLES } from "./adminPageTitle";
import {
  loadBackupPage,
  loadCrawlersPage,
  loadDrivesPage,
  loadLogsPage,
  loadSettingsPage,
  loadTagsPage,
  loadUsersPage,
  loadVideosPage,
} from "./adminPageModules";

type AdminPagePath = (typeof ADMIN_PAGE_TITLES)[number]["path"];
type AdminPageModuleLoader = () => Promise<unknown>;

const loadersByPath = {
  "/admin/drives": loadDrivesPage,
  "/admin/crawlers": loadCrawlersPage,
  "/admin/videos": loadVideosPage,
  "/admin/tags": loadTagsPage,
  "/admin/users": loadUsersPage,
  "/admin/backup": loadBackupPage,
  "/admin/logs": loadLogsPage,
  "/admin/settings": loadSettingsPage,
} satisfies Record<AdminPagePath, AdminPageModuleLoader>;

const adminPageModules = ADMIN_PAGE_TITLES.map(({ path }) => ({
  path,
  load: loadersByPath[path],
}));

function scheduleIdle(task: () => void): () => void {
  if (typeof window.requestIdleCallback === "function") {
    const idleID = window.requestIdleCallback(task);
    return () => window.cancelIdleCallback(idleID);
  }

  const timeoutID = window.setTimeout(task, 250);
  return () => window.clearTimeout(timeoutID);
}

/**
 * Wait for the active page module, then fetch each remaining admin page during
 * a separate idle period. Loading is deliberately sequential so background
 * preparation does not open a burst of competing chunk requests.
 */
export function preloadRemainingAdminPageModules(activePath: string): () => void {
  const activeModule = adminPageModules.find(({ path }) => path === activePath);
  const queue = adminPageModules.filter(({ path }) => path !== activePath);
  let cancelled = false;
  let cancelScheduledIdle: (() => void) | undefined;

  const scheduleNext = () => {
    if (cancelled) return;
    const nextModule = queue.shift();
    if (!nextModule) return;

    cancelScheduledIdle = scheduleIdle(() => {
      cancelScheduledIdle = undefined;
      if (cancelled) return;
      void nextModule.load().catch(() => undefined).finally(scheduleNext);
    });
  };

  void (activeModule?.load() ?? Promise.resolve())
    .catch(() => undefined)
    .finally(scheduleNext);

  return () => {
    cancelled = true;
    cancelScheduledIdle?.();
  };
}
