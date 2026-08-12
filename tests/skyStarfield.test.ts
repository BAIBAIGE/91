import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import test from "node:test";
import { SkyStarfield } from "../src/components/SkyStarfield";

function renderWithTheme(theme: string): string {
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, "document");

  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {
      documentElement: {
        getAttribute: (name: string) => (name === "data-theme" ? theme : null),
      },
    },
  });

  try {
    return renderToStaticMarkup(createElement(SkyStarfield));
  } finally {
    if (originalDocument) {
      Object.defineProperty(globalThis, "document", originalDocument);
    } else {
      Reflect.deleteProperty(globalThis, "document");
    }
  }
}

test("star GIF nodes only exist in the sky theme", () => {
  assert.equal(renderWithTheme("dark"), "");
  assert.equal(renderWithTheme("pink"), "");

  const skyMarkup = renderWithTheme("sky");
  assert.equal(skyMarkup.match(/<img\b/g)?.length, 24);
  assert.match(skyMarkup, /\/stickers\/star-mini\.gif/);
  assert.match(skyMarkup, /\/stickers\/star-sparkle\.gif/);
});
