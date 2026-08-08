import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const app = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const layout = readFileSync(
  new URL("../src/admin/AdminLayout.tsx", import.meta.url),
  "utf8"
);
const page = readFileSync(
  new URL("../src/admin/BackupPage.tsx", import.meta.url),
  "utf8"
);
const api = readFileSync(new URL("../src/admin/api.ts", import.meta.url), "utf8");
const backupApiHandler = readFileSync(
  new URL("../backend/internal/api/admin_backups.go", import.meta.url),
  "utf8"
);
const backupTypes = readFileSync(
  new URL("../backend/internal/backup/types.go", import.meta.url),
  "utf8"
);
const backupArchive = readFileSync(
  new URL("../backend/internal/backup/archive.go", import.meta.url),
  "utf8"
);
const backupRestorePrepare = readFileSync(
  new URL("../backend/internal/backup/restore_prepare.go", import.meta.url),
  "utf8"
);
const backupRestoreMerge = readFileSync(
  new URL("../backend/internal/backup/restore_merge.go", import.meta.url),
  "utf8"
);
const backupRestoreAssets = readFileSync(
  new URL("../backend/internal/backup/restore_assets.go", import.meta.url),
  "utf8"
);
const backupRestoreLocal = readFileSync(
  new URL("../backend/internal/backup/restore_local.go", import.meta.url),
  "utf8"
);
const authContext = readFileSync(
  new URL("../src/admin/AuthContext.tsx", import.meta.url),
  "utf8"
);
const css = readFileSync(
  new URL("../src/styles/admin.css", import.meta.url),
  "utf8"
);
const serverMain = readFileSync(
  new URL("../backend/cmd/server/main.go", import.meta.url),
  "utf8"
);
const install = readFileSync(new URL("../install.sh", import.meta.url), "utf8");
const deploy = readFileSync(new URL("../deploy.sh", import.meta.url), "utf8");
const compose = readFileSync(
  new URL("../docker-compose.yml", import.meta.url),
  "utf8"
);

test("backup restore is reachable from the system navigation", () => {
  assert.match(app, /path="backup"[\s\S]*?<BackupPage \/>/);
  assert.match(layout, /to="\/admin\/backup"[\s\S]*?备份恢复/);
  assert.doesNotMatch(app, /path="\/tmp"/);
});

test("backup page keeps destructive restore confirmation concise", () => {
  assert.match(page, /restoreText !== "确认恢复"/);
  assert.doesNotMatch(page, /restorePassword|PasswordInput|当前管理员密码/);
  assert.match(api, /input: \{ confirmation: string \}/);
  assert.doesNotMatch(backupApiHandler, /CheckCurrentPassword|request\.Password/);
  assert.match(backupApiHandler, /request\.Confirmation != "确认恢复"/);
  assert.match(page, /服务就绪后返回登录页/);
  assert.match(page, /请手动重启后端，页面会继续检测/);
  assert.match(page, /<span>恢复所选内容并重启<\/span>/);
  assert.doesNotMatch(page, /将恢复所选内容并重启/);
});

test("restore acknowledgement stays bounded and survives the planned restart gap", () => {
  assert.match(backupApiHandler, /writeRestoreAccepted/);
  assert.match(backupApiHandler, /compactRestoreReport/);
  assert.match(backupApiHandler, /http\.Flusher/);
  assert.match(backupApiHandler, /restoreRestartGracePeriod/);
  assert.match(api, /export class APIResponseError/);
  assert.match(page, /shouldConfirmRestoreAfterTransportError/);
  assert.match(page, /restoreConfirmationStartedAt/);
  assert.match(page, /backupState\.pendingRestore \|\| Boolean\(backupState\.restoreProgress\)/);
  assert.match(page, /RESTORE_CONFIRMATION_GRACE_MS/);
});

test("restore confirmation input uses the shared theme-aware field palette", () => {
  assert.match(page, /className="admin-input"/);
  assert.match(css, /\.admin-form__row textarea,\s*\.admin-input \{[\s\S]*?background: var\(--bg-sunken\)/);
  assert.match(css, /\.admin-input:focus \{[\s\S]*?border-color: var\(--border-accent\)/);
  assert.match(css, /box-shadow:[^;]*var\(--accent-soft\)/);
});

test("backup creation uses credential-neutral backup wording", () => {
  assert.match(
    page,
    /className="admin-btn is-transparent"\s+onClick=\{handleCreate\}[\s\S]*?创建备份/
  );
  assert.match(page, /创建备份\n\s*<\/button>/);
  assert.match(page, /show\("备份任务已开始", "success"\)/);
  assert.match(page, /<span>当前没有备份包<\/span>/);
  assert.doesNotMatch(
    page,
    /className="admin-btn is-primary"[\s\S]{0,240}?创建备份/
  );
  assert.match(
    css,
    /\.admin-btn\.is-transparent,\s*\.admin-btn\.is-transparent:hover:not\(:disabled\)\s*\{[^}]*background:\s*transparent;/s
  );
  assert.doesNotMatch(page, /创建完整备份|完整备份任务已开始|还没有完整备份/);
});

test("backup creation lets administrators choose a durable backup scope", () => {
  assert.match(page, /title="选择备份内容"/);
  for (const label of [
    "网盘凭证和对应视频资源",
    "爬虫脚本和对应的视频资源",
    "上传存储和对应视频资源",
    "本地存储和对应的视频资源",
    "用户信息",
  ]) {
    assert.match(page, new RegExp(label));
  }
  assert.match(page, /Object\.values\(backupSelection\)\.some\(Boolean\)/);
  assert.match(api, /export type BackupSelection/);
  assert.match(api, /function createBackup\(selection\?: BackupSelection\)/);
  assert.match(api, /JSON\.stringify\(selection\)/);
  assert.match(backupApiHandler, /ErrNoBackupContent/);
});

test("backup creation starts with every scope unchecked", () => {
  assert.match(
    page,
    /const EMPTY_BACKUP_SELECTION:[\s\S]*?cloudDrives: false,[\s\S]*?crawlerScripts: false,[\s\S]*?uploadStorage: false,[\s\S]*?localStorage: false,[\s\S]*?userInfo: false,/
  );
  assert.match(
    page,
    /function handleCreate\(\) \{\s*setBackupSelection\(\{ \.\.\.EMPTY_BACKUP_SELECTION \}\);/
  );
  assert.doesNotMatch(page, /FULL_BACKUP_SELECTION/);
});

test("backup scope stays visible when choosing and confirming a restore", () => {
  assert.match(page, /function backupSelectionLabels/);
  assert.match(page, /backupSelectionLabels\(record\.selection\)/);
  assert.match(
    page,
    /<dt>恢复内容<\/dt>[\s\S]*?backupSelectionLabels\(restoreTarget\.selection\)/
  );
  assert.match(
    css,
    /\.backup-scope-list\s*\{[^}]*display:\s*flex;[^}]*flex-wrap:\s*wrap;/s
  );
  assert.match(
    css,
    /\.backup-scope\s*\{[^}]*background:\s*transparent;[^}]*color:\s*var\(--text-muted\);/s
  );
  assert.match(
    css,
    /\.backup-scope\s*\{[^}]*border:\s*1px solid var\(--border-default\);/s
  );
  assert.doesNotMatch(
    css,
    /\.backup-scope\s*\{[^}]*background:\s*var\(--(?:accent-soft|bg-elevated)\);/s
  );
  assert.match(
    css,
    /\.backup-restore-summary > \.backup-restore-summary__scope\s*\{[^}]*grid-column:\s*1 \/ -1;/s
  );
});

test("backup archives accept only the current protocol", () => {
  assert.match(backupTypes, /FormatVersion = 3/);
  assert.doesNotMatch(
    backupTypes,
    /ScopedFormatVersion|LegacyFormatVersion|UserConfig/
  );
  assert.match(
    backupArchive,
    /if manifest\.FormatVersion != FormatVersion \{[\s\S]*?unsupported format version/
  );
  assert.match(backupArchive, /if manifest\.Selection == nil/);
  assert.doesNotMatch(
    backupArchive,
    /ScopedFormatVersion|LegacyFormatVersion|EffectiveSelection/
  );
  assert.doesNotMatch(backupRestorePrepare, /\.IsFull\(\)|UserConfig/);
  assert.doesNotMatch(api, /userConfig/);
});

test("upload storage merges while each source local storage keeps an isolated namespace", () => {
  assert.match(backupRestoreMerge, /driveUsesMergeRestore/);
  assert.match(backupRestoreMerge, /restore_merged_drives/);
  assert.doesNotMatch(
    backupRestoreMerge,
    /DELETE FROM main\.remote_upload_jobs/
  );
  assert.doesNotMatch(backupRestoreMerge, /INSERT OR REPLACE/);
  assert.match(
    backupRestoreMerge,
    /INSERT OR IGNORE INTO main\.video_tags/
  );
  assert.match(backupRestoreAssets, /prepareMergedUploadStorage/);
  assert.match(
    backupRestoreAssets,
    /snapshotSource\(ctx, source, merged\)[\s\S]*?overlaySource\(ctx, target, merged\)/
  );
  assert.match(backupRestorePrepare, /prepareIsolatedLocalStorage/);
  assert.doesNotMatch(backupRestorePrepare, /prepareMergedLocalStorage/);
  assert.match(
    backupRestoreLocal,
    /fmt\.Sprintf\("localstorage-restore-%s-%03d", stageID, index\+1\)/
  );
  assert.match(backupRestoreLocal, /SourceDriveID/);
  assert.match(backupRestoreLocal, /rewriteLocalStorageCatalog/);
  assert.match(backupRestoreLocal, /videoid\.ForDrive\("localstorage", video\.newDriveID/);
  const isolatedLocalRestore = backupRestoreAssets.slice(
    backupRestoreAssets.indexOf("func prepareIsolatedLocalStorage"),
    backupRestoreAssets.indexOf("func overlaySource")
  );
  assert.doesNotMatch(
    isolatedLocalRestore,
    /preserve target local storage|overlaySource\(ctx, target, merged\)/
  );
});

test("backup creation dialog uses flat chrome without structural divider lines", () => {
  assert.match(
    css,
    /\.admin-modal\.admin-modal--backup-create\s*\{[^}]*width:\s*min\(520px,\s*100%\);[^}]*border:\s*0;[^}]*box-shadow:\s*none;/s
  );
  assert.match(
    css,
    /\.admin-modal--backup-create \.admin-modal__header,\s*\.admin-modal--backup-create \.admin-modal__footer\s*\{[^}]*border:\s*0;[^}]*background:\s*var\(--bg-surface\);/s
  );
  assert.match(css, /\.backup-selection-option\s*\{[^}]*border:\s*0;/s);
  assert.doesNotMatch(
    css,
    /\.backup-selection-option:hover\s*\{[^}]*border-color:/s
  );
});

test("backup overview uses one full-width card with three evenly distributed metrics", () => {
  const overview = page.slice(
    page.indexOf('<section className="backup-overview"'),
    page.indexOf("{current && taskActive(current)")
  );

  assert.equal(overview.match(/className="backup-stat"/g)?.length, 3);
  assert.match(overview, /预计数据量[\s\S]*服务器可用空间[\s\S]*备份数量/);
  assert.match(
    css,
    /\.backup-overview\s*\{[^}]*grid-template-columns:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\);[^}]*width:\s*100%;[^}]*border:\s*1px solid var\(--border-subtle\);[^}]*border-radius:\s*var\(--radius-md\);[^}]*background:\s*var\(--bg-surface\);/s
  );
  assert.match(
    css,
    /\.backup-stat\s*\{[^}]*justify-items:\s*center;[^}]*text-align:\s*center;/s
  );
  assert.match(
    css,
    /\.backup-stat \+ \.backup-stat\s*\{[^}]*border-left:\s*1px solid var\(--border-subtle\);/s
  );
  assert.doesNotMatch(
    css,
    /@media \(max-width: 840px\)\s*\{[\s\S]*?\.backup-overview\s*\{[^}]*grid-template-columns:\s*1fr;/
  );
});

test("backup loading keeps the fixed page shell and leaves backend data blank", () => {
  assert.doesNotMatch(page, /AdminLoading|lds-ellipsis/);
  assert.doesNotMatch(page, /if \(loading && !data\)/);
  assert.match(
    page,
    /className="admin-page backup-page"\s+aria-busy=\{loading \|\| undefined\}/
  );
  assert.match(
    page,
    /<section className="backup-overview"[\s\S]*?data \? formatBytes\(estimate\?\.totalBytes\) : null[\s\S]*?data \? formatBytes\(estimate\?\.availableBytes\) : null[\s\S]*?data \? data\.backups\.length : null/
  );
  assert.match(
    page,
    /<section className="admin-card backup-upload-card">[\s\S]*?<section className="backup-list-section">/
  );
  assert.match(
    page,
    /\{data\?\.backups\.length \? \([\s\S]*?className="backup-list"[\s\S]*?\) : data \? \([\s\S]*?className="backup-empty"[\s\S]*?\) : null\}/
  );
  assert.match(page, /disabled=\{!data \|\| creating \|\| taskActive\(current\)/);
  assert.match(
    css,
    /\.backup-stat strong\s*\{[^}]*min-height:\s*1\.2em;[^}]*line-height:\s*1\.2;/s
  );
});

test("migration upload uses resumable 16 MiB server chunks with hashes", () => {
  assert.match(api, /X-Chunk-SHA256/);
  assert.match(api, /\/backup-uploads\/\$\{encodeURIComponent\(id\)\}\/chunks\/\$\{index\}/);
  assert.match(page, /crypto\.subtle\.digest\("SHA-256"/);
  assert.match(page, /localStorage\.setItem\(RESUME_KEY/);
  assert.match(page, /继续上传/);
  assert.match(page, /handlePause/);
  assert.match(page, /校验并入库/);
  assert.doesNotMatch(page, /正在合并并完整校验/);
});

test("backup upload aligns its picker with an ordinary compact upload button", () => {
  assert.match(
    page,
    /className="backup-file-picker"[\s\S]*?<div className="backup-upload-actions">[\s\S]*?className="admin-btn backup-upload-submit"[\s\S]*?开始上传/
  );
  assert.doesNotMatch(
    page,
    /className="admin-btn is-primary"[\s\S]{0,180}?\{upload\?\.received\.length \? "继续上传" : "开始上传"\}/
  );
  assert.match(
    css,
    /\.backup-upload-controls\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto;[^}]*align-items:\s*stretch;/s
  );
  assert.match(
    css,
    /\.backup-file-picker\s*\{[^}]*width:\s*auto;[^}]*min-height:\s*40px;[^}]*padding:\s*8px 12px;/s
  );
  assert.match(
    css,
    /\.backup-upload-actions \.admin-btn\s*\{[^}]*flex:\s*0 0 auto;[^}]*min-height:\s*40px;[^}]*padding-inline:\s*14px;/s
  );
  assert.doesNotMatch(
    css,
    /\.backup-upload-submit\s*\{[^}]*min-width:/s
  );
  assert.match(
    css,
    /@media \(max-width: 600px\)[\s\S]*?\.backup-upload-controls\s*\{[^}]*grid-template-columns:\s*1fr;/s
  );
});

test("backup long operations render phase-driven task checklists", () => {
  assert.match(api, /export type BackupOperationProgress/);
  assert.match(api, /restoreProgress\?: BackupOperationProgress/);
  assert.match(page, /function BackupOperationChecklist/);
  assert.match(page, /upload\?\.progress\?\.phase/);
  assert.match(page, /data\?\.restoreProgress/);
  assert.match(page, /校验完整文件/);
  assert.match(page, /校验并解压暂存/);
  assert.match(page, /检查暂存数据库/);
  assert.doesNotMatch(page, /每个文件只读取一次/);
  assert.doesNotMatch(page, /生成可回滚的切换清单/);
  assert.match(css, /\.backup-operation-steps/);
  assert.match(css, /backup-progress-indeterminate/);
  assert.match(css, /backup-marker-breathe/);
  assert.match(css, /backup-check-pop/);
  assert.match(css, /prefers-reduced-motion/);
});

test("active backup task progress uses neutral status colors", () => {
  assert.match(
    css,
    /\.backup-task\s*\{[^}]*border:\s*1px solid var\(--border-subtle\);[^}]*background:\s*var\(--bg-surface\);/s
  );
  assert.match(
    css,
    /\.backup-task__percent\s*\{[^}]*color:\s*var\(--text-muted\);/s
  );
  assert.match(
    css,
    /\.backup-task \.backup-progress > span\s*\{[^}]*background:\s*var\(--text-muted\);/s
  );
});

test("active backup task keeps metrics, percentage, and cancellation above the progress bar", () => {
  const activeTask = page.slice(
    page.indexOf("{current && taskActive(current)"),
    page.indexOf('<div className="backup-grid">')
  );
  assert.match(
    activeTask,
    /className="backup-task__progress-row"[\s\S]*?className="backup-task__meta"[\s\S]*?className="backup-task__percent"[\s\S]*?onClick=\{handleCancelBackup\}[\s\S]*?className="backup-progress"/
  );
  assert.match(
    css,
    /\.backup-task__progress-row\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto auto;/s
  );
  assert.match(
    css,
    /\.backup-task__meta > span\s*\{[^}]*text-overflow:\s*ellipsis;[^}]*white-space:\s*nowrap;/s
  );
});

test("active backup cancellation uses the transparent ordinary button style", () => {
  assert.match(
    page,
    /taskActive\(current\) && current\.cancellable[\s\S]*?className="admin-btn is-transparent"[\s\S]*?onClick=\{handleCancelBackup\}/
  );
  assert.doesNotMatch(page, /className="admin-btn is-stop" onClick=\{handleCancelBackup\}/);
  assert.match(
    css,
    /\.admin-btn\.is-transparent,\s*\.admin-btn\.is-transparent:hover:not\(:disabled\)\s*\{[^}]*background:\s*transparent;/s
  );
});

test("backup layout collapses safely on narrow screens", () => {
  assert.match(css, /@media \(max-width: 600px\)[\s\S]*?\.backup-stat/);
  assert.match(css, /@media \(max-width: 600px\)[\s\S]*?\.backup-file-picker/);
  assert.match(css, /\.backup-record__actions \.admin-btn[\s\S]*?flex: 1 1 110px/);
  assert.match(css, /\.admin-modal\.admin-modal--backup-restore[\s\S]*?width: min\(620px, 100%\)/);
});

test("supported deployments restart on the dedicated restore exit code", () => {
  assert.match(serverMain, /os\.Exit\(backup\.RestartExitCode\)/);
  assert.match(install, /RestartForceExitStatus=75/);
  assert.match(install, /VIDEO_RESTART_MANAGED=true/);
  assert.match(deploy, /RestartForceExitStatus=75/);
  assert.match(deploy, /VIDEO_RESTART_MANAGED=true/);
  assert.match(compose, /VIDEO_RESTART_MANAGED: "true"/);
  assert.match(compose, /restart: unless-stopped/);
});

test("deploy keeps systemd environment lines separate from LimitNOFILE", () => {
  assert.match(deploy, /\$\{env_lines\}\nLimitNOFILE=65536/);
  assert.doesNotMatch(deploy, /\$\{env_lines\}LimitNOFILE=65536/);
});

test("restore polling reports a missing durable restore only after its grace window", () => {
  assert.match(
    page,
    /const restoreInProgress =\s*backupState\.pendingRestore \|\| Boolean\(backupState\.restoreProgress\)/
  );
  assert.match(
    page,
    /!restoreInProgress[\s\S]*?RESTORE_CONFIRMATION_GRACE_MS[\s\S]*?未确认恢复已启动，当前数据保持不变，请重试/
  );
  assert.match(page, /未确认恢复已启动，当前数据保持不变，请重试/);
  assert.match(page, /restoreReport\?\.localStorageWarnings/);
  assert.match(page, /restoreReport\?\.missingAssets/);
});

test("successful restore invalidates cached auth before opening login", () => {
  assert.match(authContext, /invalidateSession:\s*\(\) => void/);
  assert.match(
    authContext,
    /const invalidateSession = useCallback\(\(\) => \{[\s\S]*?setStatus\("guest"\);[\s\S]*?setRole\(""\);/
  );
  const polling = page.slice(
    page.indexOf("const redirectToLogin"),
    page.indexOf("const current = data?.current")
  );
  assert.ok(
    polling.indexOf("invalidateSession();") < polling.indexOf('navigate("/login"'),
    "the shared auth state must become guest before LoginPage renders"
  );
  assert.match(polling, /!state\.authenticated[\s\S]*?redirectToLogin\(\)/);
});

test("restore polling starts only after validation and staging are accepted", () => {
  assert.match(page, /校验并解压暂存/);
  assert.match(
    page,
    /const \[restoreSubmitting, setRestoreSubmitting\] = useState\(false\)/
  );
  const handler = page.slice(
    page.indexOf("async function handleRestore()"),
    page.indexOf("function closeRestore()")
  );
  assert.ok(
    handler.indexOf("await api.restoreBackup") < handler.indexOf("setRestoring(true)"),
    "restart polling must not begin while the restore request is still staging"
  );
});
