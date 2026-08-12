import assert from "node:assert/strict";
import test from "node:test";

import {
  changedCredentialValues,
  newDriveCredentialError,
} from "../src/admin/drive/credentials.ts";

test("pikpak accepts a refresh token without account credentials", () => {
  assert.equal(
    newDriveCredentialError("pikpak", { refresh_token: "refresh-token" }),
    ""
  );
});

test("pikpak accepts a complete username and password pair", () => {
  assert.equal(
    newDriveCredentialError("pikpak", {
      username: "user@example.com",
      password: "secret",
    }),
    ""
  );
});

test("pikpak rejects missing or partial account credentials", () => {
  const want = "请填写 PikPak 邮箱和密码，或使用方式二的 refresh_token";
  assert.equal(newDriveCredentialError("pikpak", {}), want);
  assert.equal(
    newDriveCredentialError("pikpak", { username: "user@example.com" }),
    want
  );
  assert.equal(newDriveCredentialError("pikpak", { password: "secret" }), want);
});

test("p123 alternative credential validation remains unchanged", () => {
  assert.equal(newDriveCredentialError("p123", { access_token: "token" }), "");
  assert.equal(
    newDriveCredentialError("p123", {
      username: "user@example.com",
      password: "secret",
    }),
    ""
  );
  assert.equal(
    newDriveCredentialError("p123", {}),
    "请使用方式一扫码登录，或填写方式二的手机号/邮箱和密码"
  );
});

test("credential edit payload contains only changed editable values", () => {
  const initial = {
    username: "old-user",
    password: "old-password",
    refresh_token: "old-refresh",
    access_token: "runtime-access",
    captcha_token: "runtime-captcha",
    device_id: "runtime-device",
  };
  const current = {
    ...initial,
    username: "new-user",
    access_token: "newer-runtime-access",
  };

  assert.deepEqual(
    changedCredentialValues(current, initial, [
      "refresh_token",
      "username",
      "password",
    ]),
    { username: "new-user" }
  );
});

test("unchanged pikpak credentials produce an empty edit patch", () => {
  const credentials = {
    refresh_token: "refresh",
    access_token: "access",
    captcha_token: "captcha",
    device_id: "device",
  };
  assert.deepEqual(
    changedCredentialValues(credentials, credentials, [
      "refresh_token",
      "username",
      "password",
    ]),
    {}
  );
});
