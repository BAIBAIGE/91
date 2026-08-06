import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const layoutCss = readFileSync(
  new URL("../src/styles/layout.css", import.meta.url),
  "utf8"
);

function ruleBodies(css: string, selector: string): string[] {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return Array.from(
    css.matchAll(new RegExp(`^\\s*${escapedSelector}\\s*\\{([^}]*)\\}`, "gm")),
    (match) => match[1]
  );
}

test("public page sections preserve the shared container gutters", () => {
  const containerRules = ruleBodies(layoutCss, ".container");
  const pageSectionRules = ruleBodies(layoutCss, ".page-section");

  assert.ok(containerRules.length > 0);
  assert.ok(pageSectionRules.length > 0);

  for (const body of containerRules) {
    assert.match(body, /padding-inline\s*:/);
  }

  for (const body of pageSectionRules) {
    assert.match(body, /padding-block\s*:/);
    assert.doesNotMatch(body, /padding\s*:/);
    assert.doesNotMatch(body, /padding-(?:inline|left|right)\s*:/);
  }
});
