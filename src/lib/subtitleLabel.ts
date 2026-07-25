/**
 * 在线字幕的名字来自第三方接口，常见形态是完整的发布文件名
 * （例如 `Some.Movie.2019.1080p.WEB-DL.H264.chs.srt`）。
 * ArtPlayer 的设置面板宽度是固定的，过长的名字会被两侧裁掉，
 * 因此这里统一把名字压缩成可读的短标签，完整名字留给 title 提示。
 */

export type SubtitleLabelInput = {
  name?: string;
  label?: string;
  language?: string;
  ext?: string;
  type?: string;
};

/** 选择列表里名字部分的显示宽度上限（1 = 一个半角字符）。 */
const NAME_MAX_WIDTH = 20;
/** 语言字段一般是 chi/eng 这类短码，异常长值也不该挤掉后面的格式。 */
const LANGUAGE_MAX_WIDTH = 8;
/** 设置主面板里“字幕”一行右侧提示的显示宽度上限。 */
const TOOLTIP_MAX_WIDTH = 14;

const SUBTITLE_EXTENSIONS = ["vtt", "srt", "ass", "ssa"];

/** 完整标签，用于 title 提示与播放器内部的字幕名。 */
export function formatSubtitleLabel(
  subtitle: SubtitleLabelInput,
  index?: number
) {
  return joinLabelParts(
    subtitleLanguage(subtitle),
    subtitleName(subtitle),
    subtitleExtension(subtitle),
    index
  );
}

/** 选择列表中的短标签：只压缩名字，保留语言与格式。 */
export function formatSubtitleOptionLabel(
  subtitle: SubtitleLabelInput,
  index?: number
) {
  return joinLabelParts(
    truncateEnd(subtitleLanguage(subtitle), LANGUAGE_MAX_WIDTH),
    truncateMiddle(subtitleName(subtitle), NAME_MAX_WIDTH),
    subtitleExtension(subtitle),
    index
  );
}

/** 设置主面板右侧的提示文案，空间最紧张。 */
export function formatSubtitleTooltipLabel(
  subtitle: SubtitleLabelInput,
  index?: number
) {
  return truncateEnd(
    formatSubtitleOptionLabel(subtitle, index),
    TOOLTIP_MAX_WIDTH
  );
}

export function escapeHtml(value: string) {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function joinLabelParts(
  language: string,
  name: string,
  extension: string,
  index?: number
) {
  const parts = [language, name, extension].filter(Boolean);
  if (parts.length > 0) return parts.join(" · ");
  return typeof index === "number" ? `字幕 ${index + 1}` : "在线字幕";
}

function subtitleLanguage(subtitle: SubtitleLabelInput) {
  return collapseSpaces(subtitle.language ?? "");
}

/**
 * 名字里通常带着字幕扩展名，而扩展名已经单独展示了，去掉可以省出宽度。
 * 接口只给 label 时退回解析 label 的中间段。
 */
function subtitleName(subtitle: SubtitleLabelInput) {
  const name = collapseSpaces(subtitle.name ?? "") || labelName(subtitle);
  const lastDot = name.lastIndexOf(".");
  if (lastDot <= 0) return name;
  const ext = name.slice(lastDot + 1).toLowerCase();
  return SUBTITLE_EXTENSIONS.includes(ext) ? name.slice(0, lastDot) : name;
}

/** 后端用 ` · ` 拼接 label，这里剥掉已经单独展示的语言与格式段。 */
function labelName(subtitle: SubtitleLabelInput) {
  const label = collapseSpaces(subtitle.label ?? "");
  if (!label) return "";
  const parts = label.split(" · ").filter(Boolean);
  if (parts.length <= 1) return label;
  const language = subtitleLanguage(subtitle);
  const extension = subtitleExtension(subtitle);
  const rest = parts.filter(
    (part) => part !== language && part.toUpperCase() !== extension
  );
  return rest.join(" · ") || label;
}

function subtitleExtension(subtitle: SubtitleLabelInput) {
  const ext = collapseSpaces(subtitle.ext ?? subtitle.type ?? "");
  return ext.replace(/^\./, "").toUpperCase();
}

function collapseSpaces(value: string) {
  return value.replace(/\s+/g, " ").trim();
}

/** 中英混排时按显示宽度计数：全角算 2，半角算 1。 */
function displayWidth(text: string) {
  let width = 0;
  for (const char of text) width += charWidth(char);
  return width;
}

function charWidth(char: string) {
  const code = char.codePointAt(0) ?? 0;
  // CJK、假名、全角标点与表情符号都按两个半角字符占位。
  if (
    (code >= 0x1100 && code <= 0x115f) ||
    (code >= 0x2e80 && code <= 0xa4cf) ||
    (code >= 0xac00 && code <= 0xd7a3) ||
    (code >= 0xf900 && code <= 0xfaff) ||
    (code >= 0xfe30 && code <= 0xfe6f) ||
    (code >= 0xff00 && code <= 0xff60) ||
    (code >= 0xffe0 && code <= 0xffe6) ||
    code >= 0x1f300
  ) {
    return 2;
  }
  return 1;
}

/** 保留开头与结尾，省略中间——文件名的区分度多半在两端。 */
export function truncateMiddle(text: string, maxWidth: number) {
  if (displayWidth(text) <= maxWidth) return text;
  const budget = Math.max(0, maxWidth - 1);
  const head = takeWidth(text, Math.ceil(budget / 2));
  const tail = takeWidthFromEnd(text, budget - displayWidth(head));
  return `${head}…${tail}`;
}

export function truncateEnd(text: string, maxWidth: number) {
  if (displayWidth(text) <= maxWidth) return text;
  return `${takeWidth(text, Math.max(0, maxWidth - 1))}…`;
}

function takeWidth(text: string, maxWidth: number) {
  let width = 0;
  let out = "";
  for (const char of text) {
    const next = width + charWidth(char);
    if (next > maxWidth) break;
    out += char;
    width = next;
  }
  return out;
}

function takeWidthFromEnd(text: string, maxWidth: number) {
  const chars = Array.from(text);
  let width = 0;
  let out = "";
  for (let i = chars.length - 1; i >= 0; i -= 1) {
    const next = width + charWidth(chars[i]);
    if (next > maxWidth) break;
    out = chars[i] + out;
    width = next;
  }
  return out;
}
