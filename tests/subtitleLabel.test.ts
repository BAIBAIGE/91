import assert from "node:assert/strict";
import test from "node:test";
import {
  escapeHtml,
  formatSubtitleLabel,
  formatSubtitleOptionLabel,
  formatSubtitleTooltipLabel,
  truncateEnd,
  truncateMiddle,
} from "../src/lib/subtitleLabel";

test("short names are shown in full", () => {
  const subtitle = { name: "chs.srt", language: "chi", ext: "srt" };
  assert.equal(formatSubtitleOptionLabel(subtitle, 0), "chi · chs · SRT");
});

test("long release names keep their head and tail", () => {
  const subtitle = {
    name: "Some.Movie.2019.1080p.WEB-DL.H264.AAC-GROUP.chs.srt",
    language: "chi",
    ext: "srt",
  };
  assert.equal(
    formatSubtitleOptionLabel(subtitle, 0),
    "chi · Some.Movie…GROUP.chs · SRT"
  );
});

test("the full label keeps the whole name for the title tooltip", () => {
  const subtitle = {
    name: "Some.Movie.2019.1080p.WEB-DL.H264.AAC-GROUP.chs.srt",
    language: "chi",
    ext: "srt",
  };
  assert.equal(
    formatSubtitleLabel(subtitle, 0),
    "chi · Some.Movie.2019.1080p.WEB-DL.H264.AAC-GROUP.chs · SRT"
  );
});

test("only the subtitle extension is stripped from the name", () => {
  assert.equal(
    formatSubtitleLabel({ name: "movie.2019.mp4", ext: "srt" }),
    "movie.2019.mp4 · SRT"
  );
  assert.equal(
    formatSubtitleLabel({ name: "movie.2019.ass", ext: "ass" }),
    "movie.2019 · ASS"
  );
});

test("full width characters count as two columns", () => {
  const subtitle = {
    name: "某部很长很长的电影名字简体中文字幕.srt",
    language: "chi",
    ext: "srt",
  };
  // 名字部分共 19 列，全角字符按 2 列计入预算。
  assert.equal(
    formatSubtitleOptionLabel(subtitle, 0),
    "chi · 某部很长很…中文字幕 · SRT"
  );
});

test("the tooltip is shorter than the option row", () => {
  const subtitle = {
    name: "Some.Movie.2019.1080p.WEB-DL.H264.AAC-GROUP.chs.srt",
    language: "chi",
    ext: "srt",
  };
  assert.equal(formatSubtitleTooltipLabel(subtitle, 0), "chi · Some.Mo…");
});

test("missing fields fall back to the label, then to an index", () => {
  // 只有 label 时无从拆分，原样保留。
  assert.equal(
    formatSubtitleOptionLabel({ label: "chi · movie · SRT" }, 0),
    "chi · movie · SRT"
  );
  assert.equal(
    formatSubtitleOptionLabel(
      { label: "chi · movie · SRT", language: "chi", ext: "srt" },
      0
    ),
    "chi · movie · SRT"
  );
  assert.equal(formatSubtitleOptionLabel({}, 2), "字幕 3");
  assert.equal(formatSubtitleOptionLabel({}), "在线字幕");
});

test("whitespace in remote names is collapsed", () => {
  assert.equal(
    formatSubtitleLabel({ name: "  movie\n\tname  ", ext: " .SRT " }),
    "movie name · SRT"
  );
});

test("html in remote names is escaped", () => {
  assert.equal(
    escapeHtml(`<img src=x onerror="alert('x')">`),
    "&lt;img src=x onerror=&quot;alert(&#39;x&#39;)&quot;&gt;"
  );
});

test("truncation helpers respect the width budget", () => {
  assert.equal(truncateMiddle("abcdefghij", 10), "abcdefghij");
  assert.equal(truncateMiddle("abcdefghijk", 10), "abcde…hijk");
  assert.equal(truncateEnd("abcdefghijk", 10), "abcdefghi…");
  assert.equal(truncateEnd("中文中文中文", 6), "中文…");
});
