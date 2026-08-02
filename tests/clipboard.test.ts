import assert from "node:assert/strict";
import test from "node:test";
import { copyTextToClipboard } from "../src/lib/clipboard.ts";

type ReplaceableGlobal = "document" | "navigator";

function replaceGlobal(name: ReplaceableGlobal, value: unknown) {
  const original = Object.getOwnPropertyDescriptor(globalThis, name);
  Object.defineProperty(globalThis, name, {
    configurable: true,
    writable: true,
    value,
  });
  return () => {
    if (original) Object.defineProperty(globalThis, name, original);
    else Reflect.deleteProperty(globalThis, name);
  };
}

test("clipboard copy prefers the modern browser API", async () => {
  let copiedText = "";
  const restoreNavigator = replaceGlobal("navigator", {
    clipboard: {
      writeText: async (text: string) => {
        copiedText = text;
      },
    },
  });

  try {
    assert.equal(await copyTextToClipboard("runtime log"), true);
    assert.equal(copiedText, "runtime log");
  } finally {
    restoreNavigator();
  }
});

test("clipboard copy falls back after an unavailable secure-context write", async () => {
  let selectedText = "";
  let removed = false;
  let restoredFocus = false;
  const textarea = {
    value: "",
    readOnly: false,
    tabIndex: 0,
    style: {} as Record<string, string>,
    setAttribute: () => undefined,
    focus: () => undefined,
    select: () => {
      selectedText = textarea.value;
    },
    setSelectionRange: () => undefined,
    remove: () => {
      removed = true;
    },
  };
  const restoreNavigator = replaceGlobal("navigator", {
    clipboard: {
      writeText: async () => {
        throw new Error("not allowed");
      },
    },
  });
  const restoreDocument = replaceGlobal("document", {
    activeElement: {
      focus: () => {
        restoredFocus = true;
      },
    },
    body: {
      appendChild: () => undefined,
    },
    createElement: () => textarea,
    execCommand: (command: string) => command === "copy",
  });

  try {
    assert.equal(await copyTextToClipboard("HTTP log line"), true);
    assert.equal(selectedText, "HTTP log line");
    assert.equal(removed, true);
    assert.equal(restoredFocus, true);
  } finally {
    restoreDocument();
    restoreNavigator();
  }
});

test("clipboard copy reports failure when neither API is available", async () => {
  const restoreNavigator = replaceGlobal("navigator", {});
  const restoreDocument = replaceGlobal("document", undefined);

  try {
    assert.equal(await copyTextToClipboard("runtime log"), false);
  } finally {
    restoreDocument();
    restoreNavigator();
  }
});
