import assert from "node:assert/strict";
import test from "node:test";
import { buildConfigDiff } from "../src/admin/settings/configDiff";

test("config diff leaves identical files unchanged", () => {
  assert.deepEqual(buildConfigDiff("nightly:\n  enabled: true", "nightly:\n  enabled: true"), {
    hunks: [],
    additions: 0,
    deletions: 0,
  });
});

test("config diff aligns an inserted line without marking following lines as changed", () => {
  const diff = buildConfigDiff(
    "alpha: 1\nbeta: 2\ngamma: 3",
    "alpha: 1\ninserted: true\nbeta: 2\ngamma: 3"
  );

  assert.equal(diff.additions, 1);
  assert.equal(diff.deletions, 0);
  assert.equal(diff.hunks.length, 1);
  assert.deepEqual(
    diff.hunks[0].lines.filter((line) => line.kind === "addition"),
    [{ kind: "addition", oldLine: null, newLine: 2, content: "inserted: true" }]
  );
  assert.ok(
    diff.hunks[0].lines.some(
      (line) =>
        line.kind === "context" &&
        line.oldLine === 2 &&
        line.newLine === 3 &&
        line.content === "beta: 2"
    )
  );
});

test("config diff aligns a deleted line and retains real old and new line numbers", () => {
  const diff = buildConfigDiff(
    "one\ntwo\nremove me\nthree\nfour",
    "one\ntwo\nthree\nfour"
  );

  assert.equal(diff.additions, 0);
  assert.equal(diff.deletions, 1);
  assert.ok(
    diff.hunks[0].lines.some(
      (line) =>
        line.kind === "deletion" &&
        line.oldLine === 3 &&
        line.newLine === null &&
        line.content === "remove me"
    )
  );
  assert.ok(
    diff.hunks[0].lines.some(
      (line) =>
        line.kind === "context" &&
        line.oldLine === 4 &&
        line.newLine === 3 &&
        line.content === "three"
    )
  );
});

test("config diff limits an isolated replacement to three context lines per side", () => {
  const beforeLines = Array.from({ length: 15 }, (_, index) => `line-${index + 1}`);
  const afterLines = [...beforeLines];
  afterLines[9] = "line-10-updated";

  const diff = buildConfigDiff(beforeLines.join("\n"), afterLines.join("\n"));
  const [hunk] = diff.hunks;

  assert.equal(diff.additions, 1);
  assert.equal(diff.deletions, 1);
  assert.equal(hunk.lines.filter((line) => line.kind === "context").length, 6);
  assert.equal(hunk.oldStart, 7);
  assert.equal(hunk.newStart, 7);
  assert.equal(hunk.oldCount, 7);
  assert.equal(hunk.newCount, 7);
  assert.equal(hunk.lines.some((line) => line.content === "line-1"), false);
  assert.equal(hunk.lines.some((line) => line.content === "line-15"), false);
});
