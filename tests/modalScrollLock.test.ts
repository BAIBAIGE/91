import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const scrollLockSource = readFileSync(
  new URL("../src/lib/useDocumentScrollLock.ts", import.meta.url),
  "utf8"
);
const adminModalSource = readFileSync(
  new URL("../src/admin/Modal.tsx", import.meta.url),
  "utf8"
);
const videoInfoSource = readFileSync(
  new URL("../src/components/VideoInfoPanel.tsx", import.meta.url),
  "utf8"
);
const videoDetailSource = readFileSync(
  new URL("../src/pages/VideoDetailPage.tsx", import.meta.url),
  "utf8"
);

test("modal surfaces share a reference-counted document scroll lock", () => {
  assert.match(adminModalSource, /useDocumentScrollLock\(open\)/);
  assert.match(videoInfoSource, /useDocumentScrollLock\(editingTags\)/);
  assert.match(videoDetailSource, /useDocumentScrollLock\(deleteOpen && isAdmin\)/);
  assert.match(scrollLockSource, /let activeScrollLocks = 0/);
  assert.match(scrollLockSource, /activeScrollLocks \+= 1/);
  assert.match(scrollLockSource, /activeScrollLocks -= 1/);
  assert.match(scrollLockSource, /if \(activeScrollLocks > 0\) return/);
});

test("document scroll lock freezes touch and wheel scrolling without losing position", () => {
  assert.match(scrollLockSource, /scrollX: window\.scrollX|const scrollX = window\.scrollX/);
  assert.match(scrollLockSource, /scrollY: window\.scrollY|const scrollY = window\.scrollY/);
  assert.match(scrollLockSource, /root\.style\.overflow = "hidden"/);
  assert.match(scrollLockSource, /root\.style\.overscrollBehavior = "none"/);
  assert.match(scrollLockSource, /body\.style\.position = "fixed"/);
  assert.match(scrollLockSource, /body\.style\.top = `-\$\{scrollY\}px`/);
  assert.match(scrollLockSource, /body\.style\.overflow = "hidden"/);
  assert.match(scrollLockSource, /body\.style\.overscrollBehavior = "none"/);
  assert.match(scrollLockSource, /window\.scrollTo\(snapshot\.scrollX, snapshot\.scrollY\)/);
});

test("document scroll lock restores existing inline document styles", () => {
  for (const property of [
    "rootOverflow",
    "rootOverscrollBehavior",
    "rootScrollBehavior",
    "bodyPosition",
    "bodyTop",
    "bodyLeft",
    "bodyWidth",
    "bodyOverflow",
    "bodyOverscrollBehavior",
    "bodyPaddingRight",
  ]) {
    assert.match(scrollLockSource, new RegExp(`${property}:`));
    assert.match(scrollLockSource, new RegExp(`snapshot\\.${property}`));
  }
  // 3b66f6c 起补偿量改用 body 自身宽度增量测量：scrollbar-gutter: stable 下
  // root.clientWidth 会随滚动条隐藏而变宽，按它补偿会把整页左移。
  assert.match(
    scrollLockSource,
    /const bodyWidthBeforeLock = body\.getBoundingClientRect\(\)\.width;/
  );
  assert.match(
    scrollLockSource,
    /const widthGain = body\.getBoundingClientRect\(\)\.width - bodyWidthBeforeLock;\s*if \(widthGain > 0\) \{\s*body\.style\.paddingRight = `\$\{bodyPaddingRight \+ widthGain\}px`;/
  );
  assert.doesNotMatch(scrollLockSource, /window\.innerWidth - root\.clientWidth/);
});
