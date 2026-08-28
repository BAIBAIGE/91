type AdminPageModuleLoader<T> = () => Promise<T>;

function reusableModuleLoader<T>(
  importModule: AdminPageModuleLoader<T>
): AdminPageModuleLoader<T> {
  let request: Promise<T> | undefined;

  return () => {
    request ??= importModule().catch((error: unknown) => {
      request = undefined;
      throw error;
    });
    return request;
  };
}

export const loadDrivesPage = reusableModuleLoader(() => import("./DrivesPage"));
export const loadCrawlersPage = reusableModuleLoader(
  () => import("./CrawlersPage")
);
export const loadVideosPage = reusableModuleLoader(() => import("./VideosPage"));
export const loadTagsPage = reusableModuleLoader(() => import("./TagsPage"));
export const loadUsersPage = reusableModuleLoader(() => import("./UsersPage"));
export const loadBackupPage = reusableModuleLoader(() => import("./BackupPage"));
export const loadLogsPage = reusableModuleLoader(() => import("./LogsPage"));
export const loadSettingsPage = reusableModuleLoader(
  () => import("./SettingsPage")
);
