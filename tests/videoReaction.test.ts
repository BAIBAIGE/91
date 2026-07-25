import assert from "node:assert/strict";
import test from "node:test";

import {
  applyVideoReactionTransition,
  createVideoReactionVisitId,
  nextVideoReaction,
  type VideoReaction,
} from "../src/lib/videoReaction.ts";

test("video reaction selection follows the three-state toggle", () => {
  const cases: Array<{
    current: VideoReaction;
    selected: "like" | "dislike";
    want: VideoReaction;
  }> = [
    { current: "none", selected: "like", want: "like" },
    { current: "none", selected: "dislike", want: "dislike" },
    { current: "like", selected: "like", want: "none" },
    { current: "like", selected: "dislike", want: "dislike" },
    { current: "dislike", selected: "like", want: "like" },
    { current: "dislike", selected: "dislike", want: "none" },
  ];

  for (const item of cases) {
    assert.equal(
      nextVideoReaction(item.current, item.selected),
      item.want,
      `${item.current} + ${item.selected}`
    );
  }
});

test("video reaction transitions update both aggregate counters", () => {
  const initial = { likes: 10, dislikes: 2 };
  assert.deepEqual(
    applyVideoReactionTransition(initial, "none", "like"),
    { likes: 11, dislikes: 2 }
  );
  assert.deepEqual(
    applyVideoReactionTransition(initial, "none", "dislike"),
    { likes: 10, dislikes: 3 }
  );
  assert.deepEqual(
    applyVideoReactionTransition(initial, "like", "none"),
    { likes: 9, dislikes: 2 }
  );
  assert.deepEqual(
    applyVideoReactionTransition(initial, "like", "dislike"),
    { likes: 9, dislikes: 3 }
  );
  assert.deepEqual(
    applyVideoReactionTransition(initial, "dislike", "like"),
    { likes: 11, dislikes: 1 }
  );
  assert.deepEqual(
    applyVideoReactionTransition(initial, "dislike", "none"),
    { likes: 10, dislikes: 1 }
  );
});

test("each detail page instance can create a fresh valid visit id", () => {
  const first = createVideoReactionVisitId();
  const second = createVideoReactionVisitId();

  assert.match(first, /^[A-Za-z0-9_-]{16,128}$/);
  assert.match(second, /^[A-Za-z0-9_-]{16,128}$/);
  assert.notEqual(first, second);
});
