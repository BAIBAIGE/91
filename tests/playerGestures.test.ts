import assert from "node:assert/strict";
import test from "node:test";
import {
  getPlayerGesturePoint,
  setPlayerGestureVolume,
  type PlayerGestureFrame,
} from "../src/lib/playerGestures";

const portrait: PlayerGestureFrame = {
  left: 20,
  top: 80,
  width: 390,
  height: 844,
  rotated: false,
};

test("gesture coordinates are relative to the player, including page offsets", () => {
  assert.deepEqual(getPlayerGesturePoint({ clientX: 120, clientY: 280 }, portrait), {
    x: 100, y: 200, width: 390, height: 844,
  });
});

test("CSS rotation swaps the axes and reverses the local vertical axis", () => {
  const frame = { ...portrait, rotated: true };
  assert.deepEqual(getPlayerGesturePoint({ clientX: 410, clientY: 80 }, frame), {
    x: 0, y: 0, width: 844, height: 390,
  });
  assert.deepEqual(getPlayerGesturePoint({ clientX: 20, clientY: 924 }, frame), {
    x: 844, y: 390, width: 844, height: 390,
  });
});

test("upward swipes on either side remain vertical after CSS rotation", () => {
  const frame = { ...portrait, rotated: true };
  for (const sideRatio of [0.2, 0.8]) {
    const clientY = frame.top + frame.height * sideRatio;
    const start = getPlayerGesturePoint({ clientX: 120, clientY }, frame);
    const end = getPlayerGesturePoint({ clientX: 220, clientY }, frame);
    assert.equal(start.x < start.width / 2, sideRatio < 0.5);
    assert.equal(end.x - start.x, 0);
    assert.equal(end.y - start.y, -100);
    assert.equal((start.y - end.y) / start.height, 100 / 390);
  }
});

test("horizontal seeks use the displayed player width after CSS rotation", () => {
  const frame = { ...portrait, rotated: true };
  const start = getPlayerGesturePoint({ clientX: 200, clientY: 200 }, frame);
  const end = getPlayerGesturePoint({ clientX: 200, clientY: 411 }, frame);
  assert.equal(end.y - start.y, 0);
  assert.equal((end.x - start.x) / start.width, 0.25);
});

test("native landscape keeps screen axes without applying another rotation", () => {
  const frame = { ...portrait, width: 844, height: 390 };
  const start = getPlayerGesturePoint({ clientX: 700, clientY: 300 }, frame);
  const end = getPlayerGesturePoint({ clientX: 700, clientY: 200 }, frame);
  assert.ok(start.x > start.width / 2);
  assert.equal(end.x - start.x, 0);
  assert.equal((start.y - end.y) / start.height, 100 / 390);
});

test("gesture coordinates continue beyond the player without clamping movement", () => {
  assert.equal(getPlayerGesturePoint({ clientX: 0, clientY: 60 }, portrait).y, -20);
  assert.equal(getPlayerGesturePoint({ clientX: 430, clientY: 100 }, {
    ...portrait, rotated: true,
  }).y, -20);
});

test("volume gestures return the actual rounded volume and clear mute on success", () => {
  const media = { volume: 0.5, muted: true };
  assert.equal(setPlayerGestureVolume(media, 0.674), 0.67);
  assert.deepEqual(media, { volume: 0.67, muted: false });
});

test("volume gestures clamp endpoints and synchronize mute", () => {
  const media = { volume: 0.5, muted: false };
  assert.equal(setPlayerGestureVolume(media, -0.5), 0);
  assert.deepEqual(media, { volume: 0, muted: true });
  assert.equal(setPlayerGestureVolume(media, 2), 1);
  assert.deepEqual(media, { volume: 1, muted: false });
});

test("ignored volume assignments do not report success or change mute", () => {
  for (const muted of [true, false]) {
    const media = {
      get volume() { return 1; },
      set volume(_value: number) {},
      muted,
    };
    assert.equal(setPlayerGestureVolume(media, 0.65), null);
    assert.equal(media.volume, 1);
    assert.equal(media.muted, muted);
    assert.equal(setPlayerGestureVolume(media, 0), null);
    assert.equal(media.muted, muted);
  }
});

test("throwing volume setters degrade without escaping into the touch handler", () => {
  const media = {
    get volume() { return 1; },
    set volume(_value: number) { throw new Error("read only"); },
    muted: true,
  };
  assert.equal(setPlayerGestureVolume(media, 0.5), null);
  assert.equal(media.muted, true);
});

test("volume feedback uses readback rather than the requested value", () => {
  let volume = 1;
  const media = {
    get volume() { return volume; },
    set volume(value: number) { volume = value + 0.00001; },
    muted: false,
  };
  assert.equal(setPlayerGestureVolume(media, 0.65), 0.65001);
});

test("invalid target volumes leave the media untouched", () => {
  const media = { volume: 0.5, muted: true };
  for (const target of [NaN, Infinity, -Infinity]) {
    assert.equal(setPlayerGestureVolume(media, target), null);
    assert.deepEqual(media, { volume: 0.5, muted: true });
  }
});
