import assert from "node:assert/strict";
import test from "node:test";
import { applyVisualFields, changedVisualFields, configDocument, parseConfig } from "../src/admin/settings/configYaml";

test("Telegram settings and credentials round-trip through the shared YAML draft", () => {
  for (const source of ["# keep\npreview: { enabled: true }\n", "{}\n", "telegram:\n", "telegram: null\n", "telegram: {}\n"]) {
    const before = parseConfig(source).draft;
    const draft = { ...before, telegramEnabled: true, telegramBotToken: "123:test_token", telegramAllowedUserIds: "123, 456",
      telegramUploadDriveId: "cloud", telegramUploadDirectory: "Telegram/videos", telegramUploadProxy: "socks5h://user:pass@proxy:1080", telegramMaxPendingJobs: 25 };
    const output = applyVisualFields(source, draft, changedVisualFields(before, draft));
    assert.deepEqual(parseConfig(output).draft, draft);
    assert.equal(configDocument(output).getIn(["telegram", "bot_token"]), draft.telegramBotToken);
    assert.equal(configDocument(output).hasIn(["telegram", "api_base_url"]), false);
    assert.equal(configDocument(output).hasIn(["telegram", "api_id"]), false);
    assert.equal(configDocument(output).hasIn(["telegram", "api_hash"]), false);
    assert.equal(configDocument(output).hasIn(["telegram", "api_files_root"]), false);
    assert.equal(configDocument(output).hasIn(["telegram", "local_files_root"]), false);
    if (source.startsWith("# keep")) assert.ok(output.startsWith("# keep\npreview: { enabled: true }\n"));
  }
});

test("Telegram edits preserve unrelated comments, keys, and concurrent field changes", () => {
  const source = "# top\ntelegram:\n  bot_token: '123:old' # bot\n  api_hash: '0123456789abcdef0123456789abcdef'\n  max_pending_jobs: 10 # jobs\n  allowed_user_ids: [123] # users\n  unknown_key: keep\npreview:\n  width: 480\n";
  const before = parseConfig(source).draft;
  const draft = { ...before, telegramBotToken: "123:new" };
  const latest = source.replace("max_pending_jobs: 10", "max_pending_jobs: 25");
  const output = applyVisualFields(latest, draft, changedVisualFields(before, draft));
  assert.equal(output, latest.replace("'123:old'", "'123:new'"));
});

test("Telegram lists can be edited in block and flow YAML without damaging the next field", () => {
  for (const source of [
    "telegram:\n  allowed_user_ids:\n    - 123\n    - 456\n  site_base_url: https://example.com\n",
    "telegram: { allowed_user_ids: [123], site_base_url: https://example.com }\n",
    "telegram:\n  allowed_user_ids: # empty\n  site_base_url: https://example.com\n",
  ]) {
    const before = parseConfig(source).draft;
    const draft = { ...before, telegramAllowedUserIds: "789, 123" };
    const output = applyVisualFields(source, draft, new Set(["telegramAllowedUserIds"]));
    assert.deepEqual(parseConfig(output).draft, draft);
  }
});

test("clearing the Telegram Bot Token writes empty values instead of retaining old secrets", () => {
  const source = "telegram: { bot_token: '123:old', api_hash: '0123456789abcdef0123456789abcdef' }\n";
  const before = parseConfig(source).draft;
  const draft = { ...before, telegramBotToken: "" };
  assert.deepEqual(parseConfig(applyVisualFields(source, draft, changedVisualFields(before, draft))).draft, draft);
});

test("Telegram YAML rejects mismatched types and invalid user ID lists", () => {
  for (const source of ["telegram: []", "telegram: { enabled: yes }", "telegram: { bot_token: 123 }", "telegram: { allowed_user_ids: [-1] }", "telegram: { upload_proxy: 7890 }"]) {
    assert.throws(() => parseConfig(source), /telegram/);
  }
});

test("upload settings preserve concurrent credentials and untouched upload fields", () => {
  const source = "# config\ntelegram:\n  bot_token: '123:old' # token\n  upload_drive_id: cloud\n  upload_directory: Telegram\npreview: { enabled: true }\n";
  const before = parseConfig(source).draft;
  const draft = { ...before, telegramUploadDriveId: "new-cloud" };
  const latest = source.replace("123:old", "456:new").replace("upload_directory: Telegram", "upload_directory: videos");
  const output = applyVisualFields(latest, draft, changedVisualFields(before, draft));
  assert.equal(output, latest.replace("upload_drive_id: cloud", "upload_drive_id: new-cloud"));
});

test("clearing the upload target keeps its directory, proxy and Telegram connection settings", () => {
  const source = "telegram: { enabled: true, bot_token: '123:token', upload_drive_id: cloud, upload_directory: Telegram/videos, upload_proxy: 'http://proxy:7890' }\n";
  const before = parseConfig(source).draft;
  const draft = { ...before, telegramUploadDriveId: "" };
  const output = applyVisualFields(source, draft, changedVisualFields(before, draft));
  assert.deepEqual(parseConfig(output).draft, draft);
});

test("upload proxy edits and clearing preserve comments and concurrent target changes", () => {
  for (const source of [
    "# config\ntelegram:\n  upload_drive_id: cloud\n  upload_proxy: 'http://old:7890' # proxy\n  unknown_key: keep\n",
    "telegram: { upload_drive_id: cloud, upload_proxy: 'http://old:7890', unknown_key: keep }\n",
  ]) {
    const before = parseConfig(source).draft;
    for (const proxy of ["socks5h://user:p%40ss@proxy:1080", ""]) {
      const draft = { ...before, telegramUploadProxy: proxy };
      const latest = source.replace("upload_drive_id: cloud", "upload_drive_id: new-cloud");
      const output = applyVisualFields(latest, draft, changedVisualFields(before, draft));
      assert.equal(output, latest.replace("'http://old:7890'", `'${proxy}'`));
      assert.equal(parseConfig(output).draft.telegramUploadProxy, proxy);
    }
  }
});
