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

test("restore polling distinguishes success from an automatic rollback", () => {
  assert.match(page, /!backupState\.pendingRestore/);
  assert.match(page, /旧数据已自动回滚/);
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
