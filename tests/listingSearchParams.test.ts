import assert from "node:assert/strict";
import test from "node:test";

import {
  readListingPage,
  readListingSort,
  readListingView,
  withListingNavigation,
  withListingPage,
  withListingSort,
  withListingView,
} from "../src/lib/listingSearchParams.ts";

test("listing sort is restored from the URL after returning from a video", () => {
  assert.equal(readListingSort(new URLSearchParams("sort=latest")), "latest");
  assert.equal(readListingSort(new URLSearchParams("sort=recent")), "recent");
  assert.equal(readListingSort(new URLSearchParams("sort=hot")), "hot");
  assert.equal(readListingSort(new URLSearchParams("sort=unknown")), "hot");
  assert.equal(readListingSort(new URLSearchParams()), "hot");
});

test("listing page and view URL state use stable defaults", () => {
  assert.equal(readListingPage(new URLSearchParams("page=3")), 3);
  assert.equal(readListingPage(new URLSearchParams("page=0")), 1);
  assert.equal(readListingPage(new URLSearchParams("page=3.5")), 1);
  assert.equal(readListingPage(new URLSearchParams("page=invalid")), 1);
  assert.equal(readListingView(new URLSearchParams("view=compact")), "compact");
  assert.equal(readListingView(new URLSearchParams("view=unknown")), "grid");

  const original = new URLSearchParams("q=舞蹈&tag=推荐");
  const deepPage = withListingPage(original, 4);
  assert.equal(deepPage.get("page"), "4");
  assert.equal(withListingPage(deepPage, 1).get("page"), null);
  assert.equal(withListingView(original, "compact").get("view"), "compact");
  assert.equal(withListingView(new URLSearchParams("view=compact"), "grid").get("view"), null);
});

test("listing navigation applies page sort and view atomically", () => {
  const original = new URLSearchParams("q=舞蹈&page=8&view=compact");
  const next = withListingNavigation(original, {
    page: 1,
    sort: "latest",
    view: "grid",
  });

  assert.equal(next.get("q"), "舞蹈");
  assert.equal(next.get("page"), null);
  assert.equal(next.get("sort"), "latest");
  assert.equal(next.get("view"), null);
  assert.equal(original.get("page"), "8");
});

test("listing sort URL updates preserve filters and omit the default", () => {
  const original = new URLSearchParams("q=舞蹈&tag=推荐");

  const latest = withListingSort(original, "latest");
  assert.equal(latest.get("q"), "舞蹈");
  assert.equal(latest.get("tag"), "推荐");
  assert.equal(latest.get("sort"), "latest");
  assert.equal(original.get("sort"), null, "the current location must not be mutated");

  const hot = withListingSort(latest, "hot");
  assert.equal(hot.get("q"), "舞蹈");
  assert.equal(hot.get("tag"), "推荐");
  assert.equal(hot.get("sort"), null);
});
