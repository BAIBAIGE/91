import assert from "node:assert/strict";
import test from "node:test";
import { resolveVideoGridAnchor } from "../src/lib/videoGridAnchor.ts";

const videos = Array.from({ length: 120 }, (_, index) => ({ id: String(index) }));
const anchor = { videos, index: 80, viewportOffset: -37 };

test("removal above the viewport retains the same card and vertical offset", () => {
  const next = videos.filter((video) => Number(video.id) >= 3);
  assert.deepEqual(resolveVideoGridAnchor(anchor, next), { index: 77, viewportOffset: -37 });
});

test("removing the anchor chooses the next surviving neighbor", () => {
  const next = videos.filter((video) => video.id !== "80" && video.id !== "81");
  const resolved = resolveVideoGridAnchor(anchor, next);
  assert.ok(resolved);
  assert.equal(next[resolved.index].id, "82");
  assert.equal(resolved.viewportOffset, -37);
});

test("deleting the last card chooses its predecessor and deleting all yields no anchor", () => {
  const last = { ...anchor, index: 119 };
  assert.deepEqual(resolveVideoGridAnchor(last, videos.slice(0, -1)), { index: 118, viewportOffset: -37 });
  assert.equal(resolveVideoGridAnchor(last, []), null);
});

test("ordinary returns and pagination do not issue scroll corrections", () => {
  assert.equal(resolveVideoGridAnchor(anchor, videos), null);
  assert.equal(resolveVideoGridAnchor(anchor, [...videos]), null);
  assert.equal(resolveVideoGridAnchor(anchor, [...videos, { id: "120" }]), null);
});
