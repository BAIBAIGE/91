import assert from "node:assert/strict";
import test from "node:test";
import { sha256Blob } from "../src/lib/sha256.ts";

const ABC_SHA256 =
  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

test("sha256Blob incrementally hashes a Blob", async () => {
  assert.equal(await sha256Blob(new Blob(["abc"])), ABC_SHA256);
});

test("sha256Blob honors cancellation", async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(sha256Blob(new Blob(["abc"]), controller.signal), {
    name: "AbortError",
  });
});
