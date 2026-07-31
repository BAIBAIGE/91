import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const videosPageSource = readFileSync(new URL("../src/admin/VideosPage.tsx", import.meta.url), "utf8");
const apiSource = readFileSync(new URL("../src/admin/api.ts", import.meta.url), "utf8");
const emptyVisualSource = readFileSync(new URL("../src/admin/AdminEmptyVisual.tsx", import.meta.url), "utf8");
const adminCss = readFileSync(new URL("../src/styles/admin.css", import.meta.url), "utf8");

test("admin empty visual places the requested image above its text", () => {
  assert.match(emptyVisualSource, /import emptyImage from "@\/assets\/admin\/empty\.webp"/);
  assert.match(emptyVisualSource, /import noResultsImage from "@\/assets\/admin\/no-results\.webp"/);
  assert.match(emptyVisualSource, /variant === "no-results" \? noResultsImage : emptyImage/);
  assert.match(emptyVisualSource, /admin-empty-visual__media[\s\S]*?<img[\s\S]*?admin-empty-visual__text/);
});

test("normal videos use ten items while blacklist remains responsive", () => {
  assert.match(videosPageSource, /const NORMAL_VIDEOS_PAGE_SIZE = 10;/);
  assert.match(videosPageSource, /const DESKTOP_VIDEOS_PAGE_SIZE = 50;/);
  assert.match(videosPageSource, /const MOBILE_VIDEOS_PAGE_SIZE = 20;/);
  assert.match(videosPageSource, /const VIDEOS_MOBILE_QUERY = "\(max-width: 640px\)";/);
  assert.match(videosPageSource, /window\.matchMedia\(VIDEOS_MOBILE_QUERY\)/);
  assert.match(videosPageSource, /function CurrentVideosTab[\s\S]*?const pageSize = NORMAL_VIDEOS_PAGE_SIZE;/);
  assert.match(videosPageSource, /function BlacklistTab[\s\S]*?const pageSize = useVideosPageSize\(\);/);
  assert.match(videosPageSource, /api\.listVideos\(\{[\s\S]*?page,[\s\S]*?size: pageSize,[\s\S]*?keyword: searchKeyword,[\s\S]*?\.\.\.appliedFilters/);
});

test("normal videos support composable advanced filters", () => {
  const clearFiltersSource = videosPageSource.slice(
    videosPageSource.indexOf("function clearAdvancedFilters"),
    videosPageSource.indexOf("\n\n  return (", videosPageSource.indexOf("function clearAdvancedFilters"))
  );
  const sourcePickerSource = videosPageSource.slice(
    videosPageSource.indexOf("function VideoSourcePicker"),
    videosPageSource.indexOf("function AdvancedVideoFilters")
  );
  const advancedFilterSource = videosPageSource.slice(
    videosPageSource.indexOf("function AdvancedVideoFilters"),
    videosPageSource.indexOf("function SearchBox")
  );
  assert.match(videosPageSource, /className="admin-video-advanced-filters"/);
  assert.match(videosPageSource, /aria-haspopup="dialog"/);
  assert.match(videosPageSource, /<Modal[\s\S]*?open=\{advancedFiltersOpen\}[\s\S]*?title="高级筛选"/);
  assert.match(advancedFilterSource, /<span>来源<\/span>[\s\S]*?<VideoSourcePicker/);
  assert.match(sourcePickerSource, /aria-haspopup="listbox"[\s\S]*?aria-expanded=\{open\}/);
  assert.match(sourcePickerSource, /role="listbox" aria-label="来源"/);
  assert.match(sourcePickerSource, /role="group" aria-label="网盘"/);
  assert.match(sourcePickerSource, /role="group" aria-label="爬虫"/);
  assert.match(sourcePickerSource, /placeholder="搜索网盘或爬虫"/);
  assert.match(sourcePickerSource, /driveKindIconPath\(drive\.kind\)/);
  assert.match(sourcePickerSource, /<SpiderIcon \/>/);
  assert.match(videosPageSource, /const LOCAL_UPLOAD_SOURCE_ID = "local-upload";/);
  assert.match(sourcePickerSource, /<strong>上传来源<\/strong>/);
  assert.match(sourcePickerSource, /selectSource\("drive", LOCAL_UPLOAD_SOURCE_ID\)/);
  assert.match(sourcePickerSource, /<VideoSourcePickerIcon upload \/>/);
  assert.ok(
    sourcePickerSource.indexOf("<strong>上传来源</strong>") >
      sourcePickerSource.indexOf('role="group" aria-label="爬虫"')
  );
  assert.doesNotMatch(sourcePickerSource, /<Bot\b/);
  assert.match(sourcePickerSource, /document\.addEventListener\("pointerdown", closeOnOutsidePointer\)/);
  assert.match(sourcePickerSource, /useLayoutEffect\(\(\) => \{[\s\S]*?getBoundingClientRect\(\)[\s\S]*?spaceBelow < 240 && spaceAbove > spaceBelow/);
  assert.match(sourcePickerSource, /window\.addEventListener\("scroll", updateMenuPosition, true\)/);
  assert.match(sourcePickerSource, /\[role="option"\]\[aria-selected="true"\][\s\S]*?scrollIntoView\(\{ block: "nearest" \}\)/);
  assert.match(sourcePickerSource, /data-placement=\{menuPosition\?\.placement \?\? "bottom"\}/);
  assert.match(sourcePickerSource, /visibility: menuPosition \? "visible" : "hidden"/);
  assert.match(sourcePickerSource, /event\.key === "Escape" && open/);
  assert.match(sourcePickerSource, /event\.key === "Tab"[\s\S]*?!pickerRoot\.contains\(activeElement\)[\s\S]*?setOpen\(false\)/);
  assert.doesNotMatch(sourcePickerSource, /onBlur=|relatedTarget/);
  assert.match(sourcePickerSource, /function openPicker\(focusTarget\?: "first" \| "last"\)/);
  assert.doesNotMatch(sourcePickerSource, /searchRef\.current\?\.focus\(\)/);
  assert.doesNotMatch(sourcePickerSource, /admin-video-source-picker__badge|<small>/);
  assert.doesNotMatch(sourcePickerSource, />网盘与爬虫<|>PikPak<|>脚本爬虫</);
  assert.doesNotMatch(sourcePickerSource, /filtered(?:Drives|Crawlers)\.length\}[^)]*<\/div>/);
  assert.equal(Array.from(sourcePickerSource.matchAll(/<select/g)).length, 0);
  assert.doesNotMatch(videosPageSource, /<optgroup|<option value="">全部来源/);
  assert.match(adminCss, /\.admin-video-advanced-range__inputs\s*\{[^}]*grid-template-columns:\s*max-content auto max-content;[^}]*margin-top:\s*var\(--space-2\);/s);
  assert.match(adminCss, /input\[type="date"\]\s*\{[^}]*width:\s*136px;/s);
  assert.match(adminCss, /input\[type="number"\]\s*\{[^}]*width:\s*104px;/s);
  assert.doesNotMatch(adminCss, /\.admin-video-source-picker__badge/);
  assert.match(adminCss, /\.admin-video-source-picker__menu\s*\{[^}]*position:\s*fixed;[^}]*z-index:\s*calc\(var\(--z-modal\) \+ 1\);[^}]*display:\s*flex;/s);
  assert.match(adminCss, /\.admin-video-source-picker__list\s*\{[^}]*flex:\s*1 1 auto;[^}]*min-height:\s*0;/s);
  assert.doesNotMatch(adminCss, /\.admin-video-source-picker\.is-open \.admin-video-source-picker__trigger/);
  assert.doesNotMatch(adminCss, /\.admin-video-source-picker__group-title span:last-child/);
  assert.match(videosPageSource, /<legend>入库时间<\/legend>/);
  assert.match(videosPageSource, /<legend>视频时长\(分钟\)<\/legend>/);
  assert.doesNotMatch(advancedFilterSource, /<span>上传来源<\/span>/);
  assert.doesNotMatch(videosPageSource, /admin-video-upload-source-options|uploadSource/);
  assert.equal(Array.from(advancedFilterSource.matchAll(/admin-video-advanced-range__placeholder/g)).length, 2);
  assert.equal(Array.from(advancedFilterSource.matchAll(/年\/月\/日/g)).length, 2);
  assert.match(videosPageSource, />\s*应用\s*<\/button>/);
  assert.doesNotMatch(videosPageSource, /应用筛选/);
  assert.doesNotMatch(advancedFilterSource, /<span>(开始|结束|最短|最长)<\/span>/);
  assert.doesNotMatch(advancedFilterSource, /入库日期包含开始和结束当天|视频时长按分钟计算/);
  assert.doesNotMatch(adminCss, /\.admin-video-advanced-filters__hint/);
  for (const label of ["入库开始日期", "入库截止日期", "视频最短时长（分钟）", "视频最长时长（分钟）"]) {
    assert.match(advancedFilterSource, new RegExp(`aria-label="${label}"`));
  }
  assert.match(videosPageSource, /type="number"[\s\S]*?value=\{value\.durationMinMinutes\}/);
  assert.match(videosPageSource, /type="number"[\s\S]*?value=\{value\.durationMaxMinutes\}/);
  assert.equal(Array.from(videosPageSource.matchAll(/type="date"/g)).length, 2);
  assert.match(
    advancedFilterSource,
    /value=\{value\.createdFrom\}[\s\S]*?max=\{earlierDateInputValue\(value\.createdTo, today\)\}/
  );
  assert.match(advancedFilterSource, /value=\{value\.createdTo\}[\s\S]*?max=\{today\}/);
  assert.match(videosPageSource, /function localDateInputValue\(date: Date\): string/);
  assert.match(videosPageSource, /show\("入库时间不能超过当天", "error"\)/);
  assert.equal(
    Array.from(
      videosPageSource.matchAll(/onClick=\{\(event\) => openNativeDatePicker\(event\.currentTarget\)\}/g)
    ).length,
    2
  );
  assert.match(videosPageSource, /function openNativeDatePicker\(input: HTMLInputElement\)[\s\S]*?input\.showPicker\(\)/);
  assert.doesNotMatch(videosPageSource, /视频时间|publishedFrom|publishedTo/);
  assert.match(videosPageSource, /Promise\.all\(\[api\.listTags\(\), api\.listDrives\(\), api\.listCrawlers\(\)\]\)/);
  assert.match(videosPageSource, /setAppliedFilters\(\{ \.\.\.draftFilters \}\)/);
  assert.match(clearFiltersSource, /setDraftFilters\(\{ \.\.\.EMPTY_VIDEO_FILTERS \}\)/);
  assert.doesNotMatch(clearFiltersSource, /setAppliedFilters|setPage|setAdvancedFiltersOpen/);
  assert.match(videosPageSource, /const activeAdvancedFilterCount = countVideoAdvancedFilters\(appliedFilters\);/);
  for (const key of ["driveId", "crawlerId", "createdFrom", "createdTo", "durationMinMinutes", "durationMaxMinutes"]) {
    assert.match(apiSource, new RegExp(`if \\(params\\.${key}\\) qs\\.set\\("${key}", params\\.${key}\\)`));
  }
  assert.doesNotMatch(apiSource, /publishedFrom|publishedTo/);
  assert.match(adminCss, /\.admin-modal\.admin-modal--video-filters\s*\{[^}]*width\s*:\s*min\(700px,\s*100%\)/s);
  assert.match(adminCss, /\.admin-video-advanced-filters__grid\s*\{[^}]*grid-template-columns\s*:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/s);
  assert.match(adminCss, /\.admin-modal--video-filters \.admin-video-source-picker,\s*\.admin-modal--video-filters \.admin-video-advanced-range__inputs\s*\{[^}]*width:\s*min\(100%,\s*294px\);[^}]*margin-inline:\s*auto/s);
  assert.match(adminCss, /@media \(max-width: 520px\)[\s\S]*?\.admin-video-advanced-range__inputs\s*\{[^}]*margin-top:\s*8px/s);
  assert.match(adminCss, /@media \(max-width: 520px\)[\s\S]*?\.admin-video-advanced-range__inputs\.is-date-range\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto minmax\(0,\s*1fr\)/s);
  assert.match(adminCss, /@media \(max-width: 520px\)[\s\S]*?\.admin-video-advanced-range__inputs\.is-duration-range\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\) auto minmax\(0,\s*1fr\)/s);
  assert.match(adminCss, /@media \(max-width: 520px\)[\s\S]*?\.admin-video-advanced-range input\[type="date"\],[\s\S]*?height:\s*40px;[^}]*border:\s*0;/s);
  assert.match(adminCss, /\.admin-modal--video-filters \.admin-video-source-picker__trigger\s*\{[^}]*min-height:\s*42px;[^}]*border:\s*0;/s);
  assert.match(adminCss, /\.admin-modal--video-filters \.admin-modal__footer\s*\{[^}]*display:\s*grid;[^}]*grid-template-columns:\s*auto repeat\(2,\s*minmax\(0,\s*1fr\)\)/s);
  assert.match(videosPageSource, /className="admin-btn admin-video-advanced-clear"/);
});

test("admin video searches debounce typed input before querying", () => {
  assert.match(videosPageSource, /const ADMIN_SEARCH_DEBOUNCE_MS = 500;/);
  assert.equal(
    Array.from(videosPageSource.matchAll(/}, ADMIN_SEARCH_DEBOUNCE_MS\);/g)).length,
    2
  );
  assert.match(videosPageSource, /onChange=\{\(e\) => onChange\(e\.target\.value\)\}/);
  assert.doesNotMatch(videosPageSource, /onChange=\{\(e\) => setSearchKeyword/);
});

test("admin video pagination only shows current and total pages", () => {
  const paginationCalls = Array.from(
    videosPageSource.matchAll(/<Pagination page=\{page\} totalPages=\{totalPages\} onPage=\{setPage\} \/>/g)
  );
  assert.match(videosPageSource, /第 \{page\} \/ \{totalPages\} 页/);
  assert.doesNotMatch(videosPageSource, /每页 \{pageSize\} 个/);
  assert.doesNotMatch(videosPageSource, /<Pagination[^>]*pageSize=\{pageSize\}/);
  assert.equal(paginationCalls.length, 2);
  assert.equal(
    Array.from(videosPageSource.matchAll(/\{showPagination && <Pagination page=\{page\} totalPages=\{totalPages\} onPage=\{setPage\} \/>\}/g)).length,
    2
  );
});

test("admin video pagination stores the active page in the URL", () => {
  assert.match(videosPageSource, /const page = readAdminVideosPage\(searchParams\);/);
  assert.match(videosPageSource, /return withAdminVideosPage\(prev, resolvedPage\);/);
  assert.match(videosPageSource, /if \(key !== activeTab\) next\.delete\("page"\);/);
  assert.doesNotMatch(videosPageSource, /const \[page, setPage\] = useState\(1\);/);
});

test("admin video pagination clamps stale deep links after totals load", () => {
  const currentSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const blacklistSource = videosPageSource.slice(videosPageSource.indexOf("function BlacklistTab"));
  // Both tabs must wait for the load to settle before clamping, otherwise the
  // initial total of 0 would discard the page restored from the URL.
  const clamp = /!loading\s*&&\s*page\s*>\s*totalPages[\s\S]{0,40}setPage\(totalPages\)/;
  assert.match(currentSource, clamp);
  assert.match(blacklistSource, clamp);
});

test("video pagination keeps its position when the page has fewer rows", () => {
  const currentSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const blacklistSource = videosPageSource.slice(videosPageSource.indexOf("function BlacklistTab"));
  assert.equal(Array.from(videosPageSource.matchAll(/const showPagination = totalPages > 1;/g)).length, 2);
  assert.match(currentSource, /const placeholderRows = showPagination \? Math\.max\(0, pageSize - listItems\.length\) : 0;/);
  assert.match(blacklistSource, /const placeholderRows = showPagination \? Math\.max\(0, pageSize - list\.length\) : 0;/);
  assert.equal(Array.from(videosPageSource.matchAll(/Array\.from\(\{ length: placeholderRows \}/g)).length, 2);
  assert.match(videosPageSource, /className="admin-video-placeholder-row"/);
  assert.match(blacklistSource, /admin-table is-selectable admin-blacklist-table/);
  assert.match(blacklistSource, /data-label="文件名"[\s\S]*?admin-blacklist-filecell[\s\S]*?placeholder/);
  assert.match(
    adminCss,
    /\.admin-video-placeholder-row\s*\{[^}]*visibility\s*:\s*hidden;[^}]*pointer-events\s*:\s*none/s
  );
});

test("empty video tabs use the correct visual and distinguish search misses", () => {
  const currentSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const blacklistSource = videosPageSource.slice(videosPageSource.indexOf("function BlacklistTab"));
  assert.match(currentSource, /const hasActiveSearch = searchKeyword\.trim\(\)\.length > 0 \|\| activeAdvancedFilterCount > 0;/);
  assert.match(blacklistSource, /const hasActiveSearch = searchKeyword\.trim\(\)\.length > 0;/);
  assert.match(currentSource, /const hasVideoActions = listItems\.length > 0;/);
  assert.match(blacklistSource, /const hasBlacklistActions = list\.length > 0;/);
  assert.match(currentSource, /\{hasVideoActions && \(\s*<button[\s\S]*?批量选择/);
  assert.match(blacklistSource, /\{hasBlacklistActions && \(\s*<div className="admin-videos-filter__actions admin-blacklist-source-delete">[\s\S]*?删除全部[\s\S]*?批量选择/);
  assert.match(currentSource, /admin-empty-state admin-empty-state--plain/);
  assert.match(blacklistSource, /admin-empty-state admin-empty-state--plain/);
  assert.match(currentSource, /variant=\{hasActiveSearch \? "no-results" : "empty"\}/);
  assert.match(blacklistSource, /variant=\{hasActiveSearch \? "no-results" : "empty"\}/);
  assert.match(currentSource, /hasActiveSearch \? "未查询到" : "当前库中没有视频"/);
  assert.match(blacklistSource, /hasActiveSearch \? "未查询到" : "暂无拉黑视频"/);
  assert.match(blacklistSource, /暂无拉黑视频/);
  assert.doesNotMatch(currentSource, /还没有视频。先在「网盘管理」里配置好盘并触发扫描，或调整搜索词。/);
  assert.doesNotMatch(blacklistSource, /黑名单为空/);
  assert.doesNotMatch(currentSource, /<Image size=\{48\}/);
  assert.doesNotMatch(blacklistSource, /<Ban size=\{48\}/);
  assert.match(
    adminCss,
    /\.admin-empty-state--plain\s*\{[^}]*border\s*:\s*0;[^}]*background\s*:\s*transparent/s
  );
  assert.match(
    adminCss,
    /\.admin-videos-current,[\s\S]*?\.admin-videos-blacklist\s*\{[^}]*display\s*:\s*flex;[^}]*flex-direction\s*:\s*column;[^}]*min-height\s*:\s*calc\(100vh - \(var\(--space-7\) \* 2\)\)/s
  );
  assert.match(
    adminCss,
    /\.admin-videos-current > \.admin-empty-state--plain,[\s\S]*?\.admin-videos-blacklist > \.admin-empty-state--plain\s*\{[^}]*box-sizing\s*:\s*border-box;[^}]*flex\s*:\s*1 1 auto;[^}]*min-height\s*:\s*0;[^}]*padding\s*:\s*0 16px 96px/s
  );
  assert.doesNotMatch(adminCss, /translateY\(-48px\)/);
});

test("video tabs show a loading state while fetching data", () => {
  const currentSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const blacklistSource = videosPageSource.slice(videosPageSource.indexOf("function BlacklistTab"));
  assert.match(currentSource, /loading \? \(\s*<LoadingState \/>/);
  assert.match(blacklistSource, /loading \? \(\s*<LoadingState \/>/);
  assert.match(videosPageSource, /function LoadingState\(\)/);
  assert.match(videosPageSource, /function LoadingState\(\)[\s\S]*?<AdminLoading \/>/);
});

test("admin videos batch delete runs deletions sequentially", () => {
  assert.match(videosPageSource, /for \(const id of ids\) \{/);
  assert.match(videosPageSource, /const result = await api\.deleteVideo\(id, \{ deleteSource: batchDeleteSource \}\);/);
  assert.doesNotMatch(
    videosPageSource,
    /Promise\.allSettled\(\s*ids\.map\(\(id\) => api\.deleteVideo\(id(?:, [^)]+)?\)\)\s*\)/
  );
});

test("admin video selections persist across pages and can include the current page", () => {
  const currentSource = videosPageSource.slice(
    videosPageSource.indexOf("function CurrentVideosTab"),
    videosPageSource.indexOf("// ---------- 拉黑视频 ----------")
  );
  const refreshSource = currentSource.slice(
    currentSource.indexOf("async function refresh()"),
    currentSource.indexOf("async function refreshListOnly()")
  );

  assert.doesNotMatch(refreshSource, /setSelectedIds\(new Set\(\)\)/);
  assert.match(currentSource, /const selectPageVideos = \(\) => \{[\s\S]*?new Set\(current\)[\s\S]*?listItems\.forEach\(\(video\) => next\.add\(video\.id\)\)/);
  assert.match(currentSource, /listItems\.every\(\(video\) => selectedIds\.has\(video\.id\)\)/);
  assert.match(currentSource, /message=\{`确定要删除已选中的 \$\{selectedIds\.size\} 个视频吗？`\}/);
  assert.match(currentSource, /listItems\.every\(\(video\) => deletedIds\.has\(video\.id\)\)/);
  assert.doesNotMatch(currentSource, /success >= listItems\.length/);
});

test("admin videos track preview regeneration after it is accepted", () => {
  assert.match(videosPageSource, /const REGEN_PREVIEW_STATUS = "generating";/);
  assert.match(videosPageSource, /const \[regenPreviewById, setRegenPreviewById\]/);
  assert.match(videosPageSource, /trackRegeneratingPreview\(\[v\]\)/);
  assert.doesNotMatch(videosPageSource, /data-label="预览视频"[\s\S]*?<PreviewStatus/);
  assert.match(videosPageSource, /onRegenPreview=\{\(\) => handleRegen\(editingVideo\)\}/);
  assert.match(videosPageSource, /className="admin-btn admin-video-preview-button"/);
  assert.match(videosPageSource, /refreshListOnly\(\)/);
});

test("admin videos keep generating status after page refresh", () => {
  assert.match(videosPageSource, /const hasGeneratingPreview = list\.some\(\(v\) => v\.previewStatus === REGEN_PREVIEW_STATUS\);/);
  assert.match(videosPageSource, /if \(trackedRegenCount === 0 && !hasGeneratingPreview\) return;/);
  assert.match(videosPageSource, /function isPreviewGenerating\(v: api\.AdminVideo\)/);
  assert.match(videosPageSource, /return !!regenPreviewById\[v\.id\] \|\| v\.previewStatus === REGEN_PREVIEW_STATUS;/);
  assert.match(videosPageSource, /previewGenerating=\{isPreviewGenerating\(editingVideo\)\}/);
  assert.match(videosPageSource, /disabled=\{saving \|\| previewBusy\}/);
});
