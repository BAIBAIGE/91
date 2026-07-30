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
});

test("backup page exposes full backup safety and destructive restore confirmation", () => {
  assert.match(page, /备份包不加密/);
  assert.match(page, /网盘令牌、账号数据和媒体文件/);
  assert.match(page, /restoreText !== "确认恢复"/);
  assert.match(page, /<PasswordInput/);
  assert.match(page, /所有用户需要重新登录/);
  assert.match(page, /当前运行方式没有进程守护/);
});

test("backup creation uses credential-neutral backup wording", () => {
  assert.match(page, /\n          创建备份\n        <\/button>/);
  assert.match(page, /show\("备份任务已开始", "success"\)/);
  assert.match(page, /<span>还没有备份<\/span>/);
  assert.doesNotMatch(page, /创建完整备份|完整备份任务已开始|还没有完整备份/);
});

test("migration upload uses resumable 16 MiB server chunks with hashes", () => {
  assert.match(api, /X-Chunk-SHA256/);
  assert.match(api, /\/backup-uploads\/\$\{encodeURIComponent\(id\)\}\/chunks\/\$\{index\}/);
  assert.match(page, /crypto\.subtle\.digest\("SHA-256"/);
  assert.match(page, /localStorage\.setItem\(RESUME_KEY/);
  assert.match(page, /继续上传/);
  assert.match(page, /handlePause/);
});

test("backup layout collapses safely on narrow screens", () => {
  assert.match(css, /@media \(max-width: 840px\)[\s\S]*?\.backup-overview/);
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
  assert.match(page, /restoreReport\.localStorageWarnings/);
  assert.match(page, /restoreReport\.missingAssets/);
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
