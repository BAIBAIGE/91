import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  SettingsRow,
  SettingsSection,
} from "../src/admin/settings/SettingsSection";

const appSource = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const layoutSource = readFileSync(
  new URL("../src/admin/AdminLayout.tsx", import.meta.url),
  "utf8"
);
const pageSource = readFileSync(
  new URL("../src/admin/SettingsPage.tsx", import.meta.url),
  "utf8"
);
const pageTitleSource = readFileSync(
  new URL("../src/admin/adminPageTitle.ts", import.meta.url),
  "utf8"
);
const sectionSource = readFileSync(
  new URL("../src/admin/settings/SettingsSection.tsx", import.meta.url),
  "utf8"
);
const configYamlSource = readFileSync(
  new URL("../src/admin/settings/configYaml.ts", import.meta.url),
  "utf8"
);
const sourceEditorSource = readFileSync(
  new URL("../src/admin/settings/ConfigSourceEditor.tsx", import.meta.url),
  "utf8"
);
const sourceWorkspaceSource = readFileSync(
  new URL("../src/admin/settings/ConfigSourceWorkspace.tsx", import.meta.url),
  "utf8"
);
const diffModalSource = readFileSync(
  new URL("../src/admin/settings/ConfigDiffModal.tsx", import.meta.url),
  "utf8"
);
const apiSource = readFileSync(new URL("../src/admin/api.ts", import.meta.url), "utf8");
const adminCss = readFileSync(
  new URL("../src/styles/admin.css", import.meta.url),
  "utf8"
);

test("configuration panel is a dedicated protected admin route", () => {
  assert.match(appSource, /const SettingsPage = lazy/);
  assert.match(appSource, /path="settings"[\s\S]*?<SettingsPage \/>/);
  assert.match(layoutSource, /to="\/admin\/settings"/);
  assert.match(layoutSource, />配置面板</);
  assert.doesNotMatch(appSource, /duplicate-reviews|DuplicateReviewsPage/);
  assert.doesNotMatch(layoutSource, /重复复核|duplicate-reviews/);
  assert.doesNotMatch(apiSource, /DuplicateReviewPair|listDuplicateReviews|resolveDuplicateReview/);
  assert.doesNotMatch(adminCss, /admin-duperev/);
});

test("configuration panel groups typed fields from the real YAML document", () => {
  assert.match(pageSource, /title="定时任务"/);
  assert.match(pageSource, /description="按指定时区控制每日扫盘和库内视频维护时间"/);
  assert.match(pageSource, /label="启动时间"/);
  assert.match(pageSource, /label="任务时区"/);
  assert.doesNotMatch(pageSource, /label="任务时区"\s+description=/);
  assert.match(pageSource, /label="任务时区"[\s\S]*?layout="inline"/);
  assert.match(pageSource, /<select[\s\S]*?id="nightly-timezone"/);
  assert.match(
    pageSource,
    /admin-config-picker-field__value[\s\S]*?draft\.nightlyTimezone \|\| "--"/
  );
  assert.doesNotMatch(pageSource, /北京时间/);
  assert.match(pageSource, /"Asia\/Shanghai"/);
  assert.match(pageSource, /America\/Los_Angeles/);
  assert.match(pageSource, /updateVisualField\("nightlyTimezone", event\.target\.value\)/);
  assert.doesNotMatch(pageSource, /24 小时制 · HH:mm/);
  assert.doesNotMatch(
    pageSource,
    /按服务器本地时区执行，每天最多自动触发一次；保存后无需重启。/
  );
  assert.match(pageSource, /<SettingsSection/);
  assert.match(pageSource, /<SettingsRow/);
  assert.match(pageSource, /type="time"/);
  assert.match(pageSource, /applyVisualFields/);
  assert.match(configYamlSource, /parseDocument/);
  assert.match(configYamlSource, /nightlyStartTimeEdits/);
  assert.match(configYamlSource, /nightlyTimezoneEdits/);
  assert.match(configYamlSource, /nightlyTimezone: string/);
  assert.match(configYamlSource, /\["nightly", "timezone"\]/);
  assert.doesNotMatch(configYamlSource, /document\.toString/);
  assert.match(pageSource, /api\.getConfigYAML\(\)/);
  assert.match(pageSource, /api\.updateConfigYAML\(pendingSave\.after, pendingSave\.version\)/);
  assert.match(pageSource, /有未保存更改/);
  assert.doesNotMatch(pageSource, /重复复核|duplicateReviewEnabled|duplicate_review_enabled/);
  assert.match(apiSource, /If-Match/);
  assert.match(apiSource, /ConfigConflictError/);
  assert.match(apiSource, /nightlyTimezone: string/);
});

test("built-in tag changes use the configuration draft and shared save review", () => {
  assert.match(pageSource, /id: "config-tags"/);
  assert.match(pageSource, /title="内置标签"/);
  assert.match(pageSource, /description="管理系统内置标签"/);
  assert.match(pageSource, /label="内置标签"/);
  assert.doesNotMatch(pageSource, /内置标签开关/);
  assert.doesNotMatch(pageSource, /自定义标签不受影响/);
  assert.doesNotMatch(pageSource, /builtin-tags-description/);
  assert.doesNotMatch(pageSource, /api\.getSettings\(\)|api\.updateSettings\(/);
  assert.doesNotMatch(pageSource, /builtinTagsChange\b|builtinTagsDirty/);
  assert.match(pageSource, /role="switch"/);
  assert.match(pageSource, /aria-checked=\{draft\.builtinTagsEnabled\}/);
  assert.match(
    pageSource,
    /updateVisualField\("builtinTagsEnabled", !draft\.builtinTagsEnabled\)/
  );
  assert.match(
    pageSource,
    /api\.updateConfigYAML\(pendingSave\.after, pendingSave\.version\)[\s\S]*?builtinTagsChanged[\s\S]*?invalidateTagsCache\(\)/
  );
  assert.match(pageSource, /visualDirtyFields\.has\("builtinTagsEnabled"\)[\s\S]*?"待恢复"[\s\S]*?"待移除"/);
  assert.doesNotMatch(pageSource, /ConfirmModal|removeBuiltinTagsConfirmOpen/);
  assert.match(configYamlSource, /builtinTagsEnabled: boolean/);
  assert.match(configYamlSource, /\["tags", "builtin_pack_enabled"\]/);
  assert.match(configYamlSource, /builtinTagsEnabledEdits/);
  assert.match(configYamlSource, /builtin_pack_enabled: \$\{rendered\}/);
  assert.match(diffModalSource, /const hasChanges = diff\.additions \+ diff\.deletions > 0/);
  assert.match(diffModalSource, /aria-label="config\.yaml 变更对比"/);
  assert.doesNotMatch(diffModalSource, /settingChanges|应用设置|Database|TriangleAlert/);
  assert.doesNotMatch(adminCss, /admin-config-diff-settings|admin-config-diff-setting__/);
  assert.match(apiSource, /settings:\s*\{[\s\S]*?builtinTagsEnabled: boolean/);
  assert.match(adminCss, /\.admin-config-control--switch\s*\{[^}]*display:\s*flex/s);
});

test("configuration panel keeps the CPA workspace mounted while loading", () => {
  assert.doesNotMatch(pageSource, /AdminLoading/);
  assert.doesNotMatch(pageSource, /if \(loading\) return\s*</);
  assert.match(pageSource, /if \(!loading && \(loadError \|\| !loaded\)\)/);
  assert.match(pageSource, /aria-busy=\{loading\}/);
  assert.match(pageSource, /const controlsDisabled = loading \|\| saving \|\| loaded === null/);
  assert.match(pageSource, /loading\s*\? "加载配置中"/);
  assert.match(sourceWorkspaceSource, /<Suspense fallback=\{null\}>/);
  assert.doesNotMatch(
    `${pageSource}\n${sourceWorkspaceSource}`,
    /admin-config-source__loading|正在加载源码编辑器/
  );
  assert.doesNotMatch(adminCss, /\.admin-config-source__loading/);
});

test("configuration source workspace stays visible while CodeMirror loads", () => {
  assert.match(pageSource, /<ConfigSourceWorkspace[\s\S]*?value=\{workingYAML\}/);
  assert.match(
    sourceWorkspaceSource,
    /<div className="admin-config-source">[\s\S]*?<div className="admin-config-source__toolbar">[\s\S]*?<div className=\{`admin-config-source__editor[\s\S]*?<Suspense fallback=\{null\}>[\s\S]*?<LazyConfigSourceEditor/
  );
  assert.doesNotMatch(
    sourceEditorSource,
    /admin-config-source__toolbar|admin-config-source__editor/
  );
  assert.match(pageSource, /window\.requestIdleCallback\(preload, \{ timeout: 1_500 \}\)/);
  assert.match(pageSource, /preloadConfigSourceEditor\(\)\.catch/);
});

test("configuration panel follows the CLIProxy configuration workspace UI", () => {
  assert.match(pageTitleSource, /title: "配置管理"/);
  assert.match(pageSource, /可视化编辑/);
  assert.match(pageSource, /源码编辑/);
  assert.doesNotMatch(pageSource, /placeholder="搜索配置项\.\.\."/);
  assert.doesNotMatch(pageSource, /admin-config-search/);
  assert.match(pageSource, /admin-config-section-nav/);
  assert.match(
    sourceWorkspaceSource,
    /const loadConfigSourceEditor = \(\) => import\("\.\/ConfigSourceEditor"\)/
  );
  assert.match(sourceWorkspaceSource, /const LazyConfigSourceEditor = lazy\(loadConfigSourceEditor\)/);
  assert.match(sourceWorkspaceSource, /placeholder="搜索配置内容\.\.\."/);
  assert.match(sourceEditorSource, /<CodeMirror/);
  assert.match(sourceEditorSource, /yaml\(\)/);
  assert.match(pageSource, /ConfigDiffModal/);
  assert.match(pageSource, /差异已更新，请重新确认/);
  assert.match(diffModalSource, /buildConfigDiff/);
  assert.match(diffModalSource, /确认变更/);
  assert.match(diffModalSource, /@@ -\{hunk\.oldStart\}/);
  assert.match(diffModalSource, /is-addition/);
  assert.match(diffModalSource, /is-deletion/);
  assert.match(adminCss, /\.admin-config-tabs\s*\{[^}]*display:\s*grid/s);
  assert.match(
    pageSource,
    /aria-selected=\{activeTab === "visual"\}[\s\S]*?disabled=\{loading \|\| saving\}/
  );
  assert.match(
    pageSource,
    /aria-selected=\{activeTab === "source"\}[\s\S]*?disabled=\{loading \|\| saving\}/
  );
  assert.match(layoutSource, /isSettingsPage \? " admin-main--settings"/);
  assert.match(
    adminCss,
    /\.admin-main--settings\s*\{[^}]*--admin-config-content-width:\s*1480px;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-page\s*\{[^}]*width:\s*min\(100%, var\(--admin-config-content-width, 1480px\)\);[^}]*margin:\s*0 auto;/s
  );
  assert.match(
    adminCss,
    /\.admin-main--settings \.admin-current-page-header\s*\{[^}]*width:\s*min\(100%, var\(--admin-config-content-width\)\);[^}]*margin:\s*0 auto 10px;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-section-nav\s*\{[^}]*position:\s*sticky[^}]*grid-template-columns:\s*repeat\(3, minmax\(0, 1fr\)\)/s
  );
  assert.match(
    adminCss,
    /@media \(min-width: 769px\) and \(max-width: 1024px\)[\s\S]*?\.admin-config-section-nav\s*\{[^}]*grid-template-columns:\s*repeat\(2, minmax\(0, 1fr\)\)/s
  );
  assert.match(adminCss, /\.admin-config-sections\s*\{[^}]*display:\s*block/s);
  assert.match(
    adminCss,
    /\.admin-config-section\s*\{[^}]*border-radius:\s*8px;[^}]*background:\s*color-mix\(in srgb, var\(--bg-surface\) 50%, transparent\);/s
  );
  assert.match(
    adminCss,
    /\.admin-config-section__header\s*\{[^}]*background:\s*transparent;/s
  );
  assert.doesNotMatch(adminCss, /scroll-snap-type:\s*x mandatory/);
  assert.match(adminCss, /\.admin-config-diff-hunk__header/);
  assert.match(adminCss, /\.admin-config-diff-line\.is-addition/);
  assert.match(adminCss, /\.admin-config-diff-line\.is-deletion/);
  assert.match(
    adminCss,
    /\.admin-config-actions\s*\{[^}]*position:\s*fixed[^}]*width:\s*fit-content/s
  );
  assert.match(
    adminCss,
    /@media \(max-width: 768px\)[\s\S]*?\.admin-config-row\s*\{[^}]*grid-template-columns:\s*minmax\(0, 1fr\)/s
  );
  assert.match(
    adminCss,
    /\.admin-config-control--switch\s*\{[^}]*display:\s*flex;[^}]*flex-direction:\s*column;[^}]*align-items:\s*center;[^}]*gap:\s*4px;[^}]*min-width:\s*48px;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-row__copy label\s*\{[^}]*align-self:\s*flex-start;[^}]*width:\s*fit-content;[^}]*max-width:\s*100%;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field\s*\{[^}]*min-height:\s*38px;[^}]*border:\s*1px solid var\(--border-default\);/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field--time\s*\{[^}]*width:\s*72px;[^}]*padding:\s*0 9px;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field--timezone\s*\{[^}]*width:\s*160px;[^}]*padding:\s*0 8px;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field__value\s*\{[^}]*overflow:\s*hidden;[^}]*line-height:\s*1\.4;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field > :is\(input, select\)\s*\{[^}]*position:\s*absolute;[^}]*inset:\s*0;[^}]*color-scheme:\s*dark;[^}]*opacity:\s*0;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-picker-field select option\s*\{[^}]*background:\s*var\(--bg-elevated\);[^}]*color:\s*var\(--text-strong\);/s
  );
  assert.match(
    adminCss,
    /:root\[data-theme="pink"\][\s\S]*?\.admin-config-picker-field > :is\(input, select\)[\s\S]*?color-scheme:\s*light;/s
  );
  assert.match(
    pageSource,
    /admin-config-picker-field__value--time[\s\S]*?draft\.nightlyStartTime \|\| "--:--"[\s\S]*?event\.currentTarget\.showPicker\(\)/
  );
  assert.doesNotMatch(adminCss, /calendar-picker-indicator/);
  assert.match(
    adminCss,
    /@media \(max-width: 768px\)[\s\S]*?\.admin-config-section\s*\{[^}]*height:\s*clamp\(420px,\s*calc\(100dvh - var\(--admin-header-height\) - 260px\),\s*680px\)/s
  );
  assert.match(pageSource, /data-admin-floating-actions/);
});

test("configuration source editor uses one scrollable CodeMirror viewport", () => {
  assert.doesNotMatch(
    `${pageSource}\n${sourceWorkspaceSource}`,
    /<textarea|sourceGutterRef|admin-config-source__gutter/
  );
  assert.match(sourceEditorSource, /height="100%"/);
  assert.match(sourceEditorSource, /lineNumbers:\s*true/);
  assert.match(sourceEditorSource, /foldGutter:\s*true/);
  assert.match(
    adminCss,
    /\.admin-config-source__editor \.cm-scroller\s*\{[^}]*overflow:\s*auto;[^}]*overscroll-behavior:\s*contain;[^}]*touch-action:\s*pan-x pan-y;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-source__editor\s*\{[^}]*height:\s*clamp\(500px, 70vh, 1040px\);[^}]*overflow:\s*hidden;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-source\s*\{[^}]*padding-bottom:\s*var\(--space-8\);/s
  );
  assert.match(
    adminCss,
    /@media \(max-width: 768px\)[\s\S]*?\.admin-config-source\s*\{[^}]*padding-bottom:\s*var\(--space-6\);/s
  );
});

test("configuration section navigation directly renders the selected panel", () => {
  const markup = renderToStaticMarkup(
    createElement(
      SettingsSection,
      {
        id: "config-automation",
        index: "01",
        icon: null,
        title: "定时任务",
        description: "维护任务设置",
      },
      createElement("span", null, "setting")
    )
  );
  assert.match(
    markup,
    /<section id="config-automation" class="admin-config-section" role="tabpanel" aria-labelledby="config-automation-tab"/
  );
  assert.match(sectionSource, /role="tabpanel"/);
  assert.match(pageSource, /role="tablist"/);
  assert.match(pageSource, /aria-selected=\{activeSection === section\.id\}/);
  assert.match(pageSource, /onClick=\{\(\) => setActiveSection\(section\.id\)\}/);
  assert.match(pageSource, /activeSection === "config-automation"/);
  assert.match(pageSource, /activeSection === "config-tags"/);
  assert.doesNotMatch(pageSource, /activeSection === "config-dedupe"/);
  assert.doesNotMatch(pageSource, /scrollTo\(/);
  assert.doesNotMatch(pageSource, /handleSectionsScroll/);
});

test("compact configuration rows stay inline on mobile", () => {
  const markup = renderToStaticMarkup(
    createElement(
      SettingsRow,
      {
        label: "启动时间",
        layout: "inline",
      },
      createElement("span", null, "03:00")
    )
  );

  assert.match(markup, /class="admin-config-row admin-config-row--inline"/);
  assert.match(pageSource, /label="启动时间"[\s\S]*?layout="inline"/);
  assert.match(pageSource, /label="内置标签"[\s\S]*?layout="inline"/);
  assert.match(
    adminCss,
    /@media \(max-width: 768px\)[\s\S]*?\.admin-config-row--inline\s*\{[^}]*grid-template-columns:\s*minmax\(0, 1fr\) auto;[^}]*align-items:\s*center;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-row--inline \.admin-config-control--picker\s*\{[^}]*justify-items:\s*end;/s
  );
  assert.match(
    adminCss,
    /\.admin-config-control--switch\s*\{[^}]*flex-direction:\s*column;[^}]*justify-content:\s*center;/s
  );
});
