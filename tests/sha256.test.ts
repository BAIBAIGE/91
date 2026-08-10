import assert from "node:assert/strict";
import test from "node:test";
import { sha256Blob } from "../src/lib/sha256.ts";

const ABC_SHA256 =
  "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

test("sha256Blob hashes with native Web Crypto when it is available", async () => {
  assert.ok(globalThis.crypto?.subtle);
  assert.equal(await sha256Blob(new Blob(["abc"])), ABC_SHA256);
});

test("sha256Blob falls back when an HTTP origin has no SubtleCrypto", async () => {
  assert.equal(await sha256Blob(new Blob(["abc"]), null), ABC_SHA256);
});
