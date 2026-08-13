import assert from "node:assert/strict";
import test from "node:test";
import {
  fetchHomeVideos,
  fetchLatestHomeVideos,
  fetchListing,
  fetchTags,
  fetchUploadTags,
  invalidateTagsCache,
  readCachedTags,
} from "../src/data/videos";

test("home recommendations send only the requested display count", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  let requestPath = "";
  let requestInit: RequestInit | undefined;
  globalThis.fetch = (async (input, init) => {
    calls += 1;
    requestPath = String(input);
    requestInit = init;
    return new Response("[]", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const result = await fetchHomeVideos(8);

  assert.deepEqual(result, []);
  assert.equal(calls, 1);
  assert.equal(
    requestPath,
    "/api/home?count=8"
  );
  assert.equal(requestInit?.method, undefined);
  assert.equal(requestInit?.body, undefined);
  assert.equal(requestInit?.cache, "no-store");
  assert.equal(new Headers(requestInit?.headers).get("Accept"), "application/json");
});

test("latest home rotation requests only the visible batch", async (t) => {
  const originalFetch = globalThis.fetch;
  let requestPath = "";
  globalThis.fetch = (async (input) => {
    requestPath = String(input);
    return new Response(JSON.stringify([{ id: "latest-1" }]), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const result = await fetchLatestHomeVideos(8);

  assert.equal(requestPath, "/api/home/latest?count=8");
  assert.deepEqual(result.map((item) => item.id), ["latest-1"]);
});

test("home recommendations retry one transient GET failure", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    if (calls === 1) {
      return new Response("unavailable", { status: 503 });
    }
    return new Response(JSON.stringify([{ id: "video-after-retry" }]), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const result = await fetchHomeVideos();

  assert.equal(calls, 2);
  assert.deepEqual(result.map((item) => item.id), ["video-after-retry"]);
});

test("home recommendations trust one server-filtered response", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  let requestPath = "";
  globalThis.fetch = (async (input) => {
    calls += 1;
    requestPath = String(input);
    return new Response(JSON.stringify([
      { id: "first-new-video" },
      { id: "second-new-video" },
    ]), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const result = await fetchHomeVideos(12);

  assert.equal(calls, 1);
  assert.equal(requestPath, "/api/home?count=12");
  assert.deepEqual(
    result.map((item) => item.id),
    ["first-new-video", "second-new-video"]
  );
});

test("home recommendations keep the default request path short", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  let requestPath = "";
  globalThis.fetch = (async (input) => {
    calls += 1;
    requestPath = String(input);
    return new Response("[]", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const result = await fetchHomeVideos();

  assert.equal(calls, 1);
  assert.equal(requestPath, "/api/home");
  assert.deepEqual(result, []);
});

test("home recommendation request failures remain observable", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response("unavailable", { status: 503 });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  await assert.rejects(() => fetchHomeVideos(), /HTTP 503/);
  assert.equal(calls, 2);
});

test("listing request failures are not converted to an empty library", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response("unauthorized", { status: 401 });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  await assert.rejects(
    () => fetchListing(1, 96, { sort: "latest", includeTotal: false }),
    /HTTP 401/
  );
  assert.equal(calls, 1);
});

test("listing requests stop immediately when their caller aborts", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = ((_input, init) => {
    calls += 1;
    const signal = init?.signal;
    assert.ok(signal);
    return new Promise<Response>((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(signal.reason), { once: true });
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const controller = new AbortController();
  const request = fetchListing(2, 20, { sort: "latest" }, {
    signal: controller.signal,
  });
  controller.abort();

  await assert.rejects(
    request,
    (error: unknown) => error instanceof DOMException && error.name === "AbortError"
  );
  assert.equal(calls, 1);
});

test("caller cancellation also interrupts the GET retry delay", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  let firstCall!: () => void;
  const called = new Promise<void>((resolve) => {
    firstCall = resolve;
  });
  globalThis.fetch = (async () => {
    calls += 1;
    firstCall();
    return new Response("unavailable", { status: 503 });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const controller = new AbortController();
  const request = fetchListing(2, 20, undefined, {
    signal: controller.signal,
  });
  await called;
  controller.abort();

  await assert.rejects(
    request,
    (error: unknown) => error instanceof DOMException && error.name === "AbortError"
  );
  assert.equal(calls, 1, "an abandoned request must not start its retry");
});

test("an already aborted listing request never reaches fetch", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    return new Response("unexpected");
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const controller = new AbortController();
  controller.abort();
  await assert.rejects(
    () => fetchListing(1, 20, undefined, { signal: controller.signal }),
    (error: unknown) => error instanceof DOMException && error.name === "AbortError"
  );
  assert.equal(calls, 0);
});

test("tags stay cached for the current browser session", async (t) => {
  invalidateTagsCache();
  const originalFetch = globalThis.fetch;
  let calls = 0;
  const responseTags = [{ id: "tag-1", label: "标签一", count: 3 }];
  globalThis.fetch = (async (input) => {
    calls += 1;
    assert.equal(String(input), "/api/tags");
    return new Response(JSON.stringify(responseTags), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
    invalidateTagsCache();
  });

  const firstResult = await fetchTags();
  const secondResult = await fetchTags();

  assert.equal(calls, 1);
  assert.deepEqual(firstResult, responseTags);
  assert.strictEqual(secondResult, firstResult);
  assert.strictEqual(readCachedTags(), firstResult);
});

test("tag cache can be invalidated after an admin catalog change", async (t) => {
  invalidateTagsCache();
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    const tags = [{ id: `tag-${calls}`, label: `标签${calls}`, count: calls }];
    return new Response(JSON.stringify(tags), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
    invalidateTagsCache();
  });

  const before = await fetchTags();
  invalidateTagsCache();
  assert.equal(readCachedTags(), null);
  const after = await fetchTags();

  assert.equal(calls, 2);
  assert.notDeepEqual(after, before);
  assert.strictEqual(readCachedTags(), after);
});

test("upload tag choices always read the managed upload catalog", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  globalThis.fetch = (async (input) => {
    calls += 1;
    assert.equal(String(input), "/api/upload/tags");
    return new Response(
      JSON.stringify([{ id: `tag-${calls}`, label: `可选标签${calls}`, count: 0 }]),
      {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }
    );
  }) as typeof fetch;
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  const first = await fetchUploadTags();
  const second = await fetchUploadTags();

  assert.equal(calls, 2);
  assert.equal(first[0]?.label, "可选标签1");
  assert.equal(second[0]?.label, "可选标签2");
});
