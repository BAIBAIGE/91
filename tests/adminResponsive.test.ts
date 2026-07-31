import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const adminCss = readFileSync(
  new URL("../src/styles/admin.css", import.meta.url),
  "utf8"
);
const videosPageSource = readFileSync(
  new URL("../src/admin/VideosPage.tsx", import.meta.url),
  "utf8"
);
const usersPageSource = readFileSync(
  new URL("../src/admin/UsersPage.tsx", import.meta.url),
  "utf8"
);
const tagsPageSource = readFileSync(
  new URL("../src/admin/TagsPage.tsx", import.meta.url),
  "utf8"
);
const crawlersPageSource = readFileSync(
  new URL("../src/admin/CrawlersPage.tsx", import.meta.url),
  "utf8"
);
const drivesPageSource = readFileSync(
  new URL("../src/admin/DrivesPage.tsx", import.meta.url),
  "utf8"
);
const apiSource = readFileSync(
  new URL("../src/admin/api.ts", import.meta.url),
  "utf8"
);

function ruleBody(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `Expected CSS rule for ${selector}`);
  return match[1];
}

function allRuleBodies(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return Array.from(css.matchAll(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`, "g")))
    .map((match) => match[1])
    .join("\n");
}

// ruleBodyByContains 处理 CSS 里"多 selector 共享 body"的合并写法：
//   .a, .b, .c {
//     ...
//   }
// 上面的 `.b` 用直接的 `selector\s*\{` 正则匹不到。这里改成"找到任何包含目标
// selector 的连续 selector 列表（可含逗号 + 空白），紧跟一个 { ... } body"。
//
// 仅支持 body 内不再嵌套 `{}`（admin.css 没有 nesting，足够）。
function ruleBodyByContains(css: string, needle: string): string {
  const escapedNeedle = needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`([^{}]*${escapedNeedle}[^{}]*)\\{([^}]*)\\}`, "g");
  const bodies: string[] = [];
  for (const m of css.matchAll(re)) {
    bodies.push(m[2]);
  }
  assert.ok(bodies.length > 0, `Expected at least one CSS rule containing ${needle}`);
  return bodies.join("\n");
}

function mobileCss(): string {
  const marker = "@media (max-width: 768px)";
  const start = adminCss.indexOf(marker);
  assert.notEqual(start, -1, "Expected mobile admin media query");
  return adminCss.slice(start);
}

test("admin login card fits narrow phone screens", () => {
  const body = ruleBody(adminCss, ".admin-login__card");

  // 桌面规则就用 min(...) 让窄屏自然适配；具体上限以 CSS 当前值为准（400px），
  // 关键是 `min(<某值>, 100%)` + `box-sizing: border-box`。
  assert.match(body, /width\s*:\s*min\(\d+px,\s*100%\)/);
  assert.match(body, /box-sizing\s*:\s*border-box/);
});

test("admin password fields use shared PasswordInput with eye toggle", () => {
  const passwordInputSource = readFileSync(
    new URL("../src/admin/PasswordInput.tsx", import.meta.url),
    "utf8"
  );
  const loginPageSource = readFileSync(
    new URL("../src/admin/LoginPage.tsx", import.meta.url),
    "utf8"
  );
  const driveFormSource = readFileSync(
    new URL("../src/admin/drive/DriveForm.tsx", import.meta.url),
    "utf8"
  );

  assert.match(passwordInputSource, /from "lucide-react"/);
  assert.match(passwordInputSource, /EyeOff/);
  assert.match(passwordInputSource, /type=\{visible \? "text" : "password"\}/);
  assert.match(passwordInputSource, /aria-label=\{visible \? "隐藏密码" : "显示密码"\}/);
  assert.match(loginPageSource, /import \{ PasswordInput \} from "\.\/PasswordInput"/);
  assert.match(loginPageSource, /<PasswordInput[\s\S]*?id="admin-login-password"/);
  assert.match(loginPageSource, /<PasswordInput[\s\S]*?id="admin-login-password-confirm"/);
  assert.doesNotMatch(loginPageSource, /type="password"/);
  assert.match(driveFormSource, /import \{ PasswordInput \} from "\.\.\/PasswordInput"/);
  assert.match(driveFormSource, /isSecretCredential\(f\.key\) \? \(/);
  assert.match(driveFormSource, /<PasswordInput[\s\S]*?id=\{`\$\{idPrefix\}-credential-\$\{f\.key\}`\}/);
  assert.doesNotMatch(usersPageSource, /type="password"/);
  assert.doesNotMatch(driveFormSource, /type="password"/);
});

test("admin tables scroll inside the mobile viewport", () => {
  const css = mobileCss();
  // 视频/标签等"长内容"表的 mobile 形态：用 `.admin-table:not(.admin-drives-table)`
  // 把它们改成 display:block 卡片栈；网盘表 .admin-drives-table 走另一组 1280px 媒体
  // 查询。这里只断"非 drives 表的 mobile 卡片化"。
  const body = ruleBody(css, ".admin-table:not(.admin-drives-table)");

  assert.match(body, /display\s*:\s*block/);
});

test("mobile user management cards keep identity, metadata, and actions separated", () => {
  const css = mobileCss();
  const userCard = ruleBodyByContains(css, ".admin-users-table:not(.admin-drives-table) tr");
  const ipCard = ruleBodyByContains(css, ".admin-banned-ips-table:not(.admin-drives-table) tr");
  const username = ruleBody(css, ".admin-users-table:not(.admin-drives-table) .admin-users-table__username");
  const userRole = ruleBodyByContains(css, ".admin-users-table:not(.admin-drives-table) .admin-users-table__role");
  const userTime = ruleBody(css, ".admin-users-table:not(.admin-drives-table) .admin-users-table__time");
  const userActions = ruleBody(css, ".admin-users-table:not(.admin-drives-table) .admin-users-table__actions");
  const userStatus = ruleBody(css, ".admin-users-table:not(.admin-drives-table) .admin-status");
  const userStatusDot = ruleBody(css, ".admin-users-table:not(.admin-drives-table) .admin-status::before");
  const actionRow = ruleBody(css, ".admin-users-table__action-row");
  const createUserModal = ruleBody(adminCss, ".admin-modal.admin-modal--user-create");
  const createUserModalChrome = ruleBodyByContains(adminCss, ".admin-modal--user-create .admin-modal__header");
  const createUserSelect = ruleBody(adminCss, ".admin-modal--user-create .admin-form-select");
  const createUserSelectIcon = ruleBody(adminCss, ".admin-modal--user-create .admin-form-select__icon");
  const passwordResetModal = ruleBody(adminCss, ".admin-modal.admin-modal--password-reset");
  const passwordResetChrome = ruleBodyByContains(adminCss, ".admin-modal--password-reset .admin-modal__header");
  const passwordResetClose = ruleBody(adminCss, ".admin-modal--password-reset .admin-modal__header .admin-btn");
  const pageLoading = ruleBody(adminCss, ".admin-page-loading");
  const userToolbarActions = ruleBody(css, ".admin-users-toolbar-actions");
  const createUserFab = ruleBody(css, ".admin-users-create-fab");
  const ipIdentity = ruleBody(css, ".admin-banned-ips-table:not(.admin-drives-table) .admin-banned-ips-table__ip");
  const ipReason = ruleBodyByContains(css, ".admin-banned-ips-table:not(.admin-drives-table) .admin-banned-ips-table__reason");
  const ipActions = ruleBody(css, ".admin-banned-ips-table:not(.admin-drives-table) .admin-banned-ips-table__actions");

  assert.match(usersPageSource, /className="admin-table admin-users-table"/);
  assert.match(usersPageSource, /className="admin-table admin-banned-ips-table"/);
  assert.match(usersPageSource, /data-label="用户名"/);
  assert.match(usersPageSource, /data-label="IP 地址"/);
  assert.doesNotMatch(usersPageSource, /<th>ID<\/th>/);
  assert.doesNotMatch(usersPageSource, /admin-users-table__id/);
  assert.match(usersPageSource, /title="创建用户"[\s\S]*?className="admin-modal--user-create"/);
  assert.match(usersPageSource, /import \{ AdminLoading \} from "\.\/AdminLoading";/);
  assert.match(usersPageSource, /<AdminLoading \/>/);
  assert.doesNotMatch(usersPageSource, /<span>加载中\.\.\.<\/span>/);
  assert.doesNotMatch(usersPageSource, /加载中\.\.\./);
  assert.match(pageLoading, /min-height\s*:\s*calc\(100dvh - 48px - var\(--space-7\) \* 2 - 44px - var\(--space-4\)\)/);
  assert.match(createUserModal, /width\s*:\s*min\(380px,\s*100%\)/);
  assert.match(createUserModal, /border\s*:\s*0/);
  assert.match(createUserModal, /box-shadow\s*:\s*none/);
  assert.match(createUserModalChrome, /border\s*:\s*0/);
  assert.match(createUserModalChrome, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(usersPageSource, /className="admin-form-select"[\s\S]*?<ChevronDown size=\{15\} className="admin-form-select__icon"/);
  assert.match(createUserSelect, /padding-right\s*:\s*44px/);
  assert.match(createUserSelectIcon, /right\s*:\s*15px/);
  assert.match(usersPageSource, /title="重置密码"[\s\S]*?className="admin-modal--password-reset"/);
  assert.match(usersPageSource, /import \{ PasswordInput \} from "\.\/PasswordInput"/);
  assert.match(usersPageSource, /<PasswordInput[\s\S]*?value=\{createPassword\}/);
  assert.match(usersPageSource, /<PasswordInput[\s\S]*?value=\{resetPasswordValue\}/);
  assert.match(
    usersPageSource,
    /title="删除用户"[\s\S]*?message=\{`确定要删除用户「\$\{deleteConfirm\?\.username \?\? ""\}」吗\？`\}/
  );
  assert.doesNotMatch(usersPageSource, /此操作不可撤销/);
  assert.match(
    usersPageSource,
    /title="删除用户"\s*message=\{`确定要删除用户「\$\{deleteConfirm\?\.username \?\? ""\}」吗\？`\}\s*hideIcon\s*danger\s*modalClassName="admin-modal--user-delete"/
  );
  assert.doesNotMatch(
    usersPageSource,
    /title="删除用户"[\s\S]{0,200}?confirmText=/
  );
  assert.match(
    usersPageSource,
    /title="解除IP封禁"\s*message=\{`确定要解除 IP「\$\{unbanIPConfirm \?\? ""\}」的封禁吗\？`\}\s*hideIcon\s*modalClassName="admin-modal--ip-unban"/
  );
  assert.doesNotMatch(
    usersPageSource,
    /title="解除IP封禁"[\s\S]{0,200}?confirmText=/
  );
  assert.match(passwordResetModal, /width\s*:\s*min\(380px,\s*100%\)/);
  assert.match(ruleBodyByContains(adminCss, ".admin-modal.admin-modal--user-delete"), /width\s*:\s*min\(380px,\s*100%\)/);
  assert.match(ruleBodyByContains(adminCss, ".admin-modal.admin-modal--ip-unban"), /width\s*:\s*min\(380px,\s*100%\)/);
  assert.match(passwordResetModal, /border\s*:\s*0/);
  assert.match(passwordResetModal, /box-shadow\s*:\s*none/);
  assert.match(passwordResetChrome, /border\s*:\s*0/);
  assert.match(passwordResetChrome, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(passwordResetClose, /border-color\s*:\s*transparent/);
  assert.match(passwordResetClose, /background\s*:\s*transparent/);
  assert.match(ruleBody(adminCss, ".admin-password-input"), /position\s*:\s*relative/);
  assert.match(ruleBody(adminCss, ".admin-password-input input"), /padding-right\s*:\s*42px/);
  assert.match(ruleBody(adminCss, ".admin-password-input__toggle"), /position\s*:\s*absolute/);
  assert.match(usersPageSource, /className="admin-btn admin-btn--small is-danger"/);
  assert.match(usersPageSource, /className="admin-btn admin-btn--small"[\s\S]*?title="解除封禁"[\s\S]*?>\s*解除封禁\s*<\/button>/);
  assert.doesNotMatch(usersPageSource, /className="admin-btn admin-btn--small is-primary"[\s\S]*?解除封禁/);
  assert.doesNotMatch(usersPageSource, /<CheckCircle size=\{14\} \/> 解除封禁/);
  assert.match(userToolbarActions, /position\s*:\s*fixed/);
  assert.match(userToolbarActions, /right\s*:\s*var\(--space-3\)/);
  assert.match(userToolbarActions, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(userToolbarActions, /z-index\s*:\s*calc\(var\(--z-nav\)\s*\+\s*2\)/);
  assert.match(createUserFab, /min-height\s*:\s*44px/);
  assert.match(createUserFab, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(createUserFab, /box-shadow\s*:\s*0\s+12px\s+32px/);
  assert.match(
    usersPageSource,
    /className="admin-users-toolbar"[\s\S]*?className="admin-users-tabs[\s\S]*?className="admin-users-toolbar-actions"[\s\S]*?tab === "users" && \(/
  );
  assert.match(ruleBody(adminCss, ".admin-users-toolbar"), /display\s*:\s*flex/);
  assert.match(ruleBody(adminCss, ".admin-users-toolbar"), /justify-content\s*:\s*flex-end/);
  assert.match(ruleBody(adminCss, ".admin-users-toolbar"), /position\s*:\s*relative/);
  assert.match(ruleBody(adminCss, ".admin-users-tabs"), /position\s*:\s*absolute/);
  assert.match(ruleBody(adminCss, ".admin-users-tabs"), /left\s*:\s*50%/);
  assert.match(ruleBody(adminCss, ".admin-users-tabs"), /transform\s*:\s*translateX\(-50%\)/);
  assert.match(ruleBody(adminCss, ".admin-users-toolbar-actions"), /justify-content\s*:\s*flex-end/);
  assert.match(userCard, /grid-template-columns\s*:\s*repeat\(12,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(userCard, /border-radius\s*:\s*var\(--radius-sm\)/);
  assert.match(ipCard, /grid-template-columns\s*:\s*repeat\(12,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(username, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(username, /text-align\s*:\s*center/);
  assert.match(userRole, /grid-row\s*:\s*2/);
  assert.match(userRole, /justify-items\s*:\s*center/);
  assert.match(userRole, /text-align\s*:\s*center/);
  assert.match(userRole, /border-top\s*:\s*1px\s+solid\s+var\(--border-subtle\)/);
  assert.match(userTime, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(userTime, /grid-row\s*:\s*3/);
  assert.match(userActions, /grid-row\s*:\s*4/);
  assert.match(userStatus, /gap\s*:\s*0/);
  assert.match(userStatusDot, /content\s*:\s*none/);
  assert.match(userStatusDot, /display\s*:\s*none/);
  assert.match(actionRow, /grid-template-columns\s*:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(ipIdentity, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(ipReason, /grid-row\s*:\s*2/);
  assert.match(ipReason, /border-top\s*:\s*1px\s+solid\s+var\(--border-subtle\)/);
  assert.match(ipActions, /grid-row\s*:\s*4/);
  assert.match(ipActions, /display\s*:\s*flex/);
  assert.match(ipActions, /justify-content\s*:\s*center/);
  assert.match(
    ruleBody(css, ".admin-banned-ips-table:not(.admin-drives-table) .admin-banned-ips-table__actions .admin-btn"),
    /width\s*:\s*auto/
  );
  assert.doesNotMatch(
    ruleBody(css, ".admin-banned-ips-table:not(.admin-drives-table) .admin-banned-ips-table__actions .admin-btn"),
    /width\s*:\s*100%/
  );
});

test("admin video management uses a modal advanced filter without a page title", () => {
  const desktopRange = ruleBody(adminCss, ".admin-modal--video-filters .admin-video-advanced-range");
  const desktopRangeInputs = ruleBody(adminCss, ".admin-modal--video-filters .admin-video-advanced-range__inputs");
  const desktopRangeControls = ruleBodyByContains(
    adminCss,
    '.admin-modal--video-filters .admin-video-advanced-range input[type="date"]'
  );

  assert.match(videosPageSource, /\{ key: "current", label: "正常视频" \}/);
  assert.doesNotMatch(videosPageSource, /\{ key: "current", label: "当前视频" \}/);
  assert.doesNotMatch(videosPageSource, /function DriveFilter/);
  assert.doesNotMatch(videosPageSource, /admin-videos-filter__select/);
  assert.doesNotMatch(videosPageSource, /<h1 className="admin-page__title">视频管理<\/h1>/);
  assert.doesNotMatch(videosPageSource, /const \[driveId, setDriveId\]/);
  assert.match(videosPageSource, /className="admin-videos-filter__current-actions"/);
  assert.match(videosPageSource, /className="admin-btn admin-video-advanced-toggle"/);
  assert.doesNotMatch(videosPageSource, /SlidersHorizontal/);
  assert.match(videosPageSource, /aria-haspopup="dialog"/);
  assert.match(videosPageSource, /<Modal[\s\S]*?open=\{advancedFiltersOpen\}[\s\S]*?title="高级筛选"[\s\S]*?className="admin-modal--video-filters"/);
  assert.match(videosPageSource, /function AdvancedVideoFilters/);
  assert.match(videosPageSource, /function VideoSourcePicker/);
  assert.match(videosPageSource, /<span>来源<\/span>/);
  assert.match(videosPageSource, /<strong>上传来源<\/strong>/);
  assert.match(videosPageSource, /selectSource\("drive", LOCAL_UPLOAD_SOURCE_ID\)/);
  assert.doesNotMatch(videosPageSource, /className="admin-video-upload-source-options"/);
  assert.match(videosPageSource, /className="admin-video-source-picker__trigger"/);
  assert.match(videosPageSource, /role="listbox" aria-label="来源"/);
  assert.match(videosPageSource, /role="group" aria-label="网盘"/);
  assert.match(videosPageSource, /role="group" aria-label="爬虫"/);
  assert.doesNotMatch(videosPageSource, /<optgroup|<option value="">全部来源/);
  assert.doesNotMatch(videosPageSource, /getVideoStats|admin-video-tab__count/);
  assert.doesNotMatch(adminCss, /admin-videos-filter__select/);
  assert.match(adminCss, /\.admin-video-advanced-toggle\s*\{/);
  assert.doesNotMatch(adminCss, /\.admin-video-advanced-toggle\.is-active/);
  assert.match(adminCss, /\.admin-modal\.admin-modal--video-filters\s*\{/);
  assert.match(adminCss, /\.admin-modal\.admin-modal--video-filters\s*\{[^}]*border:\s*0;/s);
  assert.match(
    adminCss,
    /\.admin-modal--video-filters \.admin-modal__header,\s*\.admin-modal--video-filters \.admin-modal__footer\s*\{[^}]*background:\s*var\(--bg-surface\);/s
  );
  assert.match(adminCss, /\.admin-video-advanced-filters\s*\{/);
  assert.match(adminCss, /\.admin-video-source-picker__trigger\s*\{/);
  assert.match(adminCss, /\.admin-video-source-picker__menu\s*\{/);
  assert.match(adminCss, /\.admin-video-source-picker__icon\.is-upload\s*\{/);
  assert.match(desktopRange, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(desktopRangeInputs, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\) auto minmax\(0,\s*1fr\)/);
  assert.match(desktopRangeInputs, /justify-content\s*:\s*stretch/);
  assert.match(desktopRangeInputs, /width\s*:\s*100%/);
  assert.match(desktopRangeControls, /width\s*:\s*100%/);
  assert.doesNotMatch(ruleBody(adminCss, ".admin-video-source-picker__search"), /border-bottom/);
  assert.doesNotMatch(ruleBody(adminCss, ".admin-video-source-picker__option.is-selected"), /border/);
  assert.doesNotMatch(
    ruleBody(adminCss, ".admin-video-source-picker__group + .admin-video-source-picker__group"),
    /border-top/
  );
  assert.doesNotMatch(adminCss, /admin-video-tab__count/);
});

test("current video bulk actions use ordinary text buttons", () => {
  const currentVideosSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const base = ruleBody(adminCss, ".admin-videos-bulk-actions__btn");

  assert.equal(Array.from(currentVideosSource.matchAll(/className="admin-btn admin-videos-bulk-actions__btn"/g)).length, 3);
  assert.match(currentVideosSource, /onClick=\{selectPageVideos\}[\s\S]*?disabled=\{listItems\.length === 0 \|\| allPageSelected\}[\s\S]*?>\s*全选本页\s*<\/button>/);
  assert.match(currentVideosSource, /onClick=\{\(\) => setSelectedIds\(new Set\(\)\)\}[\s\S]*?disabled=\{selectedIds\.size === 0\}[\s\S]*?>\s*取消选中\s*<\/button>/);
  assert.doesNotMatch(currentVideosSource, />\s*重生预览\s*<\/button>/);
  assert.doesNotMatch(currentVideosSource, /handleBatchRegen|batchRegenOpen|batchRegening|confirmBatchRegen/);
  assert.match(currentVideosSource, />\s*批量删除\s*<\/button>/);
  assert.match(currentVideosSource, /admin-videos-bulk-actions__mobile-exit[\s\S]*?>\s*退出选择\s*<\/button>/);
  assert.doesNotMatch(currentVideosSource, /className="admin-btn admin-videos-bulk-actions__btn"[^>]*>\s*<(?:RefreshCw|Trash2)/);
  assert.doesNotMatch(currentVideosSource, /is-primary admin-videos-bulk-actions__btn|is-danger admin-videos-bulk-actions__btn/);
  assert.match(base, /box-shadow\s*:\s*none/);
  assert.doesNotMatch(adminCss, /admin-videos-bulk-actions__btn\.is-danger/);
});

test("admin tag bulk actions use a fixed floating toolbar", () => {
  const css = mobileCss();
  const pageWithBulk = ruleBody(adminCss, ".admin-tags-page.has-bulk-actions");
  const toolbar = ruleBody(adminCss, ".admin-tags-bulk-toolbar");
  const actions = ruleBody(adminCss, ".admin-tags-bulk-actions");
  const count = ruleBodyByContains(adminCss, ".admin-tags-bulk-actions__count");
  const mobileToolbar = allRuleBodies(css, ".admin-tags-bulk-toolbar");
  const mobilePageWithBulk = allRuleBodies(css, ".admin-tags-page.has-bulk-actions");
  const mobileActions = allRuleBodies(css, ".admin-tags-bulk-actions");
  const mobileButton = allRuleBodies(css, ".admin-tags-bulk-actions__btn");
  const mobileExit = allRuleBodies(css, ".admin-tags-bulk-actions__mobile-exit");

  assert.match(tagsPageSource, /className="admin-tags-bulk-toolbar"/);
  assert.match(tagsPageSource, /aria-label="标签批量操作"/);
  assert.match(tagsPageSource, /aria-pressed=\{isSelected\}/);
  assert.match(tagsPageSource, />已选择 \{selected\.size\} 项</);
  assert.match(tagsPageSource, /全选本页/);
  assert.match(tagsPageSource, /取消选中/);
  assert.match(tagsPageSource, /删除选中/);
  assert.match(tagsPageSource, /admin-tags-bulk-actions__mobile-exit/);
  assert.match(tagsPageSource, />\s*退出选择\s*<\/button>/);
  assert.doesNotMatch(tagsPageSource, /<Trash2 size=\{13\} \/> \{bulkDeleting \? "删除中\.\.\." : "删除选中"\}/);
  assert.doesNotMatch(tagsPageSource, /className="admin-btn is-danger admin-tags-bulk-actions__btn"/);
  assert.doesNotMatch(tagsPageSource, /全选本页 \(/);
  assert.doesNotMatch(tagsPageSource, /CheckSquare/);
  assert.doesNotMatch(tagsPageSource, /admin-tag-card__check/);
  assert.doesNotMatch(tagsPageSource, /admin-tags-bulk-actions__select-all/);
  assert.doesNotMatch(tagsPageSource, /checked=\{allSelected\}/);
  assert.doesNotMatch(tagsPageSource, /admin-tags-bulkbar/);
  assert.doesNotMatch(adminCss, /admin-tags-bulkbar/);
  assert.doesNotMatch(adminCss, /admin-tag-card__check/);
  assert.doesNotMatch(adminCss, /admin-tags-bulk-actions__btn\.is-danger/);
  assert.match(pageWithBulk, /padding-bottom\s*:\s*72px/);
  assert.match(toolbar, /position\s*:\s*fixed/);
  assert.match(toolbar, /right\s*:\s*var\(--space-7\)/);
  assert.match(toolbar, /bottom\s*:\s*var\(--space-5\)/);
  assert.match(toolbar, /margin\s*:\s*0/);
  assert.match(actions, /display\s*:\s*inline-flex/);
  assert.match(count, /white-space\s*:\s*nowrap/);
  assert.match(mobileToolbar, /left\s*:\s*var\(--space-3\)/);
  assert.match(mobileToolbar, /right\s*:\s*var\(--space-3\)/);
  assert.match(mobileToolbar, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(mobilePageWithBulk, /padding-bottom\s*:\s*calc\(144px\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(css, /\.admin-tags-page\.has-bulk-actions \.admin-tags-toolbar-actions\s*\{[^}]*display\s*:\s*none/s);
  assert.match(mobileActions, /display\s*:\s*grid/);
  assert.match(mobileActions, /grid-template-columns\s*:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(count, /grid-column\s*:\s*1 \/ -1/);
  assert.match(mobileButton, /min-height\s*:\s*40px/);
  assert.match(mobileButton, /min-width\s*:\s*0/);
  assert.match(adminCss, /\.admin-tags-bulk-actions__mobile-exit\s*\{[^}]*display\s*:\s*none/s);
  assert.match(mobileExit, /display\s*:\s*inline-flex/);
});

test("admin tag auto-generation setting is removed", () => {
  assert.doesNotMatch(tagsPageSource, /admin-tag-setting-toggle__switch/);
  assert.doesNotMatch(tagsPageSource, /role="switch"/);
  assert.doesNotMatch(tagsPageSource, /onClick=\{toggleAutoGenerateTags\}/);
  assert.doesNotMatch(tagsPageSource, /autoGenerateTagsEnabled/);
  assert.doesNotMatch(tagsPageSource, /admin-tag-setting-toggle__hint/);
  assert.doesNotMatch(tagsPageSource, /admin-tag-setting-toggle__body/);
  assert.doesNotMatch(tagsPageSource, /<label\s+className=\{`admin-tag-setting-toggle/);
  assert.doesNotMatch(adminCss, /admin-tag-setting-toggle/);
});

test("admin sidebar active item frame only wraps the centered option", () => {
  const nav = ruleBody(adminCss, ".admin-nav");
  const navIcon = ruleBody(adminCss, ".admin-nav__icon");
  const navLink = ruleBody(adminCss, ".admin-nav__link");
  const activeMarker = ruleBody(adminCss, ".admin-nav__link.is-active::before");

  assert.match(nav, /justify-content\s*:\s*space-evenly/);
  assert.match(nav, /gap\s*:\s*var\(--space-4\)/);
  assert.match(navIcon, /display\s*:\s*none/);
  assert.match(navLink, /align-self\s*:\s*center/);
  assert.match(navLink, /width\s*:\s*fit-content/);
  assert.match(navLink, /max-width\s*:\s*100%/);
  assert.match(activeMarker, /content\s*:\s*none/);
  assert.match(activeMarker, /display\s*:\s*none/);
});

test("admin sidebar separates adjacent options across every mobile group", () => {
  assert.match(
    adminCss,
    /\.admin-nav__group > \.admin-nav__link \+ \.admin-nav__link::after\s*\{[^}]*content\s*:\s*"";[^}]*height\s*:\s*1px;[^}]*background\s*:\s*var\(--border-subtle\)/s
  );
  const css = mobileCss();
  const mobileSeparator = ruleBody(css, ".admin-nav__group > .admin-nav__link + .admin-nav__link::after");
  const mobileGroupSeparator = ruleBody(
    css,
    ".admin-nav__group:not(.admin-nav__group--home) + .admin-nav__group > .admin-nav__link:first-of-type::after"
  );
  assert.match(mobileSeparator, /width\s*:\s*1px/);
  assert.match(mobileSeparator, /height\s*:\s*18px/);
  assert.match(mobileGroupSeparator, /content\s*:\s*""/);
  assert.match(mobileGroupSeparator, /width\s*:\s*1px/);
  assert.match(mobileGroupSeparator, /height\s*:\s*18px/);
  assert.match(mobileGroupSeparator, /background\s*:\s*var\(--border-subtle\)/);
});

test("current video list does not render the drive summary under filters", () => {
  const shell = ruleBody(adminCss, ".admin-shell");
  const navGroupLabel = ruleBody(adminCss, ".admin-nav__group-label");
  const navLink = ruleBody(adminCss, ".admin-nav__link");
  const navText = ruleBody(adminCss, ".admin-nav__text");
  const pagination = ruleBody(adminCss, ".admin-table-pagination");
  const filter = ruleBody(adminCss, ".admin-videos-filter");
  const toolbar = ruleBody(adminCss, ".admin-videos-list-toolbar");
  const currentToolbar = ruleBodyByContains(adminCss, ".admin-videos-current .admin-videos-list-toolbar");
  const currentWithBulk = ruleBodyByContains(adminCss, ".admin-videos-current.has-bulk-actions");

  assert.doesNotMatch(videosPageSource, /listSummary/);
  assert.doesNotMatch(videosPageSource, /全部网盘：共/);
  assert.doesNotMatch(videosPageSource, /withCounts/);
  assert.doesNotMatch(videosPageSource, /teaserReadyCount|teaserPendingCount/);
  assert.match(videosPageSource, /admin-videos-filter admin-videos-filter--current/);
  assert.doesNotMatch(videosPageSource, /aria-label="刷新当前视频"/);
  assert.match(videosPageSource, /admin-videos-filter__batch admin-videos-filter__batch-select\$\{selectMode \? " is-primary" : ""\}/);
  assert.match(videosPageSource, /selectMode \? "退出选择" : "批量选择"/);
  assert.match(videosPageSource, /admin-videos-current\$\{selectMode \? " has-bulk-actions" : ""\}/);
  assert.match(videosPageSource, /\{!loading && selectMode && \(/);
  assert.match(shell, /--admin-sidebar-width\s*:\s*232px/);
  assert.match(shell, /grid-template-columns\s*:\s*var\(--admin-sidebar-width\)\s+minmax\(0,\s*1fr\)/);
  assert.match(navGroupLabel, /padding\s*:\s*0\s+12px/);
  assert.match(navGroupLabel, /text-align\s*:\s*left/);
  assert.match(navLink, /display\s*:\s*flex/);
  assert.match(navLink, /justify-content\s*:\s*flex-start/);
  assert.match(navLink, /text-align\s*:\s*left/);
  assert.match(navText, /justify-items\s*:\s*start/);
  assert.match(pagination, /justify-content\s*:\s*center/);
  assert.match(filter, /margin-bottom\s*:\s*var\(--space-4\)/);
  assert.match(toolbar, /margin\s*:\s*var\(--space-2\)\s+0\s+var\(--space-4\)/);
  assert.match(currentToolbar, /position\s*:\s*fixed/);
  assert.match(currentToolbar, /left\s*:\s*auto/);
  assert.match(currentToolbar, /right\s*:\s*var\(--space-7\)/);
  assert.match(currentToolbar, /bottom\s*:\s*var\(--space-5\)/);
  assert.match(currentToolbar, /max-width\s*:\s*calc\(100vw\s*-\s*var\(--admin-sidebar-width\)\s*-\s*\(var\(--space-7\)\s*\*\s*2\)\)/);
  assert.match(currentToolbar, /margin\s*:\s*0/);
  assert.match(currentWithBulk, /padding-bottom\s*:\s*72px/);
});

test("desktop current video list uses long cards without a header row", () => {
  const table = ruleBody(adminCss, ".admin-videos-current .admin-videos-table");
  const body = ruleBody(adminCss, ".admin-videos-current .admin-videos-table tbody");
  const row = ruleBody(adminCss, ".admin-videos-current .admin-videos-table tr");
  const selected = ruleBody(adminCss, ".admin-videos-current .admin-videos-table tr.is-selected");
  const selectedCells = ruleBody(adminCss, ".admin-videos-current .admin-videos-table tr.is-selected td");
  const selectedButtons = ruleBody(adminCss, ".admin-videos-current .admin-videos-table tr.is-selected td.is-actions .admin-btn");
  const cell = ruleBody(adminCss, ".admin-videos-current .admin-videos-table td");
  const label = ruleBody(adminCss, ".admin-videos-current .admin-videos-table td::before");
  const titleCell = ruleBody(adminCss, ".admin-videos-current .admin-videos-table .admin-video-title-cell");
  const thumb = ruleBody(adminCss, ".admin-videos-current .admin-videos-table .admin-video-thumb-wrap");
  const actions = ruleBody(adminCss, ".admin-videos-current .admin-videos-table td.is-actions");

  assert.match(videosPageSource, /admin-table is-selectable admin-videos-table/);
  assert.match(videosPageSource, /<tbody>\s*\{\s*listItems\.map/);
  assert.doesNotMatch(videosPageSource, /<th>标题<\/th>/);
  assert.doesNotMatch(videosPageSource, /<th>作者<\/th>/);
  assert.doesNotMatch(videosPageSource, /<th>时长<\/th>/);
  assert.doesNotMatch(videosPageSource, /<th>预览视频<\/th>/);
  assert.doesNotMatch(videosPageSource, /data-label="预览视频"[\s\S]*?<PreviewStatus/);
  assert.match(table, /display\s*:\s*block/);
  assert.match(table, /width\s*:\s*min\(100%,\s*780px\)/);
  assert.match(table, /margin-inline\s*:\s*auto/);
  assert.match(table, /background\s*:\s*transparent/);
  assert.match(body, /display\s*:\s*grid/);
  assert.match(body, /gap\s*:\s*10px/);
  assert.match(row, /display\s*:\s*grid/);
  assert.match(row, /grid-template-columns\s*:\s*[\s\S]*minmax\(280px,\s*1fr\)/);
  assert.doesNotMatch(row, /minmax\(96px/);
  assert.match(row, /min-height\s*:\s*80px/);
  assert.match(row, /border-radius\s*:\s*8px/);
  assert.match(selected, /background\s*:\s*color-mix\(in srgb,\s*var\(--bg-surface\) 94%,\s*var\(--accent\) 6%\)/);
  assert.match(selected, /box-shadow\s*:\s*var\(--shadow-sm\)/);
  assert.doesNotMatch(selected, /0 0 0 1px/);
  assert.match(selectedCells, /background\s*:\s*transparent\s*!important/);
  assert.match(selectedButtons, /background\s*:\s*transparent/);
  assert.match(cell, /background\s*:\s*transparent\s*!important/);
  assert.match(label, /content\s*:\s*none/);
  assert.match(titleCell, /width\s*:\s*100%/);
  assert.match(thumb, /width\s*:\s*96px/);
  assert.match(thumb, /height\s*:\s*54px/);
  assert.match(actions, /justify-content\s*:\s*flex-end/);
  assert.match(adminCss, /\.admin-videos-current \.admin-videos-table \.admin-video-title-tags,[\s\S]*display\s*:\s*none/);
  assert.match(adminCss, /\.admin-videos-current \.admin-videos-table td\[data-label="作者"\],[\s\S]*display\s*:\s*none/);
  assert.match(adminCss, /\.admin-videos-current \.admin-videos-table td\[data-label="时长"\],[\s\S]*display\s*:\s*none/);
  assert.match(adminCss, /\.admin-videos-current \.admin-videos-table td\[data-label="来源"\][\s\S]*display\s*:\s*none/);
});

test("desktop video management toolbar follows tag management layout", () => {
  const css = adminCss;
  const currentFilter = ruleBodyByContains(css, ".admin-videos-filter--current");
  const blacklistFilter = ruleBodyByContains(css, ".admin-videos-filter--blacklist");
  const currentFilterSearch = ruleBodyByContains(css, ".admin-videos-filter--current .admin-videos-filter__search");
  const blacklistFilterSearch = ruleBodyByContains(css, ".admin-videos-filter--blacklist .admin-videos-filter__search");
  const searchInput = ruleBody(css, ".admin-videos-filter__search input");
  const actions = ruleBody(css, ".admin-videos-filter__actions");
  const batch = ruleBody(css, ".admin-videos-filter__batch");
  const tabs = ruleBody(css, ".admin-video-tabs");
  const tab = ruleBody(css, ".admin-video-tab");
  const activeTab = ruleBody(css, ".admin-video-tab.is-active");

  assert.doesNotMatch(videosPageSource, /aria-label="刷新当前视频"/);
  assert.doesNotMatch(videosPageSource, /aria-label="刷新拉黑视频"/);
  assert.match(videosPageSource, /admin-videos-filter__batch/);
  assert.doesNotMatch(videosPageSource, /CheckSquare/);
  assert.match(currentFilter, /display\s*:\s*grid/);
  assert.match(currentFilter, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)\s+minmax\(240px,\s*360px\)\s+minmax\(0,\s*1fr\)/);
  assert.match(currentFilter, /width\s*:\s*100%/);
  assert.match(blacklistFilter, /display\s*:\s*grid/);
  assert.match(blacklistFilter, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)\s+minmax\(240px,\s*360px\)\s+minmax\(0,\s*1fr\)/);
  assert.match(blacklistFilter, /width\s*:\s*100%/);
  assert.match(videosPageSource, /admin-videos-filter--current[\s\S]*?\{tabSelector\}/);
  assert.match(videosPageSource, /admin-videos-filter--blacklist[\s\S]*?\{tabSelector\}/);
  assert.match(currentFilterSearch, /min-width\s*:\s*0/);
  assert.match(currentFilterSearch, /grid-column\s*:\s*2/);
  assert.match(currentFilterSearch, /max-width\s*:\s*360px/);
  assert.match(blacklistFilterSearch, /min-width\s*:\s*0/);
  assert.match(blacklistFilterSearch, /grid-column\s*:\s*2/);
  assert.match(blacklistFilterSearch, /max-width\s*:\s*360px/);
  assert.match(searchInput, /padding\s*:\s*8px\s+32px/);
  assert.match(searchInput, /text-align\s*:\s*center/);
  assert.match(searchInput, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(actions, /grid-column\s*:\s*3/);
  assert.match(actions, /display\s*:\s*inline-flex/);
  assert.match(actions, /justify-content\s*:\s*flex-end/);
  assert.match(batch, /grid-column\s*:\s*3/);
  assert.match(batch, /justify-self\s*:\s*end/);
  assert.match(batch, /white-space\s*:\s*nowrap/);
  assert.doesNotMatch(batch, /display\s*:\s*none/);
  assert.match(tabs, /background\s*:\s*var\(--bg-sunken\)/);
  assert.match(tabs, /padding\s*:\s*3px/);
  assert.match(tabs, /border-radius\s*:\s*var\(--radius-sm\)/);
  assert.match(tab, /padding\s*:\s*6px\s+12px/);
  assert.match(tab, /font-size\s*:\s*var\(--font-xs\)/);
  assert.match(activeTab, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(activeTab, /color\s*:\s*var\(--accent\)/);
});

test("admin table action headers center-align with action buttons", () => {
  const actionHeader = ruleBody(adminCss, ".admin-table th.is-actions");
  const actionCell = ruleBody(adminCss, ".admin-table td.is-actions");

  assert.match(actionHeader, /text-align\s*:\s*center/);
  assert.match(actionCell, /text-align\s*:\s*center/);
});

test("current video delete dialogs use flat modal chrome", () => {
  const flatModal = ruleBodyByContains(adminCss, ".admin-modal--video-delete-flat");
  const flatModalChrome = ruleBodyByContains(adminCss, ".admin-modal--video-delete-flat .admin-modal__header");
  const deleteSourceOption = ruleBody(adminCss, ".admin-delete-source-option");

  assert.equal(
    Array.from(videosPageSource.matchAll(/modalClassName="admin-modal--delete-confirm admin-modal--video-delete-flat"/g)).length,
    2
  );
  assert.match(flatModal, /border\s*:\s*0/);
  assert.match(flatModal, /box-shadow\s*:\s*none/);
  assert.match(flatModalChrome, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(flatModalChrome, /border\s*:\s*0/);
  assert.doesNotMatch(videosPageSource, /开启后会先删除源文件，失败则不会删除管理库记录。/);
  assert.doesNotMatch(videosPageSource, /开启后会先删除源文件，失败的视频会保留管理库记录。/);
  assert.match(videosPageSource, /title="删除视频"[\s\S]*?confirmText="确认"/);
  assert.match(videosPageSource, /title="批量删除视频"[\s\S]*?confirmText="确认"/);
  assert.doesNotMatch(videosPageSource, /confirmText="删除视频"|confirmText="批量删除"/);
  assert.match(deleteSourceOption, /padding\s*:\s*0/);
  assert.match(deleteSourceOption, /border\s*:\s*0/);
  assert.match(deleteSourceOption, /background\s*:\s*transparent/);
});

test("blacklist cancel action uses ordinary button styling", () => {
  const unavailable = ruleBody(adminCss, ".admin-blacklist-unavailable");

  assert.doesNotMatch(videosPageSource, /const \[driveId, setDriveId\] = useState\(""\);/);
  assert.match(videosPageSource, /api\.listBlacklist\(\{ page, size: pageSize, keyword: searchKeyword \}\)/);
  assert.match(videosPageSource, /admin-videos-filter admin-videos-filter--blacklist/);
  assert.doesNotMatch(videosPageSource, /<DriveFilter/);
  assert.match(apiSource, /listBlacklist\(\s*params: \{ driveId\?: string; page\?: number; size\?: number; keyword\?: string \}/);
  assert.match(apiSource, /if \(params\.driveId\) qs\.set\("driveId", params\.driveId\);/);
  assert.match(videosPageSource, /className="admin-btn"[\s\S]*?onClick=\{\(\) => setRemoveTarget\(v\)\}[\s\S]*?取消拉黑/);
  assert.doesNotMatch(videosPageSource, /admin-blacklist-restore-btn/);
  assert.doesNotMatch(videosPageSource, /RotateCcw/);
  assert.doesNotMatch(adminCss, /admin-blacklist-restore-btn/);
  assert.match(videosPageSource, /v\.restorePolicy !== "none"/);
  assert.match(videosPageSource, /取消拉黑/);
  assert.doesNotMatch(videosPageSource, /重新入库/);
  assert.match(videosPageSource, /视频将在下次扫盘时恢复/);
  assert.doesNotMatch(videosPageSource, /此操作不会立即扫盘/);
  assert.match(videosPageSource, /此操作不会立即运行爬虫/);
  assert.match(videosPageSource, /v\.sourceDeleted/);
  assert.match(videosPageSource, /v\.driveId === "local-upload"/);
  assert.doesNotMatch(videosPageSource, /被删除和被隐藏的视频会进入黑名单/);
  assert.doesNotMatch(videosPageSource, /原始记录、封面、预览已删除/);
  assert.match(unavailable, /color\s*:\s*var\(--text-faint\)/);
});

test("blacklist duplicate reason renders as a compact pill", () => {
  const pill = ruleBody(adminCss, ".admin-blacklist-reason-pill");

  assert.match(videosPageSource, /admin-blacklist-reason-pill/);
  assert.match(videosPageSource, /重复文件/);
  assert.match(videosPageSource, /v\.canonicalVideoId/);
  assert.match(videosPageSource, /查看保留视频/);
  assert.doesNotMatch(videosPageSource, /保留视频不可用/);
  assert.doesNotMatch(videosPageSource, /admin-blacklist-canonical-btn/);
  assert.doesNotMatch(videosPageSource, /<ExternalLink[\s\S]*?查看保留视频/);
  assert.match(videosPageSource, /className="admin-btn"[\s\S]*?查看保留视频/);
  assert.match(pill, /border-radius\s*:\s*999px/);
  assert.match(pill, /white-space\s*:\s*nowrap/);
  assert.doesNotMatch(adminCss, /admin-blacklist-canonical-btn/);
});

test("blacklist source files can be deleted by one serialized background task", () => {
  const blacklistSource = videosPageSource.slice(
    videosPageSource.indexOf("function BlacklistTab"),
    videosPageSource.indexOf("function canDeleteBlacklistSource")
  );
  const table = ruleBody(adminCss, ".admin-blacklist-table");
  const flatModal = ruleBodyByContains(adminCss, ".admin-modal--source-delete-flat");
  const flatModalChrome = ruleBodyByContains(adminCss, ".admin-modal--source-delete-flat .admin-modal__header");
  const action = ruleBody(adminCss, ".admin-blacklist-source-delete");
  const status = ruleBody(adminCss, ".admin-blacklist-source-delete__status");
  const button = ruleBody(adminCss, ".admin-blacklist-source-delete__button");
  const rowActions = ruleBody(adminCss, ".admin-blacklist-actions");
  const rowDelete = ruleBody(adminCss, ".admin-blacklist-delete-source-btn");
  const deleteAllButtonStart = videosPageSource.indexOf("admin-blacklist-source-delete__button");
  const deleteAllButtonEnd = videosPageSource.indexOf("</button>", deleteAllButtonStart);
  const deleteAllButtonSource = videosPageSource.slice(deleteAllButtonStart, deleteAllButtonEnd);

  assert.match(apiSource, /startBlacklistSourceDelete/);
  assert.match(apiSource, /getBlacklistSourceDeleteStatus/);
  assert.match(apiSource, /ids\?: string\[\]/);
  assert.match(videosPageSource, /删除全部/);
  assert.match(videosPageSource, /批量删除/);
  assert.match(blacklistSource, /className="admin-btn admin-videos-bulk-actions__btn"[\s\S]*?>\s*批量删除\s*<\/button>/);
  assert.match(blacklistSource, /onClick=\{\(\) => setSelectedIds\(new Set\(\)\)\}[\s\S]*?disabled=\{selectedIds\.size === 0\}[\s\S]*?>\s*取消选中\s*<\/button>/);
  assert.match(blacklistSource, /admin-videos-bulk-actions__mobile-exit[\s\S]*?>\s*退出选择\s*<\/button>/);
  assert.doesNotMatch(blacklistSource, /className="admin-btn is-danger admin-videos-bulk-actions__btn"|<Trash2 size=\{13\} \/> 批量删除/);
  assert.match(videosPageSource, /title="删除源文件"/);
  assert.equal(Array.from(blacklistSource.matchAll(/confirmText="确认"/g)).length, 3);
  assert.doesNotMatch(videosPageSource, /confirmText="删除全部"|confirmText="删除"/);
  assert.doesNotMatch(videosPageSource, /<DeleteSourceNotice|function DeleteSourceNotice/);
  assert.doesNotMatch(adminCss, /admin-delete-source-option--notice/);
  assert.doesNotMatch(videosPageSource, /范围为整个黑名单，不受当前来源筛选或搜索条件影响。/);
  assert.doesNotMatch(videosPageSource, /此操作不可撤销；成功项会从黑名单和管理库中移除，失败项可再次执行重试。/);
  assert.doesNotMatch(videosPageSource, /成功后会从黑名单和管理库中移除。/);
  assert.doesNotMatch(videosPageSource, /失败时不会改变该拉黑记录，可稍后再次重试。/);
  assert.doesNotMatch(videosPageSource, /任务会在后台逐个删除，避免并发请求触发网盘限流。/);
  assert.doesNotMatch(videosPageSource, /成功项会从黑名单和管理库中移除，失败项可再次执行重试。/);
  assert.doesNotMatch(videosPageSource, /爬虫来源会保留已爬取标记，避免后续重复爬取。/);
  assert.equal(
    Array.from(videosPageSource.matchAll(/modalClassName="admin-modal--delete-confirm admin-modal--source-delete-flat"/g)).length,
    3
  );
  assert.notEqual(deleteAllButtonStart, -1);
  assert.notEqual(deleteAllButtonEnd, -1);
  assert.doesNotMatch(videosPageSource, /共 \{total\} 个拉黑视频/);
  assert.doesNotMatch(videosPageSource, /admin-videos-summary/);
  assert.match(videosPageSource, /admin-blacklist-source-delete__button[\s\S]*?\{sourceDeleteStatus\?\.running \? "删除中" : "删除全部"\}[\s\S]*?admin-videos-filter__batch admin-videos-filter__batch-select\$\{selectMode \? " is-primary" : ""\}/);
  assert.doesNotMatch(deleteAllButtonSource, /Trash2/);
  assert.doesNotMatch(deleteAllButtonSource, /is-danger/);
  assert.match(videosPageSource, /\{ ids: \[target\.id\] \}/);
  assert.match(videosPageSource, /\{ ids \}/);
  assert.match(videosPageSource, /admin-table is-selectable admin-blacklist-table\$\{selectMode \? " is-row-select-mode" : ""\}/);
  assert.doesNotMatch(videosPageSource, /<th>文件名<\/th>|<th>来源<\/th>|<th>大小<\/th>|<th>拉黑时间<\/th>|<th className="is-actions">操作<\/th>/);
  assert.doesNotMatch(videosPageSource, /data-label="大小"|data-label="拉黑时间"|formatDateTime/);
  assert.doesNotMatch(videosPageSource, /admin-table-checkbox-btn/);
  assert.match(videosPageSource, /已开始后台顺序删除/);
  assert.match(videosPageSource, /sourceDeleteStatus\.processed/);
  assert.match(videosPageSource, /sourceDeleteStatus\.total/);
  assert.match(table, /width\s*:\s*min\(100%,\s*860px\)/);
  assert.match(table, /margin-inline\s*:\s*auto/);
  assert.match(flatModal, /border\s*:\s*0/);
  assert.match(flatModal, /box-shadow\s*:\s*none/);
  assert.match(flatModalChrome, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(flatModalChrome, /border\s*:\s*0/);
  assert.match(action, /display\s*:\s*flex/);
  assert.match(status, /font-size\s*:\s*var\(--font-xs\)/);
  assert.match(button, /white-space\s*:\s*nowrap/);
  assert.match(rowActions, /display\s*:\s*flex/);
  // 桌面端黑名单操作列保持单行（8be7ebd）；移动端媒体查询里仍允许换行
  assert.match(rowActions, /flex-wrap\s*:\s*nowrap/);
  assert.match(rowDelete, /white-space\s*:\s*nowrap/);
});

test("admin video management controls wrap instead of covering text on mobile", () => {
  const css = mobileCss();
  const paginationInfo = allRuleBodies(css, ".admin-table-pagination__info");
  const currentFilter = ruleBody(css, ".admin-videos-filter--current");
  const currentFilterField = ruleBodyByContains(css, ".admin-videos-filter--current .admin-videos-filter__search");
  const currentFilterActions = ruleBodyByContains(
    css,
    ".admin-videos-filter--current .admin-videos-filter__current-actions"
  );
  const currentFilterActionButton = ruleBodyByContains(
    css,
    ".admin-videos-filter--current .admin-videos-filter__current-actions .admin-btn"
  );
  const currentFilterActionDivider = ruleBodyByContains(
    css,
    ".admin-videos-filter__current-actions"
  );
  const blacklistFilter = allRuleBodies(css, ".admin-videos-filter--blacklist");
  const blacklistFilterField = ruleBodyByContains(css, ".admin-videos-filter--blacklist .admin-videos-filter__search");
  const blacklistFilterActions = ruleBodyByContains(css, ".admin-videos-filter--blacklist .admin-videos-filter__actions");
  const blacklistFilterBatch = ruleBodyByContains(
    css,
    ".admin-videos-filter--blacklist .admin-videos-filter__actions .admin-videos-filter__batch"
  );
  const bulkToolbar = ruleBodyByContains(css, ".admin-videos-current .admin-videos-list-toolbar");
  const blacklistBulkToolbar = ruleBodyByContains(css, ".admin-blacklist-bulk-toolbar");
  const currentWithBulk = ruleBodyByContains(css, ".admin-videos-current.has-bulk-actions");
  const blacklistWithBulk = ruleBodyByContains(css, ".admin-videos-blacklist.has-bulk-actions");
  const bulkActions = allRuleBodies(css, ".admin-videos-bulk-actions");
  const bulkCount = allRuleBodies(css, ".admin-videos-bulk-actions__count");
  const bulkButton = allRuleBodies(css, ".admin-videos-bulk-actions__btn");
  const bulkMobileExit = allRuleBodies(css, ".admin-videos-bulk-actions__mobile-exit");
  const blacklistBulkButton = ruleBody(css, ".admin-blacklist-bulk-toolbar .admin-videos-bulk-actions__btn");
  const blacklistMobileExit = ruleBody(css, ".admin-blacklist-bulk-toolbar .admin-videos-bulk-actions__mobile-exit");
  const blacklistName = ruleBody(
    css,
    '.admin-blacklist-table:not(.admin-drives-table) td[data-label="文件名"]'
  );
  const blacklistActions = ruleBody(
    css,
    ".admin-blacklist-table:not(.admin-drives-table) td.is-actions"
  );
  const blacklistActionsLabel = ruleBody(
    css,
    ".admin-blacklist-table:not(.admin-drives-table) td.is-actions::before"
  );
  const blacklistActionButton = ruleBody(
    css,
    ".admin-blacklist-table:not(.admin-drives-table) td.is-actions .admin-btn"
  );
  const blacklistCard = ruleBody(css, ".admin-blacklist-table:not(.admin-drives-table) tr");
  const blacklistSelected = ruleBody(css, ".admin-blacklist-table:not(.admin-drives-table) tr.is-selected");

  assert.match(paginationInfo, /flex\s*:\s*1\s+0\s+100%/);
  assert.match(currentFilter, /display\s*:\s*grid/);
  assert.match(currentFilter, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)/);
  assert.match(currentFilterField, /min-width\s*:\s*0/);
  assert.match(currentFilterActions, /position\s*:\s*fixed/);
  assert.match(currentFilterActions, /right\s*:\s*var\(--space-3\)/);
  assert.match(currentFilterActions, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(currentFilterActions, /width\s*:\s*max-content/);
  assert.match(currentFilterActions, /border\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(currentFilterActions, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(currentFilterActions, /overflow\s*:\s*hidden/);
  assert.match(currentFilterActionButton, /position\s*:\s*relative/);
  assert.match(currentFilterActionButton, /min-height\s*:\s*44px/);
  assert.match(currentFilterActionButton, /min-width\s*:\s*76px/);
  assert.match(currentFilterActionButton, /border\s*:\s*0/);
  assert.match(currentFilterActionButton, /border-radius\s*:\s*0/);
  assert.match(currentFilterActionButton, /background\s*:\s*transparent/);
  assert.match(currentFilterActionButton, /box-shadow\s*:\s*none/);
  assert.match(currentFilterActionDivider, /content\s*:\s*""/);
  assert.match(currentFilterActionDivider, /height\s*:\s*18px/);
  assert.match(blacklistFilter, /display\s*:\s*grid/);
  assert.match(blacklistFilter, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)/);
  assert.match(blacklistFilterField, /min-width\s*:\s*0/);
  assert.match(blacklistFilterActions, /position\s*:\s*fixed/);
  assert.match(blacklistFilterActions, /right\s*:\s*var\(--space-3\)/);
  assert.match(blacklistFilterActions, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(blacklistFilterActions, /grid-column\s*:\s*auto/);
  assert.match(blacklistFilterActions, /width\s*:\s*max-content/);
  assert.match(blacklistFilterActions, /max-width\s*:\s*calc\(100vw\s*-\s*\(var\(--space-3\)\s*\*\s*2\)\)/);
  assert.match(blacklistFilterActions, /border\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(blacklistFilterActions, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(blacklistFilterActions, /overflow\s*:\s*hidden/);
  assert.match(blacklistFilterBatch, /position\s*:\s*relative/);
  assert.match(blacklistFilterBatch, /min-height\s*:\s*44px/);
  assert.match(blacklistFilterBatch, /border\s*:\s*0/);
  assert.match(blacklistFilterBatch, /border-radius\s*:\s*0/);
  assert.match(blacklistFilterBatch, /background\s*:\s*transparent/);
  assert.match(blacklistFilterBatch, /box-shadow\s*:\s*none/);
  assert.match(blacklistFilterBatch, /white-space\s*:\s*nowrap/);
  assert.match(css, /\.admin-videos-current\.has-bulk-actions \.admin-videos-filter__current-actions,[\s\S]*?\.admin-videos-blacklist\.has-bulk-actions \.admin-videos-filter__actions\s*\{[^}]*display\s*:\s*none/s);
  assert.match(bulkToolbar, /position\s*:\s*fixed/);
  assert.match(bulkToolbar, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(bulkToolbar, /margin\s*:\s*0/);
  assert.match(blacklistBulkToolbar, /position\s*:\s*fixed/);
  assert.match(blacklistBulkToolbar, /bottom\s*:\s*calc\(var\(--space-3\)\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(blacklistBulkToolbar, /margin\s*:\s*0/);
  assert.match(currentWithBulk, /padding-bottom\s*:\s*calc\(144px\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(blacklistWithBulk, /padding-bottom\s*:\s*calc\(144px\s*\+\s*env\(safe-area-inset-bottom\)\)/);
  assert.match(bulkActions, /display\s*:\s*grid/);
  assert.match(bulkActions, /grid-template-columns\s*:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(bulkCount, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(bulkButton, /min-height\s*:\s*40px/);
  assert.match(bulkButton, /min-width\s*:\s*0/);
  assert.match(bulkMobileExit, /display\s*:\s*inline-flex/);
  assert.match(adminCss, /\.admin-videos-bulk-actions__mobile-exit\s*\{[^}]*display\s*:\s*none/s);
  assert.match(blacklistBulkButton, /grid-column\s*:\s*auto/);
  assert.match(blacklistMobileExit, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(videosPageSource, /admin-videos-blacklist/);
  assert.match(blacklistName, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(blacklistActions, /grid-column\s*:\s*2/);
  assert.match(blacklistCard, /--admin-video-card-bg\s*:\s*var\(--bg-surface\)/);
  assert.match(blacklistSelected, /background\s*:\s*var\(--admin-video-card-bg\)/);
  assert.match(blacklistSelected, /box-shadow\s*:\s*var\(--admin-video-card-selected-shadow\)/);
  assert.match(blacklistActions, /justify-content\s*:\s*flex-end/);
  assert.match(blacklistActionsLabel, /content\s*:\s*none/);
  assert.match(blacklistActionButton, /white-space\s*:\s*normal/);
});

test("admin loading spinner rotates around icon center", () => {
  const spinner = ruleBody(adminCss, ".admin-spin");
  const pageLoading = ruleBody(adminCss, ".admin-loading");

  assert.match(spinner, /animation\s*:\s*admin-update-spin\s+0\.9s\s+linear\s+infinite/);
  assert.match(spinner, /transform-box\s*:\s*fill-box/);
  assert.match(spinner, /transform-origin\s*:\s*center/);
  assert.match(spinner, /will-change\s*:\s*transform/);
  assert.match(pageLoading, /min-height\s*:\s*calc\(100dvh - 64px\)/);
  assert.match(usersPageSource, /<AdminLoading \/>/);
  assert.match(tagsPageSource, /<AdminLoading \/>/);
  assert.match(crawlersPageSource, /<AdminLoading \/>/);
  assert.match(videosPageSource, /<AdminLoading \/>/);
  assert.equal(
    Array.from(drivesPageSource.matchAll(/<AdminLoading \/>/g)).length,
    2
  );
  assert.match(adminCss, /@media \(prefers-reduced-motion: reduce\)\s*\{\s*\.admin-spin\s*\{\s*animation-duration\s*:\s*0\.9s\s*!important/s);
});

test("mobile video management uses compact theme-aware video cards", () => {
  const css = mobileCss();
  const card = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) tr");
  const title = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td[data-label=\"标题\"]");
  const label = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td::before");
  const titleCell = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) .admin-video-title-cell");
  const thumb = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) .admin-video-thumb-wrap");
  const titleText = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) .admin-video-title");
  const pills = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) .admin-video-filemeta-pills");
  const authorColumn = ruleBodyByContains(css, ".admin-videos-table:not(.admin-drives-table) td[data-label=\"作者\"]");
  const sourceColumn = ruleBodyByContains(css, ".admin-videos-table:not(.admin-drives-table) td[data-label=\"来源\"]");
  const durationColumn = ruleBodyByContains(css, ".admin-videos-table:not(.admin-drives-table) td[data-label=\"时长\"]");
  const actions = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td.is-actions");
  const actionsLabel = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td.is-actions::before");
  const actionButton = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td.is-actions .admin-btn");
  const dangerButton = ruleBody(css, ".admin-videos-table:not(.admin-drives-table) td.is-actions .admin-btn.is-danger");
  const selectedCard = ruleBodyByContains(
    css,
    ".admin-videos-table:not(.admin-drives-table).is-row-select-mode"
  );

  assert.match(card, /--admin-video-card-bg\s*:\s*var\(--bg-surface\)/);
  assert.match(card, /background\s*:\s*var\(--admin-video-card-bg\)/);
  assert.match(card, /border-radius\s*:\s*14px/);
  assert.match(card, /padding\s*:\s*12px\s+14px/);
  assert.match(card, /grid-template-columns\s*:\s*repeat\(12,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(card, /gap\s*:\s*0\s+10px/);
  assert.match(css, /:root:not\(\[data-theme="pink"\]\)\s+\.admin-videos-table:not\(\.admin-drives-table\)\s+tr\s*\{[^}]*--admin-video-card-bg\s*:\s*#1e1e1e/s);
  assert.match(css, /:root\[data-theme="pink"\]\s+\.admin-videos-table:not\(\.admin-drives-table\)\s+tr\s*\{/);
  assert.doesNotMatch(videosPageSource, /className="is-checkbox"/);
  assert.doesNotMatch(videosPageSource, /admin-table-checkbox-btn/);
  assert.match(title, /padding-left\s*:\s*0/);
  assert.match(title, /min-height\s*:\s*72px/);
  assert.match(label, /font-size\s*:\s*10px/);
  assert.match(label, /letter-spacing\s*:\s*0\.06em/);
  assert.match(titleCell, /grid-template-columns\s*:\s*clamp\(104px,\s*32vw,\s*156px\)\s+minmax\(0,\s*1fr\)/);
  assert.match(thumb, /aspect-ratio\s*:\s*16\s*\/\s*9/);
  assert.match(thumb, /border-radius\s*:\s*8px/);
  assert.match(titleText, /-webkit-line-clamp\s*:\s*2/);
  assert.match(titleText, /overflow-wrap\s*:\s*anywhere/);
  assert.match(videosPageSource, /loading="lazy"\s+decoding="async"/);
  assert.match(videosPageSource, /className="admin-video-title" title=\{v\.title\}/);
  assert.match(pills, /display\s*:\s*flex/);
  assert.doesNotMatch(videosPageSource, /admin-video-filemeta-pill is-category/);
  assert.doesNotMatch(css, /admin-video-card-category/);
  assert.match(authorColumn, /display\s*:\s*none/);
  assert.match(sourceColumn, /grid-row\s*:\s*2/);
  assert.match(sourceColumn, /grid-column\s*:\s*1\s*\/\s*7/);
  assert.match(sourceColumn, /justify-items\s*:\s*start/);
  assert.match(sourceColumn, /text-overflow\s*:\s*ellipsis/);
  assert.match(durationColumn, /grid-row\s*:\s*2/);
  assert.match(durationColumn, /grid-column\s*:\s*7\s*\/\s*-1/);
  assert.match(durationColumn, /justify-items\s*:\s*end/);
  assert.doesNotMatch(videosPageSource, /data-label="预览视频"[\s\S]*?<PreviewStatus/);
  assert.doesNotMatch(css, /\.admin-videos-table:not\(\.admin-drives-table\) td\[data-label="预览视频"\]/);
  assert.match(actions, /grid-column\s*:\s*1\s*\/\s*-1/);
  assert.match(actions, /grid-row\s*:\s*3/);
  assert.match(actions, /display\s*:\s*grid/);
  assert.match(actions, /grid-template-columns\s*:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(actions, /gap\s*:\s*10px/);
  assert.match(actionsLabel, /content\s*:\s*none/);
  assert.doesNotMatch(sourceColumn, /border-left/);
  assert.match(actionButton, /width\s*:\s*100%/);
  assert.match(actionButton, /height\s*:\s*32px/);
  assert.match(actionButton, /justify-content\s*:\s*center/);
  assert.match(actionButton, /border-radius\s*:\s*8px/);
  assert.match(dangerButton, /border-color\s*:\s*var\(--admin-video-card-danger-border\)/);
  assert.match(dangerButton, /color\s*:\s*var\(--admin-video-card-danger\)/);
  assert.match(
    css,
    /\.admin-videos-table:not\(\.admin-drives-table\)\.is-row-select-mode\s+tr\.is-selected,\s*\.admin-blacklist-table:not\(\.admin-drives-table\)\.is-row-select-mode\s+tr\.is-selected\s*\{/s
  );
  assert.doesNotMatch(css, /:root\[data-theme="sky"\][^{}]*\.admin-videos-table[^{}]*\.is-row-select-mode/);
  assert.match(selectedCard, /background\s*:\s*color-mix\(in srgb,\s*var\(--admin-video-card-bg\) 88%,\s*var\(--accent\) 12%\)/);
  assert.match(selectedCard, /border-color\s*:\s*var\(--accent\)/);
  assert.match(selectedCard, /0 0 0 1px var\(--accent\)/);
  assert.doesNotMatch(selectedCard, /0 0 0 3px/);
  assert.match(selectedCard, /var\(--shadow-md\)/);
});

test("video edit modal stays focused on common metadata", () => {
  const editModalSource = videosPageSource.slice(
    videosPageSource.indexOf("function EditVideoModal"),
    videosPageSource.indexOf("function tagAssignmentSourceLabel")
  );
  const editModal = ruleBody(adminCss, ".admin-modal--video-edit");
  const editModalHeader = ruleBody(adminCss, ".admin-modal--video-edit .admin-modal__header");
  const editModalFooter = allRuleBodies(adminCss, ".admin-modal--video-edit .admin-modal__footer");
  const editTagPicker = ruleBody(adminCss, ".admin-modal--video-edit .admin-video-tag-picker");
  const selectedTagOption = ruleBody(adminCss, ".admin-modal--video-edit .admin-video-tag-option:has(input:checked)");
  const editBasics = ruleBody(adminCss, ".admin-video-edit-basics");
  const editMeta = ruleBody(adminCss, ".admin-video-edit-meta");
  const footerActions = ruleBody(adminCss, ".admin-video-edit-footer-actions");
  const previewActions = allRuleBodies(adminCss, ".admin-video-preview-actions");
  const previewButton = ruleBody(adminCss, ".admin-video-preview-button");
  const viewVideoLink = ruleBody(adminCss, ".admin-video-edit-view-link");
  const previewStatusDot = ruleBody(adminCss, ".admin-modal--video-edit .admin-video-preview-actions .admin-status::before");

  assert.match(videosPageSource, /ariaLabel="编辑视频"/);
  assert.match(editModalSource, /title="编辑视频"/);
  assert.match(editModalSource, /className="admin-modal--video-edit"/);
  assert.match(editModalSource, /className="admin-btn admin-video-edit-view-link"[\s\S]*?to=\{`\/video\/\$\{encodeURIComponent\(video\.id\)\}`\}[\s\S]*?target="_blank"[\s\S]*?rel="noreferrer"[\s\S]*?>\s*查看视频播放页\s*<\/Link>/);
  assert.doesNotMatch(videosPageSource, /title=\{`编辑视频 ·/);
  assert.doesNotMatch(videosPageSource, /const \[badges, setBadges\]/);
  assert.doesNotMatch(videosPageSource, /const \[thumbnail, setThumbnail\]/);
  assert.doesNotMatch(videosPageSource, /const \[quality, setQuality\]/);
  assert.doesNotMatch(videosPageSource, /video-badges/);
  assert.doesNotMatch(videosPageSource, /video-quality/);
  assert.doesNotMatch(videosPageSource, /video-thumbnail/);
  assert.doesNotMatch(videosPageSource, /徽标（/);
  assert.doesNotMatch(videosPageSource, /封面 URL/);
  assert.doesNotMatch(videosPageSource, /封面预览/);
  assert.doesNotMatch(videosPageSource, /badges:\s*splitList\(badges\)/);
  assert.doesNotMatch(videosPageSource, /thumbnail:\s*thumbnail\.trim\(\)/);
  assert.doesNotMatch(videosPageSource, /quality:\s*quality\.trim\(\)/);
  assert.match(editModalSource, /id=\{`\$\{idPrefix\}-video-title`\}[\s\S]*?value=\{video\.title\}[\s\S]*?readOnly/);
  assert.match(editModalSource, /id=\{`\$\{idPrefix\}-video-author`\}[\s\S]*?value=\{video\.author \?\? ""\}[\s\S]*?readOnly/);
  assert.doesNotMatch(editModalSource, /title:\s*title\.trim\(\)/);
  assert.doesNotMatch(editModalSource, /author:\s*author\.trim\(\)/);
  assert.doesNotMatch(editModalSource, /video-description|video-duration|技术信息（排查用）|内部视频 ID|网盘文件 ID/);
  assert.doesNotMatch(editModalSource, /const \[description, setDescription\]|const \[durationSec, setDurationSec\]/);
  assert.doesNotMatch(editModalSource, /description,|durationSeconds:/);
  assert.match(editModal, /width\s*:\s*min\(680px,\s*100%\)/);
  assert.match(editModal, /border\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(editModal, /box-shadow\s*:\s*var\(--shadow-xl\)/);
  assert.match(editModalHeader, /border-bottom\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(editModalFooter, /border-top\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(editTagPicker, /border\s*:\s*0/);
  assert.match(editTagPicker, /background\s*:\s*transparent/);
  assert.match(editModalSource, /<section className="admin-video-edit-section">\s*<h3>基本信息<\/h3>[\s\S]*?<h3>标签<\/h3>[\s\S]*?<h3>视频信息<\/h3>/);
  assert.match(editBasics, /grid-template-columns\s*:\s*minmax\(0,\s*2fr\) minmax\(160px,\s*1fr\)/);
  assert.match(selectedTagOption, /background\s*:\s*var\(--accent-soft\)/);
  assert.match(editMeta, /grid-template-columns\s*:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)/);
  assert.match(editMeta, /background\s*:\s*color-mix/);
  assert.match(footerActions, /display\s*:\s*flex/);
  assert.doesNotMatch(editModalSource, /admin-video-tag-option__count|\{tag\.count\}/);
  assert.doesNotMatch(adminCss, /admin-video-tag-option__count/);
  assert.doesNotMatch(editModalSource, /admin-video-tag-option__source|video\.tagSources\?\.\[tag\.label\]/);
  assert.doesNotMatch(adminCss, /admin-video-tag-option__source/);
  assert.match(previewActions, /display\s*:\s*flex/);
  assert.match(previewActions, /align-items\s*:\s*center/);
  assert.match(previewActions, /gap\s*:\s*var\(--space-5\)/);
  assert.match(previewButton, /padding\s*:\s*5px\s+9px/);
  assert.match(previewButton, /font-size\s*:\s*var\(--font-xs\)/);
  assert.match(previewStatusDot, /content\s*:\s*none/);
  assert.match(previewStatusDot, /display\s*:\s*none/);
  assert.match(viewVideoLink, /margin-right\s*:\s*auto/);
  assert.match(viewVideoLink, /white-space\s*:\s*nowrap/);
  assert.match(editModalSource, /<dt>预览视频<\/dt>\s*<dd className="admin-video-preview-actions">\s*<PreviewStatus[\s\S]*?className="admin-btn admin-video-preview-button"[\s\S]*?<\/button>/);
  assert.doesNotMatch(editModalSource, /<RefreshCw size=\{13\} className=\{previewBusy/);
});

test("admin modals and action footers adapt on mobile", () => {
  const css = mobileCss();

  // .admin-modal 桌面段已用 `width: min(620px, 100%)`，窄屏自然 100%；mobile 段
  // 只重写 max-height，所以这里断桌面规则即可。
  assert.match(ruleBody(adminCss, ".admin-modal"), /width\s*:\s*min\(\d+px,\s*100%\)/);
  assert.match(ruleBody(adminCss, ".admin-modal.admin-modal--crawler"), /width\s*:\s*min\(1080px,\s*100%\)/);
  assert.match(allRuleBodies(css, ".admin-modal"), /display\s*:\s*flex/);
  assert.match(allRuleBodies(css, ".admin-modal"), /overflow\s*:\s*hidden/);
  assert.match(allRuleBodies(css, ".admin-modal__body"), /overflow-y\s*:\s*auto/);
  assert.match(allRuleBodies(css, ".admin-modal-backdrop"), /safe-area-inset-top/);
  assert.match(allRuleBodies(css, ".admin-modal-backdrop"), /place-items\s*:\s*center/);
  assert.doesNotMatch(allRuleBodies(css, ".admin-modal-backdrop"), /align-items\s*:\s*stretch/);
  // 多按钮 footer 在 mobile 下要换行避免溢出。
  assert.match(allRuleBodies(css, ".admin-modal__footer"), /flex-wrap\s*:\s*wrap/);
  // 删除/放弃类确认弹窗在 mobile 下不能跟随通用 modal stretch 到顶部。
  const confirmModal = ruleBody(css, ".admin-modal--delete-confirm");
  assert.match(confirmModal, /align-self\s*:\s*center/);
  assert.match(confirmModal, /justify-self\s*:\s*center/);
  assert.match(ruleBody(adminCss, ".admin-modal__header.is-titleless"), /justify-content\s*:\s*flex-end/);
  // 表单 input/select/textarea 在 mobile 下铺满。规则用逗号合并写法（多 selector
  // 共享 body），所以走 ruleBodyByContains 而不是简单正则。
  assert.match(ruleBodyByContains(css, ".admin-form__row input"), /width\s*:\s*100%/);
});

test("mobile drive type picker uses compact three-column cards", () => {
  const driveTypeGridBodies = allRuleBodies(adminCss, ".admin-drive-type-grid");
  const driveTypeCardBodies = allRuleBodies(adminCss, ".admin-drive-type-card");
  const driveTypeIconBodies = allRuleBodies(adminCss, ".admin-drive-type-card__icon");

  assert.match(driveTypeGridBodies, /grid-template-columns\s*:\s*repeat\(3,\s*minmax\(0,\s*1fr\)\)/);
  assert.doesNotMatch(driveTypeGridBodies, /grid-template-columns\s*:\s*repeat\(2,\s*1fr\)/);
  assert.match(driveTypeCardBodies, /min-height\s*:\s*94px/);
  assert.match(driveTypeIconBodies, /width\s*:\s*38px/);
  assert.match(driveTypeIconBodies, /height\s*:\s*38px/);
});

test("mobile tags management does not create horizontal page overflow", () => {
  const css = mobileCss();
  const layout = allRuleBodies(css, ".admin-tags-layout");
  const desktopLayout = allRuleBodies(adminCss, ".admin-tags-layout");
  const main = allRuleBodies(adminCss, ".admin-tags-main");
  const board = allRuleBodies(adminCss, ".admin-tags-board");
  const mobileBoard = allRuleBodies(css, ".admin-tags-board");
  const toolbar = allRuleBodies(css, ".admin-tags-toolbar");
  const search = allRuleBodies(css, ".admin-tags-search");
  const searchInput = ruleBody(adminCss, ".admin-tags-search input");
  const toolbarActions = allRuleBodies(css, ".admin-tags-toolbar-actions");
  const toolbarActionButton = allRuleBodies(css, ".admin-tags-toolbar-actions .admin-btn");
  const toolbarActionDivider = allRuleBodies(css, ".admin-tags-toolbar-actions .admin-btn + .admin-btn::before");
  const filters = allRuleBodies(css, ".admin-tags-filter-tabs");
  const desktopFilters = allRuleBodies(adminCss, ".admin-tags-filter-tabs");
  const filterPanel = allRuleBodies(css, ".admin-tags-filter-panel");
  const desktopFilterPanel = ruleBody(adminCss, ".admin-tags-filter-panel");
  const filterTab = allRuleBodies(adminCss, ".admin-tags-filter-tab");
  const filterTabText = allRuleBodies(adminCss, ".admin-tags-filter-tab__text");
  const mobileFilterTabText = allRuleBodies(css, ".admin-tags-filter-tab__text");
  const grid = allRuleBodies(css, ".admin-tags-grid");
  const card = allRuleBodies(css, ".admin-tag-card");
  const cardFooter = allRuleBodies(adminCss, ".admin-tag-card__footer");
  const cardCount = allRuleBodies(adminCss, ".admin-tag-card__count");
  const cardActions = allRuleBodies(adminCss, ".admin-tag-card__footer-actions");
  const cardEdit = allRuleBodies(adminCss, ".admin-tag-card__edit");
  const cardDelete = allRuleBodies(adminCss, ".admin-tag-card__delete");
  const pagination = allRuleBodies(css, ".admin-tags-pagination");
  const paginationInfo = allRuleBodies(css, ".admin-tags-pagination .admin-table-pagination__info");

  assert.match(desktopLayout, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)/);
  assert.match(main, /--tags-cards-width\s*:\s*calc\(\(240px \* 4\) \+ \(var\(--space-3\) \* 3\)\)/);
  assert.doesNotMatch(main, /--tags-search-width/);
  assert.doesNotMatch(board, /--tags-filter-width|--tags-board-width/);
  assert.match(board, /grid-template-columns\s*:\s*minmax\(0,\s*var\(--tags-cards-width\)\)/);
  assert.match(board, /justify-content\s*:\s*center/);
  assert.match(board, /align-items\s*:\s*stretch/);
  assert.match(layout, /width\s*:\s*100%/);
  assert.match(layout, /max-width\s*:\s*100%/);
  assert.match(layout, /overflow-x\s*:\s*clip/);
  assert.match(mobileBoard, /grid-template-columns\s*:\s*1fr/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-toolbar"), /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)\s+minmax\(240px,\s*360px\)\s+minmax\(0,\s*1fr\)/);
  assert.match(toolbar, /max-width\s*:\s*100%/);
  assert.match(toolbar, /grid-template-columns\s*:\s*1fr/);
  assert.match(toolbar, /justify-items\s*:\s*stretch/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-search"), /grid-column\s*:\s*2/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-search"), /grid-row\s*:\s*1/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-search"), /justify-self\s*:\s*center/);
  assert.match(ruleBody(adminCss, ".admin-tags-search"), /width\s*:\s*100%/);
  assert.match(ruleBody(adminCss, ".admin-tags-search"), /max-width\s*:\s*360px/);
  assert.match(searchInput, /padding\s*:\s*8px\s+32px/);
  assert.match(searchInput, /text-align\s*:\s*center/);
  assert.match(search, /grid-column\s*:\s*1/);
  assert.match(search, /grid-row\s*:\s*1/);
  assert.match(search, /width\s*:\s*100%/);
  assert.match(search, /min-width\s*:\s*0/);
  assert.match(search, /max-width\s*:\s*none/);
  assert.match(toolbarActions, /position\s*:\s*fixed/);
  assert.match(toolbarActions, /right\s*:\s*var\(--space-3\)/);
  assert.match(toolbarActions, /bottom\s*:\s*calc\(var\(--space-3\) \+ env\(safe-area-inset-bottom\)\)/);
  assert.match(toolbarActions, /width\s*:\s*max-content/);
  assert.match(toolbarActions, /max-width\s*:\s*calc\(100vw - \(var\(--space-3\) \* 2\)\)/);
  assert.match(toolbarActions, /padding\s*:\s*0/);
  assert.match(toolbarActions, /border\s*:\s*1px solid var\(--border-subtle\)/);
  assert.match(toolbarActions, /background\s*:\s*var\(--bg-surface\)/);
  assert.match(toolbarActions, /gap\s*:\s*0/);
  assert.match(toolbarActions, /overflow\s*:\s*hidden/);
  assert.match(toolbarActionButton, /min-height\s*:\s*44px/);
  assert.match(toolbarActionButton, /border\s*:\s*0/);
  assert.match(toolbarActionButton, /border-radius\s*:\s*0/);
  assert.match(toolbarActionButton, /background\s*:\s*transparent/);
  assert.match(toolbarActionButton, /box-shadow\s*:\s*none/);
  assert.match(toolbarActionDivider, /content\s*:\s*""/);
  assert.match(toolbarActionDivider, /height\s*:\s*18px/);
  assert.match(toolbarActionDivider, /background\s*:\s*var\(--border-subtle\)/);
  assert.match(css, /\.admin-tags-page\s*\{[^}]*padding-bottom\s*:\s*calc\(72px \+ env\(safe-area-inset-bottom\)\)/s);
  assert.match(css, /\.admin-tags-page\.has-bulk-actions \.admin-tags-toolbar-actions\s*\{[^}]*display\s*:\s*none/s);
  assert.match(desktopFilterPanel, /grid-column\s*:\s*2/);
  assert.match(desktopFilterPanel, /grid-row\s*:\s*2/);
  assert.match(desktopFilterPanel, /justify-self\s*:\s*center/);
  assert.match(desktopFilterPanel, /display\s*:\s*flex/);
  assert.match(desktopFilterPanel, /justify-content\s*:\s*center/);
  assert.match(desktopFilterPanel, /width\s*:\s*auto/);
  assert.match(desktopFilterPanel, /max-width\s*:\s*100%/);
  assert.match(desktopFilterPanel, /min-width\s*:\s*0/);
  assert.match(desktopFilterPanel, /margin\s*:\s*0/);
  assert.doesNotMatch(desktopFilterPanel, /position\s*:\s*(fixed|sticky)/);
  assert.doesNotMatch(desktopFilterPanel, /\bleft\s*:/);
  assert.doesNotMatch(desktopFilterPanel, /\btop\s*:/);
  assert.doesNotMatch(desktopFilterPanel, /transform\s*:/);
  assert.match(filterPanel, /grid-row\s*:\s*2/);
  assert.match(filterPanel, /width\s*:\s*max-content/);
  assert.match(filterPanel, /max-width\s*:\s*100%/);
  assert.match(desktopFilters, /display\s*:\s*flex/);
  assert.match(desktopFilters, /flex-direction\s*:\s*row/);
  assert.match(desktopFilters, /gap\s*:\s*4px/);
  assert.match(desktopFilters, /padding\s*:\s*3px/);
  assert.match(desktopFilters, /width\s*:\s*auto/);
  assert.match(desktopFilters, /min-height\s*:\s*0/);
  assert.match(desktopFilters, /max-width\s*:\s*100%/);
  assert.match(desktopFilters, /overflow-x\s*:\s*auto/);
  assert.doesNotMatch(desktopFilters, /height\s*:\s*var\(--tags-filter-height\)/);
  assert.doesNotMatch(adminCss, /admin-tags-filter-tab__count/);
  assert.match(filterTab, /flex\s*:\s*0 0 auto/);
  assert.match(filterTab, /width\s*:\s*auto/);
  assert.match(filterTab, /min-height\s*:\s*0/);
  assert.match(filterTab, /flex-direction\s*:\s*row/);
  assert.match(filterTab, /padding\s*:\s*6px\s+12px/);
  assert.match(filterTab, /text-align\s*:\s*center/);
  assert.match(filterTab, /white-space\s*:\s*nowrap/);
  assert.match(filterTabText, /writing-mode\s*:\s*horizontal-tb/);
  assert.match(filterTabText, /text-orientation\s*:\s*mixed/);
  assert.match(filters, /width\s*:\s*max-content/);
  assert.match(filters, /min-width\s*:\s*0/);
  assert.match(filters, /max-width\s*:\s*100%/);
  assert.match(filters, /flex-direction\s*:\s*row/);
  assert.match(filters, /overflow-x\s*:\s*auto/);
  assert.match(mobileFilterTabText, /writing-mode\s*:\s*horizontal-tb/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /display\s*:\s*grid/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /grid-template-columns\s*:\s*repeat\(4,\s*minmax\(0,\s*240px\)\)/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /width\s*:\s*min\(100%,\s*var\(--tags-cards-width\)\)/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /margin\s*:\s*0 auto/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /justify-content\s*:\s*start/);
  assert.match(allRuleBodies(adminCss, ".admin-tags-grid"), /align-items\s*:\s*stretch/);
  assert.doesNotMatch(allRuleBodies(adminCss, ".admin-tags-grid"), /display\s*:\s*flex/);
  assert.doesNotMatch(allRuleBodies(adminCss, ".admin-tags-grid"), /flex-wrap\s*:\s*wrap/);
  assert.match(grid, /width\s*:\s*min\(100%,\s*320px\)/);
  assert.match(grid, /grid-template-columns\s*:\s*minmax\(0,\s*1fr\)/);
  assert.match(grid, /justify-content\s*:\s*stretch/);
  assert.match(grid, /max-width\s*:\s*100%/);
  assert.doesNotMatch(card, /flex-basis/);
  assert.match(card, /width\s*:\s*100%/);
  assert.match(card, /max-width\s*:\s*100%/);
  assert.match(cardFooter, /flex-wrap\s*:\s*nowrap/);
  assert.match(cardCount, /white-space\s*:\s*nowrap/);
  assert.match(cardActions, /min-width\s*:\s*0/);
  assert.match(cardActions, /white-space\s*:\s*nowrap/);
  assert.match(cardEdit, /white-space\s*:\s*nowrap/);
  assert.match(cardDelete, /white-space\s*:\s*nowrap/);
  assert.match(cardDelete, /width\s*:\s*0/);
  assert.match(cardDelete, /padding-inline\s*:\s*0/);
  assert.match(pagination, /min-width\s*:\s*0/);
  assert.match(paginationInfo, /overflow-wrap\s*:\s*anywhere/);
});

test("mobile admin top navigation stays compact", () => {
  const css = mobileCss();
  const activeNavLink = ruleBody(css, ".admin-nav__link.is-active");

  assert.match(ruleBody(css, ".admin-shell"), /display\s*:\s*flex/);
  assert.match(ruleBody(css, ".admin-shell"), /flex-direction\s*:\s*column/);
  assert.match(ruleBody(css, ".admin-sidebar"), /flex\s*:\s*0\s+0\s+calc\(48px\s*\+\s*env\(safe-area-inset-top,\s*0px\)\)/);
  assert.match(ruleBody(css, ".admin-sidebar"), /height\s*:\s*calc\(48px\s*\+\s*env\(safe-area-inset-top,\s*0px\)\)/);
  assert.match(ruleBody(css, ".admin-sidebar"), /min-height\s*:\s*calc\(48px\s*\+\s*env\(safe-area-inset-top,\s*0px\)\)/);
  assert.match(ruleBody(css, ".admin-sidebar"), /calc\(6px\s*\+\s*env\(safe-area-inset-top,\s*0px\)\)/);
  assert.match(ruleBody(css, ".admin-sidebar"), /overflow-x\s*:\s*hidden/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-menu"), /position\s*:\s*absolute/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-menu"), /top\s*:\s*calc\(env\(safe-area-inset-top,\s*0px\)\s*\+\s*24px\)/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-menu"), /right\s*:\s*var\(--space-2\)/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-menu"), /transform\s*:\s*translateY\(-50%\)/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-panel"), /align-items\s*:\s*center/);
  assert.match(ruleBody(css, ".admin-sidebar__mobile-panel"), /gap\s*:\s*0/);
  assert.match(
    ruleBodyByContains(css, ".admin-sidebar__mobile-panel .admin-sidebar__home"),
    /justify-content\s*:\s*center/
  );
  assert.match(
    ruleBodyByContains(css, ".admin-sidebar__mobile-panel .admin-sidebar__home"),
    /width\s*:\s*min\(220px,\s*100%\)/
  );
  assert.match(
    ruleBodyByContains(css, ".admin-sidebar__mobile-panel .admin-sidebar__check-update::before"),
    /width\s*:\s*72px/
  );
  assert.match(
    ruleBodyByContains(css, ".admin-sidebar__mobile-panel .admin-sidebar__logout::before"),
    /background\s*:\s*var\(--border-subtle\)/
  );
  assert.match(ruleBody(css, ".admin-nav"), /align-items\s*:\s*center/);
  assert.match(ruleBody(css, ".admin-nav"), /justify-content\s*:\s*flex-start/);
  assert.match(ruleBody(css, ".admin-nav"), /overflow-x\s*:\s*auto/);
  assert.match(ruleBody(css, ".admin-nav__link"), /height\s*:\s*34px/);
  assert.match(ruleBody(css, ".admin-nav__link"), /gap\s*:\s*6px/);
  assert.match(ruleBody(css, ".admin-nav__link"), /line-height\s*:\s*1/);
  assert.match(ruleBody(css, ".admin-nav__link"), /flex\s*:\s*0\s+0\s+auto/);
  assert.match(ruleBody(css, ".admin-nav__icon"), /display\s*:\s*inline-flex/);
  assert.match(ruleBody(css, ".admin-nav__icon"), /width\s*:\s*16px/);
  assert.match(activeNavLink, /background\s*:\s*var\(--accent-soft\)/);
  assert.match(activeNavLink, /border-color\s*:\s*var\(--border-accent\)/);
  assert.match(activeNavLink, /box-shadow\s*:\s*none/);
  assert.match(ruleBody(css, ".admin-nav__action"), /display\s*:\s*none/);
  assert.match(ruleBody(css, ".admin-nav .admin-nav__action"), /display\s*:\s*none/);
  assert.match(ruleBody(css, ".admin-main"), /padding\s*:\s*var\(--space-2\)\s+var\(--space-3\)\s+var\(--space-4\)/);
  assert.match(ruleBody(css, ".admin-page__header"), /margin-bottom\s*:\s*var\(--space-3\)/);
});

test("crawler header keeps preview toggle and add action aligned across breakpoints", () => {
  const header = ruleBody(adminCss, ".admin-crawlers-page .admin-page__header");
  const teaser = ruleBody(
    adminCss,
    ".admin-crawlers-page .admin-page__header > .admin-crawler-global-teaser"
  );
  const actions = ruleBody(
    adminCss,
    ".admin-crawlers-page .admin-page__header > .admin-crawler-page-actions"
  );

  assert.match(header, /align-items\s*:\s*flex-start/);
  assert.match(header, /justify-content\s*:\s*space-between/);
  assert.match(header, /flex-wrap\s*:\s*nowrap/);
  assert.match(teaser, /width\s*:\s*auto/);
  assert.match(teaser, /justify-items\s*:\s*start/);
  assert.match(actions, /width\s*:\s*auto/);
  assert.match(actions, /justify-content\s*:\s*flex-end/);
  assert.match(actions, /margin-left\s*:\s*auto/);
  assert.match(actions, /padding-top\s*:\s*14px/);
});
