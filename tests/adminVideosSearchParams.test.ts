import assert from "node:assert/strict";
import test from "node:test";

import {
  adminVideosSourceFilter,
  readAdminVideosPage,
  readAdminVideosSourceKey,
  withAdminVideosPage,
  withAdminVideosSourceKey,
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

test("admin video sources use typed URL keys without colliding drive and crawler ids", () => {
  assert.equal(readAdminVideosSourceKey(new URLSearchParams()), "all");
  assert.equal(
    readAdminVideosSourceKey(new URLSearchParams("source=drive%3Ashared")),
    "drive:shared"
  );
  assert.equal(
    readAdminVideosSourceKey(new URLSearchParams("source=crawler%3Ashared")),
    "crawler:shared"
  );
  assert.equal(readAdminVideosSourceKey(new URLSearchParams("source=drive%3A")), "all");
  assert.equal(readAdminVideosSourceKey(new URLSearchParams("source=unknown%3Aid")), "all");

  assert.deepEqual(adminVideosSourceFilter("drive:shared"), {
    driveId: "shared",
    crawlerId: "",
  });
  assert.deepEqual(adminVideosSourceFilter("crawler:shared"), {
    driveId: "",
    crawlerId: "shared",
  });
});

test("admin video source updates preserve the active view and remove the default source", () => {
  const original = new URLSearchParams("tab=blacklist&page=3");
  const filtered = withAdminVideosSourceKey(original, "drive:cloud-a");

  assert.equal(filtered.get("source"), "drive:cloud-a");
  assert.equal(filtered.get("tab"), "blacklist");
  assert.equal(filtered.get("page"), "3");
  assert.equal(original.has("source"), false);

  const allSources = withAdminVideosSourceKey(filtered, "all");
  assert.equal(allSources.has("source"), false);
  assert.equal(allSources.get("tab"), "blacklist");
});
