import { Text } from "@codemirror/state";
import { Chunk } from "@codemirror/merge";

export type ConfigDiffLineKind = "context" | "addition" | "deletion";

export type ConfigDiffLine = {
  kind: ConfigDiffLineKind;
  oldLine: number | null;
  newLine: number | null;
  content: string;
};

export type ConfigDiffHunk = {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: ConfigDiffLine[];
};

export type ConfigDiff = {
  hunks: ConfigDiffHunk[];
  additions: number;
  deletions: number;
};

const CONTEXT_LINE_COUNT = 3;

function clampDocumentPosition(document: Text, position: number) {
  return Math.max(0, Math.min(position, document.length));
}

function changedLines(document: Text, from: number, to: number) {
  if (from >= to) return [];

  const firstLine = document.lineAt(from).number;
  const lastLine = document.lineAt(to - 1).number;
  return Array.from({ length: lastLine - firstLine + 1 }, (_, index) => {
    const lineNumber = firstLine + index;
    return { number: lineNumber, content: document.line(lineNumber).text };
  });
}

function contextBoundary(
  document: Text,
  position: number,
  hasChangedLines: boolean,
  first: number,
  last: number
) {
  if (hasChangedLines) {
    return { before: first - 1, after: last + 1 };
  }

  const safePosition = clampDocumentPosition(document, position);
  const anchor = document.lineAt(safePosition);
  if (safePosition === anchor.from) {
    return { before: anchor.number - 1, after: anchor.number };
  }
  return { before: anchor.number, after: anchor.number + 1 };
}

function buildHunk(oldDocument: Text, newDocument: Text, chunk: Chunk): ConfigDiffHunk {
  const deleted = changedLines(oldDocument, chunk.fromA, chunk.toA);
  const added = changedLines(newDocument, chunk.fromB, chunk.toB);
  const oldBoundary = contextBoundary(
    oldDocument,
    chunk.fromA,
    deleted.length > 0,
    deleted[0]?.number ?? 1,
    deleted[deleted.length - 1]?.number ?? 1
  );
  const newBoundary = contextBoundary(
    newDocument,
    chunk.fromB,
    added.length > 0,
    added[0]?.number ?? 1,
    added[added.length - 1]?.number ?? 1
  );
  const lines: ConfigDiffLine[] = [];

  const contextBeforeCount = Math.min(
    CONTEXT_LINE_COUNT,
    Math.max(0, oldBoundary.before),
    Math.max(0, newBoundary.before)
  );
  for (let offset = contextBeforeCount; offset > 0; offset -= 1) {
    const oldLine = oldBoundary.before - offset + 1;
    const newLine = newBoundary.before - offset + 1;
    if (
      oldLine < 1 ||
      newLine < 1 ||
      oldLine > oldDocument.lines ||
      newLine > newDocument.lines
    ) {
      continue;
    }
    lines.push({
      kind: "context",
      oldLine,
      newLine,
      content: oldDocument.line(oldLine).text,
    });
  }

  deleted.forEach((line) => {
    lines.push({
      kind: "deletion",
      oldLine: line.number,
      newLine: null,
      content: line.content,
    });
  });
  added.forEach((line) => {
    lines.push({ kind: "addition", oldLine: null, newLine: line.number, content: line.content });
  });

  const contextAfterCount = Math.min(
    CONTEXT_LINE_COUNT,
    Math.max(0, oldDocument.lines - oldBoundary.after + 1),
    Math.max(0, newDocument.lines - newBoundary.after + 1)
  );
  for (let offset = 0; offset < contextAfterCount; offset += 1) {
    const oldLine = oldBoundary.after + offset;
    const newLine = newBoundary.after + offset;
    if (
      oldLine < 1 ||
      newLine < 1 ||
      oldLine > oldDocument.lines ||
      newLine > newDocument.lines
    ) {
      continue;
    }
    lines.push({
      kind: "context",
      oldLine,
      newLine,
      content: oldDocument.line(oldLine).text,
    });
  }

  return {
    oldStart: lines.find((line) => line.oldLine !== null)?.oldLine ?? 1,
    oldCount: lines.filter((line) => line.kind !== "addition").length,
    newStart: lines.find((line) => line.newLine !== null)?.newLine ?? 1,
    newCount: lines.filter((line) => line.kind !== "deletion").length,
    lines,
  };
}

export function buildConfigDiff(before: string, after: string): ConfigDiff {
  const oldDocument = Text.of(before.split("\n"));
  const newDocument = Text.of(after.split("\n"));
  const chunks = Chunk.build(oldDocument, newDocument);
  const hunks = chunks.map((chunk) => buildHunk(oldDocument, newDocument, chunk));

  return {
    hunks,
    additions: hunks.reduce(
      (total, hunk) => total + hunk.lines.filter((line) => line.kind === "addition").length,
      0
    ),
    deletions: hunks.reduce(
      (total, hunk) => total + hunk.lines.filter((line) => line.kind === "deletion").length,
      0
    ),
  };
}
