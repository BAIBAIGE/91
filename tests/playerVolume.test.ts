import assert from "node:assert/strict";
import test from "node:test";
import { disablePlayerVolumePersistence } from "../src/lib/playerVolume";

class Storage {
  values: Record<string, unknown>;
  writes: string[] = [];
  deletions: string[] = [];

  constructor(values: Record<string, unknown> = {}) {
    this.values = { ...values };
  }

  get(key: string) {
    return this.values[key];
  }

  set(key: string, value: unknown) {
    this.writes.push(key);
    this.values[key] = value;
  }

  del(key: string) {
    this.deletions.push(key);
    delete this.values[key];
  }
}

test("old volume records are removed without clearing unrelated settings", () => {
  const storage = new Storage({ volume: 0.6, left: 20, top: 30 });
  disablePlayerVolumePersistence(storage);
  assert.deepEqual(storage.values, { left: 20, top: 30 });
  assert.deepEqual(storage.deletions, ["volume"]);
});

test("fresh players do not create an empty saved-settings record", () => {
  const storage = new Storage();
  disablePlayerVolumePersistence(storage);
  assert.deepEqual(storage.deletions, []);
  assert.deepEqual(storage.writes, []);
});

test("all subsequent native volume writes are ignored", () => {
  const storage = new Storage({ volume: 0.6 });
  disablePlayerVolumePersistence(storage);
  for (const volume of [0, 0.1, 0.5, 1]) {
    storage.set("volume", volume);
    assert.equal(storage.get("volume"), undefined);
  }
  assert.deepEqual(storage.writes, []);
});

test("unrelated writes retain the original storage receiver and behavior", () => {
  const storage = new Storage();
  disablePlayerVolumePersistence(storage);
  storage.set("left", 40);
  storage.set("top", 60);
  assert.deepEqual(storage.values, { left: 40, top: 60 });
  assert.deepEqual(storage.writes, ["left", "top"]);
});

test("the persistence policy is scoped to the configured player instance", () => {
  const player = new Storage();
  const other = new Storage();
  disablePlayerVolumePersistence(player);
  other.set("volume", 0.7);
  player.set("volume", 0.3);
  assert.equal(other.get("volume"), 0.7);
  assert.equal(player.get("volume"), undefined);
});

test("invalid and zero legacy volume records are also removed", () => {
  for (const volume of [0, null, "0.6", -1]) {
    const storage = new Storage({ volume });
    disablePlayerVolumePersistence(storage);
    assert.deepEqual(storage.values, {});
    assert.deepEqual(storage.deletions, ["volume"]);
  }
});
