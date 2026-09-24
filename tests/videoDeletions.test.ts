import assert from "node:assert/strict";
import test from "node:test";
import {
  deleteVideo,
  fetchVideoDetail,
  fetchVideoRecommendations,
  prefetchVideoDetail,
  consumePrefetchedVideoDetail,
} from "../src/data/videos.ts";
import {
  filterDeletedCollection,
  filterDeletedVideos,
  getDeletedVideoIDs,
  isVideoDeleted,
  markVideoDeleted,
  subscribeVideoDeletions,
} from "../src/lib/videoDeletions.ts";
import { emptyInfiniteListingState, infiniteListingReducer, nextListingRequest } from "../src/lib/infiniteListing.ts";
import type { VideoCollection, VideoItem } from "../src/types.ts";

test("a confirmed deletion is published once with a stable immutable snapshot", () => {
  const before = getDeletedVideoIDs();
  const notifications: string[] = [];
  const unsubscribe = subscribeVideoDeletions((id) => notifications.push(id));
  markVideoDeleted("confirmed");
  const after = getDeletedVideoIDs();
  markVideoDeleted("confirmed");
  unsubscribe();
  assert.equal(before.has("confirmed"), false);
  assert.equal(after.has("confirmed"), true);
  assert.equal(after, getDeletedVideoIDs());
  assert.deepEqual(notifications, ["confirmed"]);
  const items = [{ id: "kept" }];
  assert.equal(filterDeletedVideos(items), items);
  assert.deepEqual(filterDeletedVideos([{ id: "confirmed" }, ...items]), items);
});

test("deleting a card preserves snapshot totals, exhaustion and the next server cursor", () => {
  const state = infiniteListingReducer(emptyInfiniteListingState("list", 20), {
    type: "hydrate", key: "list", requestID: 1, pageSize: 20,
    items: [{ id: "confirmed" }, { id: "kept" }] as VideoItem[],
    total: 100, feedToken: "snapshot", requestedCount: 80, exhausted: false, receivedAt: 1,
  });
  const updated = { ...state, items: filterDeletedVideos(state.items, new Set(["confirmed"])) };
  assert.equal(updated.items.length, 1);
  assert.equal(updated.total, 100);
  assert.equal(updated.exhausted, false);
  assert.deepEqual(nextListingRequest(updated), { cursor: { feedToken: "snapshot", position: 80 }, size: 20 });
});

test("collection removal updates both the count and the current video's index", () => {
  const collection = {
    name: "collection", total: 3, currentIndex: 2,
    items: [{ id: "a" }, { id: "b" }, { id: "c" }],
  } as VideoCollection;
  const next = filterDeletedCollection(collection, new Set(["a"]));
  assert.deepEqual(next.items.map((item) => item.id), ["b", "c"]);
  assert.equal(next.total, 2);
  assert.equal(next.currentIndex, 1);
  assert.equal(collection.total, 3);
  assert.equal(filterDeletedCollection(collection, new Set(["x"])), collection);
});

test("failed deletion leaves cards and prefetches intact; success clears prefetched detail", async (t) => {
  const id = "delete-api-outcome";
  let succeeds = false;
  t.mock.method(globalThis, "fetch", async (_url: unknown, init?: RequestInit) => {
    if (init?.method === "DELETE") {
      return new Response(JSON.stringify({ ok: succeeds, deletedSource: false }), { status: succeeds ? 200 : 500 });
    }
    return new Response(JSON.stringify({ id }));
  });
  await prefetchVideoDetail(id);
  await assert.rejects(deleteVideo(id));
  assert.equal(isVideoDeleted(id), false);
  assert.equal((await consumePrefetchedVideoDetail(id))?.id, id);
  await prefetchVideoDetail(id);
  succeeds = true;
  await deleteVideo(id);
  assert.equal(isVideoDeleted(id), true);
  assert.equal(await consumePrefetchedVideoDetail(id), null);
  assert.equal(await fetchVideoDetail(id), null);
});

test("a success HTTP status with ok=false does not remove the video", async (t) => {
  t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({ ok: false })));
  await assert.rejects(deleteVideo("not-confirmed"));
  assert.equal(isVideoDeleted("not-confirmed"), false);
});

test("detail and recommendation responses that arrive after deletion cannot revive the video", async (t) => {
  const id = "late-deleted-video";
  const pending: Array<(response: Response) => void> = [];
  t.mock.method(globalThis, "fetch", () => new Promise<Response>((resolve) => pending.push(resolve)));
  const detail = fetchVideoDetail(id);
  const recommendations = fetchVideoRecommendations("other-video");
  markVideoDeleted(id);
  pending[0](new Response(JSON.stringify({ id })));
  pending[1](new Response(JSON.stringify([{ id }, { id: "survivor" }])));
  assert.equal(await detail, null);
  assert.deepEqual(await recommendations, [{ id: "survivor" }]);
});
