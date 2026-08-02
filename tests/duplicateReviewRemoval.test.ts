import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const appSource = readFileSync(new URL("../src/App.tsx", import.meta.url), "utf8");
const layoutSource = readFileSync(
  new URL("../src/admin/AdminLayout.tsx", import.meta.url),
  "utf8"
);
const apiSource = readFileSync(new URL("../src/admin/api.ts", import.meta.url), "utf8");
const adminCss = readFileSync(
  new URL("../src/styles/admin.css", import.meta.url),
  "utf8"
);

test("duplicate review admin module is fully removed", () => {
  assert.doesNotMatch(appSource, /duplicate-reviews|DuplicateReviewsPage/);
  assert.doesNotMatch(layoutSource, /重复复核|duplicate-reviews|GitCompare/);
  assert.doesNotMatch(
    apiSource,
    /DuplicateReviewPair|listDuplicateReviews|resolveDuplicateReview/
  );
  assert.doesNotMatch(adminCss, /admin-duperev/);
});
