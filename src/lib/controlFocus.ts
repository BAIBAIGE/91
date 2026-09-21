/**
 * Release focus after pointer activation of discrete controls throughout the
 * app. Native selects can match :focus-visible even when opened with a mouse.
 * Keyboard navigation, text editing, and controls that move focus elsewhere
 * retain their normal focus behavior.
 */
export function initializeControlFocus(document: Document): () => void {
  const browser = document.defaultView;
  if (!browser) return () => {};

  let pointerControl: HTMLElement | null = null;
  let pendingFrame: number | undefined;

  function cancelRelease() {
    if (pendingFrame !== undefined) browser!.cancelAnimationFrame(pendingFrame);
    pendingFrame = undefined;
  }

  function clearPointerControl() {
    cancelRelease();
    pointerControl = null;
  }

  function onPointerDown(event: PointerEvent) {
    clearPointerControl();
    if (event.button !== 0 || !event.isPrimary) return;
    pointerControl = findControl(event.target);
  }

  function releaseAfterActivation(control: HTMLElement) {
    if (pointerControl !== control) return;
    cancelRelease();
    // Let React handlers, native activation, and dialog focus management run
    // first. Never blur a different element focused by the control's action.
    pendingFrame = browser!.requestAnimationFrame(() => {
      pendingFrame = undefined;
      if (document.activeElement === control && control.isConnected) control.blur();
      pointerControl = null;
    });
  }

  function onClick(event: MouseEvent) {
    const control = findControl(event.target);
    // Blurring on the opening click would close a native dropdown immediately.
    if (control && !(control instanceof HTMLSelectElement)) {
      releaseAfterActivation(control);
    }
  }

  function onChange(event: Event) {
    const control = findControl(event.target);
    if (control) releaseAfterActivation(control);
  }

  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("keydown", clearPointerControl, true);
  document.addEventListener("pointercancel", clearPointerControl, true);
  document.addEventListener("click", onClick, true);
  document.addEventListener("change", onChange, true);

  return () => {
    clearPointerControl();
    document.removeEventListener("pointerdown", onPointerDown, true);
    document.removeEventListener("keydown", clearPointerControl, true);
    document.removeEventListener("pointercancel", clearPointerControl, true);
    document.removeEventListener("click", onClick, true);
    document.removeEventListener("change", onChange, true);
  };
}

function findControl(target: EventTarget | null): HTMLElement | null {
  if (!(target instanceof Element)) return null;
  const element = target.closest(
    "button, a[href], select, input, textarea, [contenteditable], [role='button'], summary, label",
  );
  const control = element instanceof HTMLLabelElement ? element.control : element;
  if (!(control instanceof HTMLElement) || control.isContentEditable) return null;
  if (control instanceof HTMLSelectElement) {
    return !control.multiple && control.size <= 1 ? control : null;
  }
  return control.matches(
    "button, a[href], [role='button'], summary, input[type='button'], input[type='submit'], input[type='reset'], input[type='checkbox'], input[type='radio']",
  ) ? control : null;
}
