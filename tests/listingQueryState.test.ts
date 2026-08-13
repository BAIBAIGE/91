import assert from "node:assert/strict";
import test from "node:test";
import {
  deriveListingQueryDisplay,
  listingQueryDisplayPhase,
  listingQueryKey,
  listingQueryReducer,
  type ListingQuery,
  type ListingQueryState,
  type ListingSnapshot,
} from "../src/lib/useListingQuery.ts";
import type { VideoItem } from "../src/types.ts";

function query(page: number): ListingQuery {
  return { q: "测试", tag: "", sort: "latest", page, pageSize: 20 };
}

function snapshot(page: number, receivedAt = page): ListingSnapshot {
  const value = query(page);
  return {
    key: listingQueryKey(value),
    query: value,
    items: [{ id: `video-${page}` } as VideoItem],
    total: 60,
    receivedAt,
  };
}

test("listing query retains one committed snapshot while a new page is pending", () => {
  const committed = snapshot(2);
  const state: ListingQueryState = {
    phase: "ready",
    requestID: 1,
    requestedQuery: committed.query,
    snapshot: committed,
    error: null,
  };

  const pending = listingQueryReducer(state, {
    type: "start",
    requestID: 2,
    query: query(3),
    cached: null,
    fresh: false,
  });

  assert.equal(pending.phase, "refreshing");
  assert.equal(pending.requestedQuery?.page, 3);
  assert.strictEqual(pending.snapshot, committed);
  assert.equal(pending.snapshot?.query.page, 2);
});

test("listing query failure preserves the committed page and success replaces it atomically", () => {
  const committed = snapshot(2);
  const pending: ListingQueryState = {
    phase: "refreshing",
    requestID: 2,
    requestedQuery: query(3),
    snapshot: committed,
    error: null,
  };
  const failure = listingQueryReducer(pending, {
    type: "failure",
    requestID: 2,
    error: new Error("offline"),
  });

  assert.equal(failure.phase, "error");
  assert.strictEqual(failure.snapshot, committed);
  assert.equal(failure.snapshot?.query.page, 2);
  assert.equal(failure.requestedQuery?.page, 3);

  const next = snapshot(3);
  const success = listingQueryReducer(pending, {
    type: "success",
    requestID: 2,
    snapshot: next,
  });
  assert.equal(success.phase, "ready");
  assert.strictEqual(success.snapshot, next);
  assert.equal(success.snapshot?.query.page, 3);
});

test("late listing responses cannot overwrite the active request", () => {
  const current = snapshot(3);
  const state: ListingQueryState = {
    phase: "refreshing",
    requestID: 4,
    requestedQuery: query(4),
    snapshot: current,
    error: null,
  };

  const afterLateSuccess = listingQueryReducer(state, {
    type: "success",
    requestID: 3,
    snapshot: snapshot(2),
  });
  const afterLateFailure = listingQueryReducer(state, {
    type: "failure",
    requestID: 3,
    error: new Error("late"),
  });

  assert.strictEqual(afterLateSuccess, state);
  assert.strictEqual(afterLateFailure, state);
});

test("a URL query transition blocks only while the requested snapshot is unavailable", () => {
  const committed = snapshot(2);
  const ready: ListingQueryState = {
    phase: "ready",
    requestID: 1,
    requestedQuery: committed.query,
    snapshot: committed,
    error: null,
  };

  const display = deriveListingQueryDisplay(
    ready,
    listingQueryKey(query(3)),
    true,
    null,
    10_000
  );
  assert.equal(display.phase, "refreshing");
  assert.strictEqual(display.snapshot, committed);
  assert.equal(display.transitioning, true);
  assert.equal(display.revalidating, false);
  assert.equal(
    listingQueryDisplayPhase(
      { ...ready, phase: "idle", requestedQuery: null, snapshot: null },
      listingQueryKey(query(1)),
      true
    ),
    "initial-loading"
  );
  assert.equal(
    listingQueryDisplayPhase(ready, listingQueryKey(query(2)), false),
    "idle"
  );
});

test("a stale snapshot for the requested query revalidates without blocking it", () => {
  const committed = snapshot(2, 1_000);
  const state: ListingQueryState = {
    phase: "refreshing",
    requestID: 2,
    requestedQuery: committed.query,
    snapshot: committed,
    error: null,
  };

  const display = deriveListingQueryDisplay(
    state,
    committed.key,
    true,
    null,
    100_000
  );

  assert.equal(display.phase, "refreshing");
  assert.strictEqual(display.snapshot, committed);
  assert.equal(display.transitioning, false);
  assert.equal(display.revalidating, true);
});

test("a fresh target cache is displayed atomically before the query effect starts", () => {
  const committed = snapshot(2, 10_000);
  const cachedTarget = snapshot(3, 99_999);
  const state: ListingQueryState = {
    phase: "ready",
    requestID: 1,
    requestedQuery: committed.query,
    snapshot: committed,
    error: null,
  };

  const display = deriveListingQueryDisplay(
    state,
    cachedTarget.key,
    true,
    cachedTarget,
    100_000
  );

  assert.equal(display.phase, "ready");
  assert.strictEqual(display.snapshot, cachedTarget);
  assert.equal(display.transitioning, false);
  assert.equal(display.revalidating, false);
});

test("a stale target cache is shown while it revalidates", () => {
  const committed = snapshot(2, 10_000);
  const cachedTarget = snapshot(3, 20_000);
  const state: ListingQueryState = {
    phase: "ready",
    requestID: 1,
    requestedQuery: committed.query,
    snapshot: committed,
    error: null,
  };

  const display = deriveListingQueryDisplay(
    state,
    cachedTarget.key,
    true,
    cachedTarget,
    100_000
  );

  assert.equal(display.phase, "refreshing");
  assert.strictEqual(display.snapshot, cachedTarget);
  assert.equal(display.transitioning, false);
  assert.equal(display.revalidating, true);
});
