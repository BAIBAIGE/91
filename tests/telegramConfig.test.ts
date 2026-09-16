import assert from "node:assert/strict";
import test from "node:test";
import {
  importStageLabel,
  parseTelegramUserIDs,
} from "../src/admin/telegram/config";

test("Telegram user ID parsing rejects usernames and unsafe integers", () => {
  assert.deepEqual(parseTelegramUserIDs("123, 456\n123"), [123, 456]);
  for (const value of ["@name", "-1", "0", "1.5", "9007199254740992"])
    assert.throws(() => parseTelegramUserIDs(value));
});
test("import stages distinguish acquisition, validation and retry", () => {
  assert.equal(
    importStageLabel({ state: "downloading", stage: "telegram_download" }),
    "正在从 TG 获取",
  );
  assert.equal(
    importStageLabel({ state: "downloading", stage: "telegram_download" }),
    "正在从 TG 获取",
  );
  assert.equal(
    importStageLabel({ state: "queued", stage: "retry_wait" }),
    "等待重试",
  );
  assert.equal(
    importStageLabel({
      state: "downloading",
      stage: "telegram_download",
      cancelRequested: true,
    }),
    "正在取消",
  );
});
