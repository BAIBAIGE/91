import assert from "node:assert/strict";
import test from "node:test";
import { ShortsSlideVisibility } from "../src/shorts/slideVisibility";

function slide(index: number) {
  return {
    dataset: { index: String(index) },
    isConnected: true,
  };
}

function entry(target: ReturnType<typeof slide>, intersectionRatio: number) {
  return { target: target as unknown as Element, intersectionRatio };
}

for (const [from, to] of [[0, 1], [2, 1]]) {
  test(`a long drag from ${from} to ${to} activates on release without another observation`, () => {
    const visibility = new ShortsSlideVisibility();
    const current = slide(from);
    const next = slide(to);
    assert.equal(visibility.update([entry(current, 1)], false), from);

    assert.equal(
      visibility.update([entry(current, 0.35), entry(next, 0.65)], true),
      -1,
      "crossing the playback threshold while dragging must not switch videos"
    );
    assert.equal(visibility.update([entry(next, 0.92)], true), -1);

    // 0.92 -> 1 没有再跨过 [0.6, 0.85]，IO 不会补发通知。
    assert.equal(visibility.update([], false), to);
    assert.equal(visibility.update([], false), -1, "consume the observations once");
  });
}

test("reversing a drag replaces the earlier visible candidate", () => {
  const visibility = new ShortsSlideVisibility();
  const current = slide(0);
  const next = slide(1);
  visibility.update([entry(current, 0.08), entry(next, 0.92)], true);
  visibility.update([entry(current, 0.92), entry(next, 0.08)], true);
  assert.equal(visibility.update([], false), 0);
});

test("queued observations on release supersede the last delivered drag observations", () => {
  const visibility = new ShortsSlideVisibility();
  const current = slide(0);
  const next = slide(1);
  visibility.update([entry(current, 0.08), entry(next, 0.92)], true);
  assert.equal(
    visibility.update([entry(current, 0.92), entry(next, 0.08)], false),
    0
  );
});

test("a short drag waits for the settling animation to cross the playback threshold", () => {
  const visibility = new ShortsSlideVisibility();
  const current = slide(0);
  const next = slide(1);
  visibility.update([entry(current, 0.55), entry(next, 0.45)], true);
  assert.equal(visibility.update([], false), -1);
  assert.equal(visibility.update([entry(next, 0.7)], false), 1);
});

test("queue trimming discards removed slides and uses the retained slide's current index", () => {
  const visibility = new ShortsSlideVisibility();
  const removed = slide(10);
  const retained = slide(11);
  visibility.update([entry(removed, 1), entry(retained, 0.9)], true);
  removed.isConnected = false;
  retained.dataset.index = "1";
  assert.equal(visibility.update([], false), 1);
});

test("resetting observations prevents stale activation after layout changes or teardown", () => {
  const visibility = new ShortsSlideVisibility();
  visibility.update([entry(slide(1), 0.9)], true);
  visibility.clear();
  assert.equal(visibility.update([], false), -1);
});
