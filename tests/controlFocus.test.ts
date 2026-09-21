import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { initializeControlFocus } from "../src/lib/controlFocus.ts";

function setup(t: TestContext) {
  const frames = new Map<number, FrameRequestCallback>();
  let frameID = 0;
  const document = Object.assign(new EventTarget(), {
    activeElement: null as Control | null,
    defaultView: {
      requestAnimationFrame(callback: FrameRequestCallback) {
        frames.set(++frameID, callback);
        return frameID;
      },
      cancelAnimationFrame(id: number) {
        frames.delete(id);
      },
    },
  });

  class Control {
    isConnected = true;
    isContentEditable = false;
    blurCount = 0;
    closest() { return this; }
    matches() { return true; }
    blur() {
      this.blurCount += 1;
      document.activeElement = null;
    }
  }
  class Select extends Control {
    multiple = false;
    size = 1;
  }
  class Label extends Control {}

  for (const [key, value] of Object.entries({
    Element: Control,
    HTMLElement: Control,
    HTMLSelectElement: Select,
    HTMLLabelElement: Label,
  })) {
    const original = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, value });
    t.after(() => {
      if (original) Object.defineProperty(globalThis, key, original);
      else Reflect.deleteProperty(globalThis, key);
    });
  }

  const dispose = initializeControlFocus(document as unknown as Document);
  t.after(dispose);

  function dispatch(type: string, target: Control) {
    const event = new Event(type, { cancelable: true });
    Object.defineProperties(event, {
      target: { value: target },
      button: { value: 0 },
      isPrimary: { value: true },
      pointerType: { value: "touch" },
    });
    document.dispatchEvent(event);
    return event;
  }

  function flushFrame() {
    const callbacks = [...frames.values()];
    frames.clear();
    callbacks.forEach(callback => callback(0));
  }

  const control = new Control();
  document.activeElement = control;
  return { document, control, Control, Select, dispatch, flushFrame, dispose };
}

test("a click canceled after document capture retains the card's focus", (t) => {
  const { document, control, dispatch, flushFrame } = setup(t);
  for (let tap = 0; tap < 2; tap += 1) {
    dispatch("pointerdown", control);
    const click = dispatch("click", control);
    // React's card handler runs after the document capture listener and keeps
    // the user on the card to preview it. Blurring would clear that intent.
    click.preventDefault();
    flushFrame();
    assert.equal(document.activeElement, control);
    assert.equal(control.blurCount, 0);
  }
});

test("an ordinary pointer activation releases focus after handlers finish", (t) => {
  const { document, control, dispatch, flushFrame } = setup(t);
  dispatch("pointerdown", control);
  dispatch("click", control);
  assert.equal(document.activeElement, control);
  flushFrame();
  assert.equal(document.activeElement, null);
  assert.equal(control.blurCount, 1);
});

test("keyboard activation retains focus after a prior pointer interaction", (t) => {
  const { document, control, dispatch, flushFrame } = setup(t);
  dispatch("pointerdown", control);
  dispatch("keydown", control);
  dispatch("click", control);
  flushFrame();
  assert.equal(document.activeElement, control);
});

test("activation never blurs focus moved by the component", (t) => {
  const { document, control, Control, dispatch, flushFrame } = setup(t);
  const destination = new Control();
  dispatch("pointerdown", control);
  dispatch("click", control);
  document.activeElement = destination;
  flushFrame();
  assert.equal(document.activeElement, destination);
  assert.equal(control.blurCount, 0);
  assert.equal(destination.blurCount, 0);
});

test("a select retains focus while opening and releases it after selection", (t) => {
  const { document, Select, dispatch, flushFrame } = setup(t);
  const select = new Select();
  document.activeElement = select;
  dispatch("pointerdown", select);
  dispatch("click", select);
  flushFrame();
  assert.equal(document.activeElement, select);
  dispatch("change", select);
  flushFrame();
  assert.equal(document.activeElement, null);
});

test("pointer cancellation and disposal cancel a pending focus release", (t) => {
  const { document, control, dispatch, flushFrame, dispose } = setup(t);
  dispatch("pointerdown", control);
  dispatch("click", control);
  dispatch("pointercancel", control);
  flushFrame();
  assert.equal(document.activeElement, control);
  dispatch("pointerdown", control);
  dispatch("click", control);
  dispose();
  flushFrame();
  assert.equal(document.activeElement, control);
});
