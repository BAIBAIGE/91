import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const homePageSource = readFileSync(
  new URL("../src/pages/HomePage.tsx", import.meta.url),
  "utf8"
);
const tagCloudSource = readFileSync(
  new URL("../src/components/TagCloud.tsx", import.meta.url),
  "utf8"
);
const searchPanelSource = readFileSync(
  new URL("../src/components/SearchPanel.tsx", import.meta.url),
  "utf8"
);
const layoutCss = readFileSync(
  new URL("../src/styles/layout.css", import.meta.url),
  "utf8"
);
const searchCss = readFileSync(
  new URL("../src/styles/search.css", import.meta.url),
  "utf8"
);
const appShellSource = readFileSync(
  new URL("../src/components/AppShell.tsx", import.meta.url),
  "utf8"
);
const backToTopSource = readFileSync(
  new URL("../src/components/BackToTop.tsx", import.meta.url),
  "utf8"
);

function ruleBody(css: string, selector: string): string {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = css.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`));
  assert.ok(match, `Expected CSS rule for ${selector}`);
  return match[1];
}

test("home page refresh button shares back-to-top slot until back-to-top is visible", () => {
  assert.match(homePageSource, /import \{ RefreshCw \} from "lucide-react"/);
  assert.match(homePageSource, /const LATEST_POOL_SIZE = 96;/);
  assert.match(homePageSource, /const HOME_LATEST_CURSOR_KEY = "home\.latest\.cursor";/);
  assert.match(homePageSource, /function nextLatestBatch/);
  assert.match(homePageSource, /let cachedLatestBatch: VideoItem\[\] \| null = null;/);
  assert.match(homePageSource, /function cacheNextLatestBatch/);
  assert.match(homePageSource, /loadLatestCursor\(items\.length\)/);
  assert.match(homePageSource, /saveLatestCursor\(\(start \+ count\) % items\.length\)/);
  assert.match(homePageSource, /const refreshHome = useCallback\(async \(\) =>/);
  assert.match(homePageSource, /fetchHomeVideos\(displayCountRef\.current\)/);
  assert.match(homePageSource, /fetchListing\(1,\s*LATEST_POOL_SIZE,\s*\{ sort: "latest", includeTotal: false \}\)/);
  assert.match(homePageSource, /const DESKTOP_COUNT = 12;/);
  assert.match(homePageSource, /const MOBILE_COUNT = 8;/);
  assert.match(homePageSource, /const HOME_SEARCH_DESKTOP_PAGE_SIZE = 20;/);
  assert.match(homePageSource, /setLatestVideos\(latestBatch\)/);
  assert.match(homePageSource, /const displayCount = isMobile \? MOBILE_COUNT : DESKTOP_COUNT;/);
  assert.match(homePageSource, /const displayCountRef = useRef\(displayCount\);/);
  assert.match(homePageSource, /cacheNextLatestBatch\(\s*latestResult\.value\.items,\s*displayCountRef\.current\s*\)/);
  assert.match(homePageSource, /cacheNextLatestBatch\(\s*latestResult\.items,\s*displayCountRef\.current\s*\)/);
  assert.match(homePageSource, /const batchSize = Math\.min\(DESKTOP_COUNT, items\.length\);/);
  assert.match(homePageSource, /saveLatestCursor\(\(start \+ count\) % items\.length\)/);
  assert.doesNotMatch(homePageSource, /HOME_RECENT_KEY/);
  assert.doesNotMatch(homePageSource, /loadRecentHomeVideoIds/);
  assert.doesNotMatch(homePageSource, /rememberHomeVideos/);
  assert.match(homePageSource, /className=\{`home-refresh \$\{refreshing \? "is-refreshing" : ""\}`\}/);
  assert.match(homePageSource, /aria-label="刷新首页"/);
  assert.match(homePageSource, /<RefreshCw size=\{18\} \/>/);

  const refresh = ruleBody(layoutCss, ".home-refresh");
  const shiftedRefresh = ruleBody(layoutCss, ".app-shell.is-back-to-top-visible .home-refresh");
  const backToTop = ruleBody(layoutCss, ".back-to-top");
  assert.match(refresh, /position\s*:\s*fixed/);
  assert.match(refresh, /bottom\s*:\s*24px/);
  assert.match(backToTop, /bottom\s*:\s*24px/);
  assert.match(shiftedRefresh, /bottom\s*:\s*80px/);
  assert.match(refresh, /z-index\s*:\s*var\(--z-overlay\)/);
  assert.doesNotMatch(layoutCss, /\.home-refresh\.is-visible/);

  assert.match(appShellSource, /const \[backToTopVisible,\s*setBackToTopVisible\] = useState\(false\)/);
  assert.match(appShellSource, /backToTopVisible \? "is-back-to-top-visible" : ""/);
  assert.match(appShellSource, /<BackToTop onVisibilityChange=\{setBackToTopVisible\} \/>/);
  assert.match(backToTopSource, /onVisibilityChange\?: \(visible: boolean\) => void/);
  assert.match(backToTopSource, /onVisibilityChange\?\.\(nextVisible\)/);
});

test("home page reuses the cached latest batch when returning from detail", () => {
  assert.match(homePageSource, /const \[latestVideos,\s*setLatestVideos\] = useState<VideoItem\[\]>\(cachedLatestBatch \?\? \[\]\)/);
  assert.match(homePageSource, /const \[latestLoading,\s*setLatestLoading\] = useState\(cachedLatestBatch === null\)/);
  assert.match(homePageSource, /cachedLatestBatch \?\?\s*cacheNextLatestBatch\(cachedLatestPool, displayCountRef\.current\)/);
  assert.doesNotMatch(homePageSource, /setLatestVideos\(nextLatestBatch\(cachedLatestPool,\s*DESKTOP_COUNT\)\)/);
});

test("home page reserves tag cloud space while tags load and uses one empty library state", () => {
  assert.match(tagCloudSource, /const visibleTags = useMemo/);
  assert.match(tagCloudSource, /typeof tag\.count !== "number" \|\| tag\.count > 0/);
  assert.match(tagCloudSource, /const initialTagsRef = useRef<TagItem\[\] \| null>\(readCachedTags\(\)\)/);
  assert.match(tagCloudSource, /const \[tags,\s*setTags\] = useState<TagItem\[\]>\(initialTagsRef\.current \?\? \[\]\)/);
  assert.match(tagCloudSource, /const \[loaded,\s*setLoaded\] = useState\(initialTagsRef\.current !== null\)/);
  assert.match(tagCloudSource, /if \(initialTagsRef\.current !== null\) return/);
  assert.match(tagCloudSource, /setLoaded\(true\)/);
  assert.match(tagCloudSource, /if \(loaded && visibleTags\.length === 0\) return null/);
  assert.match(tagCloudSource, /const loading = !loaded && visibleTags\.length === 0/);
  assert.match(tagCloudSource, /const TAG_PLACEHOLDER_COUNT = 16;/);
  assert.match(tagCloudSource, /type TagCloudProps = \{/);
  assert.match(tagCloudSource, /linkBasePath\?: string;/);
  assert.match(tagCloudSource, /onTagSelect\?: \(\) => void;/);
  assert.match(tagCloudSource, /export function TagCloud\(\{ linkBasePath = "\/list", onTagSelect \}: TagCloudProps\)/);
  assert.match(tagCloudSource, /to=\{`\$\{linkBasePath\}\?tag=\$\{encodeURIComponent\(tag\.label\)\}`\}/);
  assert.match(tagCloudSource, /onClick=\{onTagSelect\}/);
  assert.match(tagCloudSource, /className=\{`tag-cloud-container \$\{loading \? "is-loading" : ""\}`\}/);
  assert.match(tagCloudSource, /aria-busy=\{loading \? "true" : undefined\}/);
  assert.match(tagCloudSource, /Array\.from\(\{ length: TAG_PLACEHOLDER_COUNT \}/);
  assert.match(tagCloudSource, /tag-chip--placeholder/);
  assert.doesNotMatch(tagCloudSource, /setTimeout/);
  assert.match(tagCloudSource, /visibleTags\.map\(renderTag\)/);
  assert.doesNotMatch(tagCloudSource, /const row[12] = visibleTags\.filter/);
  assert.doesNotMatch(tagCloudSource, /\(\{tag\.count\}\)/);
  assert.doesNotMatch(tagCloudSource, /`\$\{tag\.count\} 个视频`/);

  const tagCloudContainer = ruleBody(searchCss, ".tag-cloud-container");
  const loadingTagCloud = ruleBody(searchCss, ".tag-cloud-container.is-loading");
  const reservedTagCloud = ruleBody(searchCss, ".tag-cloud-container.is-reserved");
  const tagCloudRow = ruleBody(searchCss, ".tag-cloud__row");
  const tagChip = ruleBody(searchCss, ".tag-chip");
  const tagPlaceholder = ruleBody(searchCss, ".tag-chip--placeholder");
  assert.match(tagCloudContainer, /min-height\s*:\s*34px/);
  assert.match(loadingTagCloud, /pointer-events\s*:\s*none/);
  assert.match(reservedTagCloud, /mask-image\s*:\s*none/);
  assert.match(tagCloudRow, /flex-wrap\s*:\s*nowrap/);
  assert.match(tagChip, /flex\s*:\s*0 0 auto/);
  assert.match(tagPlaceholder, /width\s*:\s*68px/);
  assert.match(tagPlaceholder, /animation\s*:\s*tag-chip-placeholder/);
  assert.match(searchCss, /\.tag-chip--placeholder:nth-child\(6n \+ 1\)/);
  assert.match(searchCss, /\.tag-chip--placeholder:nth-child\(6n\)/);

  const searchForm = ruleBody(searchCss, ".search-panel__form");
  const searchInput = ruleBody(searchCss, ".search-panel__input");
  const searchSubmit = ruleBody(searchCss, ".search-panel__submit");
  assert.match(searchPanelSource, /placeholder = "搜索视频标题或作者"/);
  assert.match(searchPanelSource, /placeholder=\{placeholder\}/);
  assert.doesNotMatch(searchPanelSource, /搜索视频标题或作者\.\.\./);
  assert.match(searchPanelSource, /const SEARCH_DEBOUNCE_MS = 500;/);
  assert.match(searchPanelSource, /window\.setTimeout\(\(\) => \{\s*commitSearch\(keyword\);/);
  assert.match(searchPanelSource, /onSearch\(q\);/);
  assert.match(searchPanelSource, /variant\?: "default" \| "uiverse";/);
  assert.match(searchPanelSource, /const isUiverse = variant === "uiverse";/);
  assert.match(searchPanelSource, /isUiverse \? " search-panel--uiverse" : ""/);
  assert.match(searchPanelSource, /aria-label="清空搜索"/);
  assert.match(searchPanelSource, /className=\{`search-panel__reset\$\{keyword \? " is-visible" : ""\}`\}/);
  assert.match(searchPanelSource, /className="search-panel__uiverse-submit" type="submit" aria-label="搜索"/);
  assert.match(searchPanelSource, /M7\.667 12\.667A5\.333 5\.333 0 107\.667 2a5\.333 5\.333 0 000 10\.667zM14\.334 14l-2\.9-2\.9/);
  assert.match(searchPanelSource, /className="search-panel__uiverse-submit"[\s\S]*?\{searchInput\}[\s\S]*?className=\{`search-panel__reset/);
  assert.doesNotMatch(searchPanelSource, /onChange=\{\(e\) => navigate/);
  assert.match(searchForm, /padding\s*:\s*4px/);
  assert.match(searchInput, /height\s*:\s*36px/);
  assert.match(searchSubmit, /height\s*:\s*36px/);

  const uiversePanel = ruleBody(searchCss, ".search-panel--uiverse");
  const uiverseForm = ruleBody(searchCss, ".search-panel--uiverse .search-panel__form");
  const uiverseFormBefore = ruleBody(searchCss, ".search-panel--uiverse .search-panel__form::before");
  const focusedUiverseForm = ruleBody(searchCss, ".search-panel--uiverse .search-panel__form:focus-within");
  const focusedUiverseFormBefore = ruleBody(searchCss, ".search-panel--uiverse .search-panel__form:focus-within::before");
  const visibleReset = ruleBody(searchCss, ".search-panel--uiverse .search-panel__reset.is-visible");
  assert.match(uiversePanel, /width\s*:\s*100%/);
  assert.match(uiversePanel, /--search-uiverse-height\s*:\s*40px/);
  assert.match(uiversePanel, /--search-uiverse-bg\s*:\s*color-mix\(in srgb, var\(--bg-surface\) 42%, transparent\)/);
  assert.match(uiversePanel, /--search-uiverse-border-color\s*:\s*var\(--accent\)/);
  assert.match(uiverseForm, /border-radius\s*:\s*var\(--search-uiverse-border-radius\)/);
  assert.match(uiverseForm, /border\s*:\s*1px solid var\(--border-default\)/);
  assert.match(uiverseForm, /backdrop-filter\s*:\s*blur\(16px\) saturate\(140%\)/);
  assert.match(uiverseForm, /box-shadow\s*:\s*var\(--shadow-sm\), var\(--shadow-inset\)/);
  assert.match(uiverseFormBefore, /transform\s*:\s*scaleX\(0\)/);
  assert.match(focusedUiverseForm, /border-radius\s*:\s*var\(--search-uiverse-focus-radius\)/);
  assert.match(focusedUiverseFormBefore, /transform\s*:\s*scaleX\(1\)/);
  assert.match(visibleReset, /visibility\s*:\s*visible/);
  assert.match(searchCss, /\.search-panel--uiverse svg\s*\{[^}]*width\s*:\s*17px/);
  assert.match(searchCss, /\.search-panel--uiverse \.search-panel__input\s*\{[^}]*color\s*:\s*var\(--text-strong\)/);
  assert.match(searchCss, /\.search-panel--uiverse \.search-panel__reset\s*\{[^}]*color\s*:\s*var\(--text-muted\)/);
  assert.doesNotMatch(searchCss, /\.search-panel--uiverse \.search-panel__submit\s*\{/);

  assert.match(homePageSource, /const homeLoading = rankingLoading \|\| latestLoading/);
  assert.match(homePageSource, /import \{ AdminEmptyVisual \} from "@\/admin\/AdminEmptyVisual"/);
  assert.doesNotMatch(homePageSource, /const \[searchQuery, setSearchQuery\] = useState\(""\)/);
  assert.match(homePageSource, /const \[searchParams, setSearchParams\] = useSearchParams\(\)/);
  assert.match(homePageSource, /const activeSearchQuery = searchParams\.get\("q"\)\?\.trim\(\) \?\? ""/);
  assert.match(homePageSource, /const activeTag = searchParams\.get\("tag"\)\?\.trim\(\) \?\? ""/);
  assert.match(homePageSource, /const \[searchSort, setSearchSort\] = useState<SortKey>\("hot"\)/);
  assert.match(homePageSource, /const \[searchView, setSearchView\] = useState<ViewMode>\("grid"\)/);
  assert.match(homePageSource, /const resetSearchResults = useCallback\(\(\) => \{\s*setSearchPage\(1\);\s*setSearchSort\("hot"\);/);
  assert.match(homePageSource, /const handleSearch = useCallback\(\(keyword: string\) => \{[\s\S]*?const q = keyword\.trim\(\);[\s\S]*?setSearchParams\(/);
  assert.match(homePageSource, /next\.set\("q", q\);[\s\S]*?next\.delete\("tag"\);/);
  assert.match(homePageSource, /next\.delete\("q"\);/);
  assert.match(homePageSource, /\{ replace: true \}/);
  assert.match(homePageSource, /<SearchPanel[\s\S]*?value=\{activeSearchQuery\}[\s\S]*?onSearch=\{handleSearch\}[\s\S]*?variant="uiverse"[\s\S]*?placeholder=""[\s\S]*?\/>/);
  assert.match(homePageSource, /const searchPageSize = isMobile\s*\? MOBILE_VIDEO_PAGE_SIZE\s*:\s*HOME_SEARCH_DESKTOP_PAGE_SIZE/);
  assert.match(homePageSource, /fetchListing\(searchPage,\s*searchPageSize,\s*\{[\s\S]*?q: activeSearchQuery,[\s\S]*?tag: activeTag,[\s\S]*?sort: searchSort/);
  assert.match(homePageSource, /setSearchPage\(1\);\s*\}, \[searchPageSize\]\)/);
  assert.match(homePageSource, /setSearchPage\(1\);\s*setSearchSort\("hot"\);\s*\}, \[activeSearchQuery, activeTag\]\)/);
  assert.match(homePageSource, /const hasActiveTag = activeTag\.length > 0/);
  assert.match(homePageSource, /const hasActiveFilter = hasActiveSearch \|\| hasActiveTag/);
  assert.doesNotMatch(homePageSource, /搜索结果：/);
  assert.match(homePageSource, /<SortToolbar[\s\S]*?sort=\{searchSort\}[\s\S]*?view=\{searchView\}/);
  assert.match(homePageSource, /setSearchSort\(nextSort\);[\s\S]*?setSearchPage\(1\);/);
  assert.match(homePageSource, /onViewChange=\{setSearchView\}/);
  assert.match(homePageSource, /compact=\{searchView === "compact"\}/);
  assert.match(homePageSource, /variant="no-results"[\s\S]*?text="未查询到"[\s\S]*?className="admin-empty-state admin-empty-state--plain home-empty-state"/);
  assert.match(homePageSource, /const searchTotalPages = Math\.max\(1, Math\.ceil\(searchTotal \/ searchPageSize\)\)/);
  assert.match(homePageSource, /<Pagination[\s\S]*?page=\{searchPage\}[\s\S]*?pageSize=\{searchPageSize\}/);
  assert.match(homePageSource, /const hasAnyVideos = ranking\.length > 0 \|\| latest\.length > 0/);
  assert.match(homePageSource, /const \[latestError, setLatestError\] = useState\(false\)/);
  assert.match(homePageSource, /const hasHomeError = rankingError \|\| latestError/);
  assert.match(homePageSource, /const showEmptyHome = !homeLoading && !hasHomeError && !hasAnyVideos/);
  assert.match(homePageSource, /const homeRequestVersion = useRef\(1\)/);
  assert.match(homePageSource, /if \(requestVersion !== homeRequestVersion\.current\) return/);
  assert.match(homePageSource, /emptyText=\{latestError \? "最新视频加载失败，请刷新重试" : undefined\}/);
  assert.doesNotMatch(homePageSource, /cachedRanking = null;\s*setRankingVideos\(\[\]\);\s*setRankingError\(true\)/);
  assert.match(homePageSource, /\{!hasActiveSearch && \(\s*hasAnyVideos \|\| hasActiveTag \? \(\s*<TagCloud linkBasePath="\/" onTagSelect=\{resetSearchResults\} \/>\s*\) : \(\s*<div className="tag-cloud-container is-reserved" aria-hidden="true" \/>\s*\)\s*\)\}/);
  assert.match(homePageSource, /<SectionHeader title="随机推荐" \/>/);
  assert.match(homePageSource, /<SectionHeader title="最新视频" \/>/);
  assert.doesNotMatch(homePageSource, /随机展示/);
  assert.doesNotMatch(homePageSource, /共 \$\{latest\.length\} 个/);
  assert.match(homePageSource, /className="container page-section home-discovery-section"/);
  assert.match(homePageSource, /className="container page-section home-primary-section"/);
  assert.match(homePageSource, /variant="empty"[\s\S]*?text="当前库中没有视频"[\s\S]*?className="admin-empty-state admin-empty-state--plain home-empty-state"/);
  assert.doesNotMatch(homePageSource, /className="home-empty"/);
  assert.doesNotMatch(homePageSource, /当前没有可播放视频/);

  const discoverySection = ruleBody(layoutCss, ".home-discovery-section");
  const primaryHeader = ruleBody(layoutCss, ".home-primary-section .section-header");
  assert.match(discoverySection, /padding-bottom\s*:\s*var\(--space-2\)/);
  assert.match(primaryHeader, /margin-top\s*:\s*var\(--space-2\)/);

  assert.doesNotMatch(layoutCss, /\.home-empty\s*\{/);
  const homeEmptyState = ruleBody(layoutCss, ".admin-empty-state.home-empty-state");
  assert.match(homeEmptyState, /min-height\s*:\s*clamp\(360px,\s*58vh,\s*620px\)/);
  assert.match(homeEmptyState, /padding\s*:\s*72px 16px 24px/);
});
