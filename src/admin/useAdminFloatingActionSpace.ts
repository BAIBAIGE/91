import { useLayoutEffect, useRef } from "react";

const FLOATING_ACTION_SELECTOR = "[data-admin-floating-actions]";
const ACTION_SURROUNDING_SPACE_PX = 32;

/**
 * Reserves the rendered height of fixed page actions plus the same safe
 * surrounding space used by CPA's floating action bars.
 *
 * Mobile action bars can wrap as labels, safe-area insets, and viewport width
 * change. Measuring the rendered bars keeps the final page content reachable
 * without maintaining page-specific padding guesses.
 */
export function useAdminFloatingActionSpace<T extends HTMLElement>() {
  const pageRef = useRef<T>(null);

  useLayoutEffect(() => {
    const currentPage = pageRef.current;
    if (!currentPage) return;
    const page: T = currentPage;

    let frame = 0;
    const observedActions = new Set<Element>();

    const resizeObserver =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(() => scheduleMeasure());

    function syncObservedActions(actions: Element[]) {
      if (!resizeObserver) return;

      for (const action of observedActions) {
        if (actions.includes(action)) continue;
        resizeObserver.unobserve(action);
        observedActions.delete(action);
      }

      for (const action of actions) {
        if (observedActions.has(action)) continue;
        observedActions.add(action);
        resizeObserver.observe(action);
      }
    }

    function measure() {
      frame = 0;
      const actions = Array.from(page.querySelectorAll(FLOATING_ACTION_SELECTOR));
      syncObservedActions(actions);

      let actionHeight = 0;

      for (const action of actions) {
        if (!(action instanceof HTMLElement)) continue;
        const style = window.getComputedStyle(action);
        if (
          style.position !== "fixed" ||
          style.display === "none" ||
          style.visibility === "hidden" ||
          action.getClientRects().length === 0
        ) {
          continue;
        }

        actionHeight = Math.max(actionHeight, action.getBoundingClientRect().height);
      }

      const nextValue =
        actionHeight > 0
          ? `calc(${Math.ceil(actionHeight + ACTION_SURROUNDING_SPACE_PX)}px + env(safe-area-inset-bottom, 0px))`
          : "0px";
      if (page.style.getPropertyValue("--admin-floating-actions-space") !== nextValue) {
        page.style.setProperty("--admin-floating-actions-space", nextValue);
      }
    }

    function scheduleMeasure() {
      if (frame) return;
      frame = window.requestAnimationFrame(measure);
    }

    const mutationObserver = new MutationObserver(scheduleMeasure);
    mutationObserver.observe(page, {
      attributes: true,
      attributeFilter: ["class", "hidden", "style"],
      childList: true,
      subtree: true,
    });

    window.addEventListener("resize", scheduleMeasure);
    measure();

    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      mutationObserver.disconnect();
      resizeObserver?.disconnect();
      window.removeEventListener("resize", scheduleMeasure);
      page.style.removeProperty("--admin-floating-actions-space");
    };
  }, []);

  return pageRef;
}
