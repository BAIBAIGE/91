import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const headerSource = readFileSync(
  new URL("../src/components/VideoMetaHeader.tsx", import.meta.url),
  "utf8"
);
const detailCss = readFileSync(
  new URL("../src/styles/video-detail.css", import.meta.url),
  "utf8"
);

test("video detail source badge uses compact responsive padding", () => {
  assert.match(
    headerSource,
    /className="vd-meta__chip" data-tone=\{sourceKind \|\| "neutral"\}/
  );
  const baseRule = detailCss.match(/\.vd-meta__chip\s*\{[^}]*\}/s)?.[0] ?? "";
  assert.match(baseRule, /padding:\s*0 var\(--space-2\)/);
  assert.match(
    detailCss,
    /@media \(max-width: 768px\)[\s\S]*?\.vd-meta__chip\s*\{[^}]*padding:\s*0 6px/s
  );
});
