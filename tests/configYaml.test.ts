import assert from "node:assert/strict";
import test from "node:test";

import {
  applyVisualFields,
  configDocument,
  type VisualField,
} from "../src/admin/settings/configYaml";
import { buildConfigDiff } from "../src/admin/settings/configDiff";

const nightlyStartTime = new Set<VisualField>(["nightlyStartTime"]);

function updateStartTime(source: string, value = "02:00") {
  return applyVisualFields(source, { nightlyStartTime: value }, nightlyStartTime);
}

test("visual config updates only the start_time scalar in the original YAML", () => {
  const source = [
    "scanner:",
    '  video_extensions: [".mp4", ".mkv", ".mov", ".webm", ".avi"]',
    "nightly:",
    "  # keep the schedule comment",
    "  start_time: 01:00 # hot reload",
    "preview:",
    '  ffmpeg_path: "ffmpeg"',
    "",
  ].join("\n");

  const updated = updateStartTime(source);
  assert.equal(
    updated,
    source.replace("start_time: 01:00", "start_time: 02:00")
  );

  const diff = buildConfigDiff(source, updated);
  assert.equal(diff.additions, 1);
  assert.equal(diff.deletions, 1);
  assert.deepEqual(
    diff.hunks.flatMap((hunk) => hunk.lines)
      .filter((line) => line.kind !== "context")
      .map((line) => line.content),
    ["  start_time: 01:00 # hot reload", "  start_time: 02:00 # hot reload"]
  );
});

test("visual config preserves scalar quoting and CRLF line endings", () => {
  assert.equal(
    updateStartTime('nightly:\n  start_time: "01:00" # keep\n'),
    'nightly:\n  start_time: "02:00" # keep\n'
  );
  assert.equal(
    updateStartTime("nightly:\n  start_time: '01:00'\n"),
    "nightly:\n  start_time: '02:00'\n"
  );
  assert.equal(
    updateStartTime("nightly:\r\n  enabled: true\r\ntail: ok\r\n"),
    "nightly:\r\n  enabled: true\r\n  start_time: 02:00\r\ntail: ok\r\n"
  );
});

test("visual config migrates cron_hour in place without reserializing neighbors", () => {
  assert.equal(
    updateStartTime(
      "nightly:\n  # legacy schedule\n  cron_hour: 1 # keep\n  enabled: true\n"
    ),
    "nightly:\n  # legacy schedule\n  start_time: 02:00 # keep\n  enabled: true\n"
  );

  assert.equal(
    updateStartTime(
      "nightly:\n  start_time: 01:00\n  cron_hour: 1\n  enabled: true\n"
    ),
    "nightly:\n  start_time: 02:00\n  enabled: true\n"
  );
});

test("visual config inserts missing fields within block and flow mappings", () => {
  assert.equal(
    updateStartTime("nightly:\n  enabled: true\npreview: true\n"),
    "nightly:\n  enabled: true\n  start_time: 02:00\npreview: true\n"
  );
  assert.equal(
    updateStartTime("nightly: { enabled: true }\ntail: ok\n"),
    "nightly: { enabled: true, start_time: 02:00 }\ntail: ok\n"
  );
  assert.equal(
    updateStartTime("head: ok\n"),
    "head: ok\nnightly:\n  start_time: 02:00\n"
  );
});

test("visual config keeps YAML valid for an empty time input without touching other fields", () => {
  const source = 'video_extensions: [".mp4", ".mkv"]\nnightly:\n  start_time: 01:00\n';
  const updated = updateStartTime(source, "");

  assert.equal(
    updated,
    'video_extensions: [".mp4", ".mkv"]\nnightly:\n  start_time: ""\n'
  );
  assert.doesNotThrow(() => configDocument(updated));
});

test("visual config returns the exact source when no visual field is dirty", () => {
  const source = 'video_extensions: [".mp4", ".mkv"]\n';
  assert.equal(
    applyVisualFields(source, { nightlyStartTime: "02:00" }, new Set()),
    source
  );
});
