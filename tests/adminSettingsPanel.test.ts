import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { SettingsSection } from "../src/admin/settings/SettingsSection";

const appSource = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const layoutSource = readFileSync(
  new URL("../src/admin/AdminLayout.tsx", import.meta.url),
  "utf8"
);
const pageSource = readFileSync(
  new URL("../src/admin/SettingsPage.tsx", import.meta.url),
  "utf8"
);
const sectionSource = readFileSync(
  new URL("../src/admin/settings/SettingsSection.tsx", import.meta.url),
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
  assert.match(pageSource, /title="自动任务"/);
  assert.match(pageSource, /<SettingsSection/);
  assert.match(pageSource, /<SettingsRow/);
  assert.match(pageSource, /type="time"/);
  assert.match(pageSource, /parseDocument/);
  assert.match(pageSource, /document\.setIn\(\["nightly", "start_time"\]/);
  assert.match(pageSource, /api\.getConfigYAML\(\)/);
  assert.match(pageSource, /api\.updateConfigYAML\(pendingSave\.after, pendingSave\.version\)/);
  assert.match(pageSource, /有未保存更改/);
  assert.doesNotMatch(pageSource, /重复复核|duplicateReviewEnabled|duplicate_review_enabled/);
  assert.match(apiSource, /If-Match/);
  assert.match(apiSource, /ConfigConflictError/);
});

test("configuration panel follows the CLIProxy configuration workspace UI", () => {
  assert.match(pageSource, /配置管理/);
  assert.match(pageSource, /可视化编辑/);
  assert.match(pageSource, /源码编辑/);
  assert.match(pageSource, /placeholder="搜索配置项\.\.\."/);
  assert.match(pageSource, /admin-config-section-nav/);
  assert.match(pageSource, /<span>config\.yaml<\/span>/);
  assert.match(pageSource, /ConfigDiffModal/);
  assert.match(pageSource, /差异已更新，请重新确认/);
  assert.match(diffModalSource, /buildConfigDiff/);
  assert.match(diffModalSource, /确认变更/);
  assert.match(diffModalSource, /@@ -\{hunk\.oldStart\}/);
  assert.match(diffModalSource, /is-addition/);
  assert.match(diffModalSource, /is-deletion/);
  assert.match(adminCss, /\.admin-config-tabs\s*\{[^}]*display:\s*grid/s);
  assert.match(
    adminCss,
    /\.admin-config-section-nav\s*\{[^}]*position:\s*sticky[^}]*grid-template-columns:/s
  );
  assert.match(adminCss, /\.admin-config-sections\s*\{[^}]*display:\s*block/s);
  assert.match(
    adminCss,
    /\.admin-config-section\s*\{[^}]*border-radius:\s*8px/s
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
});

test("configuration section navigation directly renders the selected panel", () => {
  const markup = renderToStaticMarkup(
    createElement(
      SettingsSection,
      {
        id: "config-automation",
        index: "01",
        icon: null,
        title: "自动任务",
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
  assert.doesNotMatch(pageSource, /activeSection === "config-dedupe"/);
  assert.doesNotMatch(pageSource, /scrollTo\(/);
  assert.doesNotMatch(pageSource, /handleSectionsScroll/);
});
