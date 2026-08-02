import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const sources = {
  users: readFileSync(new URL("../src/admin/UsersPage.tsx", import.meta.url), "utf8"),
  crawlers: readFileSync(
    new URL("../src/admin/CrawlersPage.tsx", import.meta.url),
    "utf8"
  ),
  tags: readFileSync(new URL("../src/admin/TagsPage.tsx", import.meta.url), "utf8"),
  backups: readFileSync(
    new URL("../src/admin/BackupPage.tsx", import.meta.url),
    "utf8"
  ),
  videos: readFileSync(
    new URL("../src/admin/VideosPage.tsx", import.meta.url),
    "utf8"
  ),
  logs: readFileSync(new URL("../src/admin/LogsPage.tsx", import.meta.url), "utf8"),
  videoDetail: readFileSync(
    new URL("../src/pages/VideoDetailPage.tsx", import.meta.url),
    "utf8"
  ),
};

function confirmModalBlock(source: string, marker: string) {
  const markerIndex = source.indexOf(marker);
  assert.notEqual(markerIndex, -1, `confirmation marker not found: ${marker}`);

  const start = source.lastIndexOf("<ConfirmModal", markerIndex);
  assert.notEqual(start, -1, `ConfirmModal not found for: ${marker}`);

  const next = source.indexOf("<ConfirmModal", markerIndex + marker.length);
  return source.slice(start, next === -1 ? source.length : next);
}

test("delete and clear confirmation dialogs omit warning icons", () => {
  const destructiveDialogs = [
    [sources.users, 'title="删除用户"'],
    [sources.crawlers, 'title="删除爬虫"'],
    [sources.tags, '"删除选中标签"'],
    [sources.backups, 'title="删除备份"'],
    [sources.videos, "open={deleteTarget !== null}"],
    [sources.videos, 'title="批量删除视频"'],
    [sources.videos, 'title="删除全部黑名单源文件"'],
    [sources.videos, 'title="删除源文件"'],
    [sources.videos, 'title="批量删除拉黑视频源文件"'],
    [sources.logs, 'title="清空日志"'],
  ] as const;

  for (const [source, marker] of destructiveDialogs) {
    const dialog = confirmModalBlock(source, marker);
    assert.match(
      dialog,
      /\bhideIcon\b/,
      `${marker} should hide the warning icon`
    );
    assert.doesNotMatch(
      dialog,
      /\bdetails=/,
      `${marker} should keep destructive confirmation copy concise`
    );
  }

  assert.match(
    sources.videoDetail,
    /确定删除「\{detail\.title\}」吗？/
  );
  assert.doesNotMatch(
    sources.videoDetail,
    /vd-delete-text[\s\S]*?此操作会从管理库移除该视频。/
  );
});
