import {
  Scalar,
  isMap,
  isScalar,
  parseDocument,
  stringify,
  type Pair,
  type ParsedNode,
  type Range,
  type YAMLMap,
} from "yaml";

export type SettingsDraft = {
  nightlyStartTime: string;
  builtinTagsEnabled: boolean;
};

export type VisualField = keyof SettingsDraft;

export const DEFAULT_DRAFT: SettingsDraft = {
  nightlyStartTime: "01:00",
  builtinTagsEnabled: true,
};

type ParsedMap = YAMLMap<ParsedNode, ParsedNode | null>;
type ParsedPair = Pair<ParsedNode, ParsedNode | null>;

type SourceEdit = {
  start: number;
  end: number;
  text: string;
};

type RangedNode = {
  range?: Range | null;
};

export function isValidStartTime(value: string) {
  if (!/^\d{2}:\d{2}$/.test(value)) return false;
  const [hour, minute] = value.split(":").map(Number);
  return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59;
}

export function configDocument(source: string) {
  const document = parseDocument(source, {
    keepSourceTokens: true,
    prettyErrors: true,
  });
  if (document.errors.length > 0) {
    throw new Error(document.errors[0].message);
  }
  const root = document.toJS();
  if (root !== null && (typeof root !== "object" || Array.isArray(root))) {
    throw new Error("config.yaml 顶层必须是映射对象");
  }
  return document;
}

function draftFromDocument(document: ReturnType<typeof configDocument>): SettingsDraft {
  const configuredStart = document.getIn(["nightly", "start_time"]);
  let nightlyStartTime = DEFAULT_DRAFT.nightlyStartTime;
  if (configuredStart !== undefined && configuredStart !== null) {
    if (typeof configuredStart !== "string" || !isValidStartTime(configuredStart)) {
      throw new Error("nightly.start_time 必须是 HH:mm 格式的有效时间");
    }
    nightlyStartTime = configuredStart;
  } else {
    const legacyHour = document.getIn(["nightly", "cron_hour"]);
    if (typeof legacyHour === "number" && legacyHour >= 1 && legacyHour <= 23) {
      nightlyStartTime = `${String(legacyHour).padStart(2, "0")}:00`;
    }
  }

  const tagsNode = document.get("tags", true);
  if (
    tagsNode !== undefined &&
    tagsNode !== null &&
    !isMap(tagsNode) &&
    !(isScalar(tagsNode) && tagsNode.value === null)
  ) {
    throw new Error("tags 必须是映射对象");
  }
  const configuredBuiltinTags = document.getIn(["tags", "builtin_pack_enabled"]);
  let builtinTagsEnabled = DEFAULT_DRAFT.builtinTagsEnabled;
  if (configuredBuiltinTags !== undefined && configuredBuiltinTags !== null) {
    if (typeof configuredBuiltinTags !== "boolean") {
      throw new Error("tags.builtin_pack_enabled 必须是布尔值");
    }
    builtinTagsEnabled = configuredBuiltinTags;
  }
  return { nightlyStartTime, builtinTagsEnabled };
}

export function parseConfig(source: string) {
  const document = configDocument(source);
  return { document, draft: draftFromDocument(document) };
}

function requiredRange(node: RangedNode, description: string) {
  if (!node.range) {
    throw new Error(`无法定位 ${description} 在 config.yaml 中的位置`);
  }
  return node.range;
}

function findPair(map: ParsedMap, key: string): ParsedPair | undefined {
  return map.items.find(
    (pair) => isScalar(pair.key) && pair.key.value === key
  );
}

function yamlString(value: string, template?: ParsedNode | null) {
  if (isScalar(template)) {
    if (template.type === Scalar.QUOTE_DOUBLE) return JSON.stringify(value);
    if (template.type === Scalar.QUOTE_SINGLE) {
      return `'${value.replace(/'/g, "''")}'`;
    }
  }

  const serialized = stringify(value, { lineWidth: 0 });
  return serialized.endsWith("\n") ? serialized.slice(0, -1) : serialized;
}

function lineStart(source: string, offset: number) {
  let cursor = offset;
  while (cursor > 0 && source[cursor - 1] !== "\n" && source[cursor - 1] !== "\r") {
    cursor -= 1;
  }
  return cursor;
}

function lineEndIncludingBreak(source: string, offset: number) {
  let cursor = offset;
  while (cursor < source.length && source[cursor] !== "\n" && source[cursor] !== "\r") {
    cursor += 1;
  }
  if (source[cursor] === "\r" && source[cursor + 1] === "\n") return cursor + 2;
  if (source[cursor] === "\r" || source[cursor] === "\n") return cursor + 1;
  return cursor;
}

function lineEnding(source: string) {
  const match = /\r\n|\n|\r/.exec(source);
  return match?.[0] ?? "\n";
}

function insertLinesAtBoundary(
  source: string,
  position: number,
  lines: readonly string[]
): SourceEdit {
  const eol = lineEnding(source);
  const previousIsBreak =
    position === 0 || source[position - 1] === "\n" || source[position - 1] === "\r";
  const nextIsBreak = source[position] === "\n" || source[position] === "\r";
  const prefix = previousIsBreak ? "" : eol;
  const suffix =
    (position < source.length && !nextIsBreak) ||
    (position === source.length && position > 0 && previousIsBreak)
      ? eol
      : "";

  return {
    start: position,
    end: position,
    text: `${prefix}${lines.join(eol)}${suffix}`,
  };
}

function replacePairValue(
  source: string,
  pair: ParsedPair,
  value: string
): SourceEdit {
  const node = pair.value;
  if (node && !isScalar(node)) {
    throw new Error("nightly.start_time 必须是字符串值");
  }

  if (node?.range && node.range[0] < node.range[1]) {
    return {
      start: node.range[0],
      end: node.range[1],
      text: yamlString(value, node),
    };
  }

  if (!isScalar(pair.key)) {
    throw new Error("无法定位 nightly.start_time 的键名");
  }
  const keyRange = requiredRange(pair.key, "nightly.start_time");
  const endOfLine = lineEndIncludingBreak(source, keyRange[1]);
  const colon = source.indexOf(":", keyRange[1]);
  if (colon === -1 || colon >= endOfLine) {
    throw new Error("无法定位 nightly.start_time 的值");
  }

  let whitespaceEnd = colon + 1;
  while (source[whitespaceEnd] === " " || source[whitespaceEnd] === "\t") {
    whitespaceEnd += 1;
  }
  const commentGap = source[whitespaceEnd] === "#" ? " " : "";
  return {
    start: colon + 1,
    end: whitespaceEnd,
    text: ` ${yamlString(value, node)}${commentGap}`,
  };
}

function replacePairKey(pair: ParsedPair, key: string): SourceEdit {
  if (!isScalar(pair.key)) {
    throw new Error("无法定位 nightly.cron_hour 的键名");
  }
  const range = requiredRange(pair.key, "nightly.cron_hour");
  return {
    start: range[0],
    end: range[1],
    text: yamlString(key, pair.key),
  };
}

function isFlowMap(map: ParsedMap) {
  return map.srcToken?.type === "flow-collection";
}

function pairContentEnd(pair: ParsedPair) {
  const node = pair.value ?? pair.key;
  return requiredRange(node, "YAML 配置项")[1];
}

function removeMapPair(source: string, map: ParsedMap, pair: ParsedPair): SourceEdit {
  const index = map.items.indexOf(pair);
  if (index === -1) throw new Error("无法定位要移除的 YAML 配置项");

  if (isFlowMap(map)) {
    const currentStart = requiredRange(pair.key, "YAML 配置项")[0];
    const currentEnd = pairContentEnd(pair);
    if (index > 0) {
      return {
        start: pairContentEnd(map.items[index - 1]),
        end: currentEnd,
        text: "",
      };
    }
    if (map.items[index + 1]) {
      return {
        start: currentStart,
        end: requiredRange(map.items[index + 1].key, "YAML 配置项")[0],
        text: "",
      };
    }
    return { start: currentStart, end: currentEnd, text: "" };
  }

  const keyRange = requiredRange(pair.key, "YAML 配置项");
  return {
    start: lineStart(source, keyRange[0]),
    end: lineEndIncludingBreak(source, pairContentEnd(pair)),
    text: "",
  };
}

function insertFlowMapEntry(source: string, map: ParsedMap, entry: string): SourceEdit {
  const range = requiredRange(map, "YAML 映射");
  const openBrace = source.indexOf("{", range[0]);
  const closeBrace = source.lastIndexOf("}", range[1] - 1);
  if (openBrace === -1 || closeBrace <= openBrace) {
    throw new Error("无法定位 YAML 行内映射的边界");
  }

  if (map.items.length === 0) {
    const existingWhitespace = source.slice(openBrace + 1, closeBrace);
    return {
      start: openBrace + 1,
      end: closeBrace,
      text: existingWhitespace.length > 0 ? ` ${entry} ` : entry,
    };
  }

  const lastPair = map.items[map.items.length - 1];
  const position = pairContentEnd(lastPair);
  return { start: position, end: position, text: `, ${entry}` };
}

function insertBlockMapEntry(
  source: string,
  map: ParsedMap,
  entry: string
): SourceEdit {
  const firstPair = map.items[0];
  if (!firstPair || !isScalar(firstPair.key)) {
    throw new Error("无法确定 nightly 配置项的缩进");
  }
  const keyRange = requiredRange(firstPair.key, "nightly 配置项");
  const indent = source.slice(lineStart(source, keyRange[0]), keyRange[0]);
  const mapRange = requiredRange(map, "nightly");
  return insertLinesAtBoundary(source, mapRange[2], [`${indent}${entry}`]);
}

function insertAfterEmptyMapKey(
  source: string,
  pair: ParsedPair,
  entry: string
): SourceEdit {
  const keyRange = requiredRange(pair.key, "nightly");
  const parentIndent = source.slice(lineStart(source, keyRange[0]), keyRange[0]);
  const lineEnd = lineEndIncludingBreak(source, keyRange[1]);
  return insertLinesAtBoundary(source, lineEnd, [`${parentIndent}  ${entry}`]);
}

function addNightlySection(
  source: string,
  document: ReturnType<typeof configDocument>,
  root: ParsedMap | null,
  value: string
): SourceEdit {
  const renderedValue = yamlString(value);
  if (root && isFlowMap(root)) {
    return insertFlowMapEntry(
      source,
      root,
      `nightly: { start_time: ${renderedValue} }`
    );
  }

  const position = root?.range?.[2] ?? document.range?.[1] ?? source.length;
  return insertLinesAtBoundary(source, position, [
    "nightly:",
    `  start_time: ${renderedValue}`,
  ]);
}

function nightlyStartTimeEdits(
  source: string,
  document: ReturnType<typeof configDocument>,
  value: string
): SourceEdit[] {
  const root = isMap(document.contents) ? (document.contents as ParsedMap) : null;
  const nightlyPair = root ? findPair(root, "nightly") : undefined;
  if (!nightlyPair) return [addNightlySection(source, document, root, value)];

  const nightlyNode = nightlyPair.value;
  if (
    !nightlyNode ||
    (isScalar(nightlyNode) &&
      nightlyNode.value === null &&
      (!nightlyNode.range || nightlyNode.range[0] === nightlyNode.range[1]))
  ) {
    return [
      insertAfterEmptyMapKey(
        source,
        nightlyPair,
        `start_time: ${yamlString(value)}`
      ),
    ];
  }
  if (isScalar(nightlyNode) && nightlyNode.value === null) {
    const range = requiredRange(nightlyNode, "nightly");
    return [
      {
        start: range[0],
        end: range[1],
        text: `{ start_time: ${yamlString(value)} }`,
      },
    ];
  }
  if (!isMap(nightlyNode)) {
    throw new Error("nightly 必须是映射对象");
  }

  const nightly = nightlyNode as ParsedMap;
  const startTimePair = findPair(nightly, "start_time");
  const legacyHourPair = findPair(nightly, "cron_hour");
  if (startTimePair) {
    const edits = [replacePairValue(source, startTimePair, value)];
    if (legacyHourPair) edits.push(removeMapPair(source, nightly, legacyHourPair));
    return edits;
  }
  if (legacyHourPair) {
    return [
      replacePairKey(legacyHourPair, "start_time"),
      replacePairValue(source, legacyHourPair, value),
    ];
  }

  const entry = `start_time: ${yamlString(value)}`;
  return [
    isFlowMap(nightly)
      ? insertFlowMapEntry(source, nightly, entry)
      : insertBlockMapEntry(source, nightly, entry),
  ];
}

function replaceBooleanPairValue(
  source: string,
  pair: ParsedPair,
  value: boolean
): SourceEdit {
  const node = pair.value;
  if (node && !isScalar(node)) {
    throw new Error("tags.builtin_pack_enabled 必须是布尔值");
  }
  const rendered = value ? "true" : "false";

  if (node?.range && node.range[0] < node.range[1]) {
    return {
      start: node.range[0],
      end: node.range[1],
      text: rendered,
    };
  }

  if (!isScalar(pair.key)) {
    throw new Error("无法定位 tags.builtin_pack_enabled 的键名");
  }
  const keyRange = requiredRange(pair.key, "tags.builtin_pack_enabled");
  const endOfLine = lineEndIncludingBreak(source, keyRange[1]);
  const colon = source.indexOf(":", keyRange[1]);
  if (colon === -1 || colon >= endOfLine) {
    throw new Error("无法定位 tags.builtin_pack_enabled 的值");
  }

  let whitespaceEnd = colon + 1;
  while (source[whitespaceEnd] === " " || source[whitespaceEnd] === "\t") {
    whitespaceEnd += 1;
  }
  const commentGap = source[whitespaceEnd] === "#" ? " " : "";
  return {
    start: colon + 1,
    end: whitespaceEnd,
    text: ` ${rendered}${commentGap}`,
  };
}

function addTagsSection(
  source: string,
  document: ReturnType<typeof configDocument>,
  root: ParsedMap | null,
  value: boolean
): SourceEdit {
  const rendered = value ? "true" : "false";
  if (root && isFlowMap(root)) {
    return insertFlowMapEntry(
      source,
      root,
      `tags: { builtin_pack_enabled: ${rendered} }`
    );
  }

  const position = root?.range?.[2] ?? document.range?.[1] ?? source.length;
  return insertLinesAtBoundary(source, position, [
    "tags:",
    `  builtin_pack_enabled: ${rendered}`,
  ]);
}

function builtinTagsEnabledEdits(
  source: string,
  document: ReturnType<typeof configDocument>,
  value: boolean
): SourceEdit[] {
  const root = isMap(document.contents) ? (document.contents as ParsedMap) : null;
  const tagsPair = root ? findPair(root, "tags") : undefined;
  if (!tagsPair) return [addTagsSection(source, document, root, value)];

  const rendered = value ? "true" : "false";
  const tagsNode = tagsPair.value;
  if (
    !tagsNode ||
    (isScalar(tagsNode) &&
      tagsNode.value === null &&
      (!tagsNode.range || tagsNode.range[0] === tagsNode.range[1]))
  ) {
    return [
      insertAfterEmptyMapKey(
        source,
        tagsPair,
        `builtin_pack_enabled: ${rendered}`
      ),
    ];
  }
  if (isScalar(tagsNode) && tagsNode.value === null) {
    const range = requiredRange(tagsNode, "tags");
    return [
      {
        start: range[0],
        end: range[1],
        text: `{ builtin_pack_enabled: ${rendered} }`,
      },
    ];
  }
  if (!isMap(tagsNode)) {
    throw new Error("tags 必须是映射对象");
  }

  const tags = tagsNode as ParsedMap;
  const builtinPair = findPair(tags, "builtin_pack_enabled");
  if (builtinPair) {
    return [replaceBooleanPairValue(source, builtinPair, value)];
  }

  const entry = `builtin_pack_enabled: ${rendered}`;
  return [
    isFlowMap(tags)
      ? insertFlowMapEntry(source, tags, entry)
      : insertBlockMapEntry(source, tags, entry),
  ];
}

function applySourceEdits(source: string, edits: readonly SourceEdit[]) {
  const ordered = [...edits].sort((left, right) => right.start - left.start);
  let boundary = source.length;
  let result = source;

  for (const edit of ordered) {
    if (
      edit.start < 0 ||
      edit.end < edit.start ||
      edit.end > source.length ||
      edit.end > boundary
    ) {
      throw new Error("YAML 局部修改范围无效或相互重叠");
    }
    result = `${result.slice(0, edit.start)}${edit.text}${result.slice(edit.end)}`;
    boundary = edit.start;
  }
  return result;
}

export function applyVisualFields(
  source: string,
  draft: SettingsDraft,
  fields: ReadonlySet<VisualField>
) {
  let updated = source;
  if (fields.has("nightlyStartTime")) {
    const document = configDocument(updated);
    updated = applySourceEdits(
      updated,
      nightlyStartTimeEdits(updated, document, draft.nightlyStartTime)
    );
  }
  if (fields.has("builtinTagsEnabled")) {
    const document = configDocument(updated);
    updated = applySourceEdits(
      updated,
      builtinTagsEnabledEdits(updated, document, draft.builtinTagsEnabled)
    );
  }

  // Source ranges perform the write, while a fresh parse verifies that the
  // localized edit still produced one valid YAML document.
  configDocument(updated);
  return updated;
}

export function changedVisualFields(saved: SettingsDraft, draft: SettingsDraft) {
  const fields = new Set<VisualField>();
  if (saved.nightlyStartTime !== draft.nightlyStartTime) {
    fields.add("nightlyStartTime");
  }
  if (saved.builtinTagsEnabled !== draft.builtinTagsEnabled) {
    fields.add("builtinTagsEnabled");
  }
  return fields;
}
