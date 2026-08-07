import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  ADMIN_PAGE_TITLES,
  DEFAULT_ADMIN_PAGE_TITLE,
  getAdminPageTitle,
  shouldShowAdminPageHeader,
} from "../src/admin/adminPageTitle.ts";
import { getAdminRouteCacheKey } from "../src/admin/AdminRouteCache.tsx";

const pages = {
  home: readFileSync(new URL("../src/pages/HomePage.tsx", import.meta.url), "utf8"),
  listing: readFileSync(new URL("../src/pages/ListingPage.tsx", import.meta.url), "utf8"),
  detail: readFileSync(new URL("../src/pages/VideoDetailPage.tsx", import.meta.url), "utf8"),
  upload: readFileSync(new URL("../src/pages/UploadPage.tsx", import.meta.url), "utf8"),
  shorts: readFileSync(new URL("../src/pages/ShortsPage.tsx", import.meta.url), "utf8"),
};
const adminLayout = readFileSync(
  new URL("../src/admin/AdminLayout.tsx", import.meta.url),
  "utf8"
);
const adminPageActions = readFileSync(
  new URL("../src/admin/AdminPageActions.tsx", import.meta.url),
  "utf8"
);
const adminRouteCache = readFileSync(
  new URL("../src/admin/AdminRouteCache.tsx", import.meta.url),
  "utf8"
);

test("public page document titles omit the site suffix", () => {
  for (const [name, source] of Object.entries(pages)) {
    assert.doesNotMatch(source, /document\.title\s*=[^;]*·\s*91/, `${name} still appends the site suffix`);
  }

  assert.match(pages.home, /document\.title = activeSearchQuery[\s\S]*\? `搜索 "\$\{activeSearchQuery\}"`[\s\S]*: activeTag[\s\S]*\? `标签 \$\{activeTag\}`[\s\S]*: "首页"/);
  assert.match(pages.listing, /\? `搜索 "\$\{keyword\}"`/);
  assert.match(pages.listing, /\? `标签 \$\{tag\}`/);
  assert.match(pages.detail, /document\.title = stableDetail \? stableDetail\.title : "视频不存在"/);
  assert.match(pages.upload, /document\.title = "上传视频"/);
  assert.match(pages.shorts, /document\.title = "短视频"/);
});

test("admin layout displays and applies the current route title", () => {
  assert.match(
    adminLayout,
    /const currentPageTitle = getAdminPageTitle\(location\.pathname\)/
  );
  assert.match(adminLayout, /document\.title = currentPageTitle/);
  assert.match(
    adminLayout,
    /const showCurrentPageHeader = shouldShowAdminPageHeader\([\s\S]*?location\.pathname,[\s\S]*?location\.search[\s\S]*?\)/
  );
  assert.match(
    adminLayout,
    /\{showCurrentPageHeader && \([\s\S]*?<h1 className="admin-page__title" aria-live="polite">\s*\{currentPageTitle\}\s*<\/h1>/
  );
  assert.match(adminLayout, /ref=\{setPageActionsTarget\}[\s\S]*className="admin-current-page-actions"/);
  assert.match(
    adminLayout,
    /<AdminPageActionsProvider target=\{pageActionsTarget\}>[\s\S]*?<AdminRouteCache \/>[\s\S]*?<\/AdminPageActionsProvider>/
  );
  assert.match(adminPageActions, /const routeActive = useAdminRouteActive\(\)/);
  assert.match(adminPageActions, /if \(!context\?\.target \|\| !routeActive\)/);
  assert.match(adminPageActions, /createPortal\(children, context\.target\)/);
});

test("admin route changes retain bounded page lifecycles", () => {
  assert.match(adminLayout, /<AdminRouteCache \/>/);
  assert.match(
    adminRouteCache,
    /cachedRoutesRef = useRef\(new Map<string, CachedAdminRoute>\(\)\)/
  );
  assert.match(adminRouteCache, /cachedRoutesRef\.current\.set\(cacheKey/);
  assert.match(adminRouteCache, /hidden=\{!active\}/);
  assert.match(adminRouteCache, /containerRef\.current\.inert = !active/);
  assert.match(
    adminRouteCache,
    /<UNSAFE_LocationContext\.Provider key=\{key\} value=\{route\.locationContext\}>/
  );
  assert.doesNotMatch(adminLayout, /<Outlet key=\{location\.pathname\} \/>/);
});

test("admin route cache is limited to known top-level pages", () => {
  assert.equal(getAdminRouteCacheKey("/admin/drives"), "/admin/drives");
  assert.equal(getAdminRouteCacheKey("/admin/drives/"), "/admin/drives");
  assert.equal(getAdminRouteCacheKey("/admin/drives/detail"), "/admin/drives");
  assert.equal(getAdminRouteCacheKey("/admin/logs"), "/admin/logs");
  assert.equal(getAdminRouteCacheKey("/admin"), null);
  assert.equal(getAdminRouteCacheKey("/admin/unknown"), null);
});

test("every admin page has a centralized title", () => {
  assert.deepEqual(
    ADMIN_PAGE_TITLES.map(({ path, title }) => [path, title]),
    [
      ["/admin/drives", "网盘管理"],
      ["/admin/crawlers", "爬虫管理"],
      ["/admin/videos", "视频管理"],
      ["/admin/tags", "标签管理"],
      ["/admin/users", "用户管理"],
      ["/admin/backup", "备份恢复"],
      ["/admin/logs", "日志查看"],
      ["/admin/settings", "配置管理"],
    ]
  );

  for (const { path, title } of ADMIN_PAGE_TITLES) {
    assert.equal(getAdminPageTitle(path), title);
    assert.equal(getAdminPageTitle(`${path}/`), title);
    assert.equal(getAdminPageTitle(`${path}/detail`), title);
  }
  assert.equal(getAdminPageTitle("/admin/unknown"), DEFAULT_ADMIN_PAGE_TITLE);
});

test("drive details omit the shared admin page header", () => {
  assert.equal(shouldShowAdminPageHeader("/admin/drives", ""), true);
  assert.equal(shouldShowAdminPageHeader("/admin/drives/", "?filter=active"), true);
  assert.equal(shouldShowAdminPageHeader("/admin/drives", "?drive=115"), false);
  assert.equal(shouldShowAdminPageHeader("/admin/drives", "?drive=%20%20"), true);
  assert.equal(shouldShowAdminPageHeader("/admin/videos", "?drive=115"), true);
});
