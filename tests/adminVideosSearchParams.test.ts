import assert from "node:assert/strict";
import test from "node:test";

import {
  readAdminVideosPage,
  withAdminVideosPage,
} from "../src/admin/videosSearchParams.ts";

test("admin video page is restored from a valid URL parameter", () => {
  assert.equal(readAdminVideosPage(new URLSearchParams("page=7")), 7);
  assert.equal(readAdminVideosPage(new URLSearchParams("tab=blacklist&page=2")), 2);
});

test("admin video page falls back to the first page for invalid URL values", () => {
  for (const value of ["", "0", "-1", "1.5", "01", "abc", "9007199254740992"]) {
    assert.equal(readAdminVideosPage(new URLSearchParams({ page: value })), 1);
  }
  assert.equal(readAdminVideosPage(new URLSearchParams()), 1);
});

test("admin video page URL updates preserve the active tab and omit page one", () => {
  const original = new URLSearchParams("tab=blacklist");

  const paged = withAdminVideosPage(original, 4);
  assert.equal(paged.get("tab"), "blacklist");
  assert.equal(paged.get("page"), "4");
  assert.equal(original.get("page"), null, "the current location must not be mutated");

  const firstPage = withAdminVideosPage(paged, 1);
  assert.equal(firstPage.get("tab"), "blacklist");
  assert.equal(firstPage.get("page"), null);
});
