import {
  createContext,
  useContext,
  type ReactNode,
  type RefObject,
} from "react";

/**
 * Public pages normally scroll the document. A retained-listing video detail
 * uses its own foreground scroller so the document can stay frozen at the
 * exact listing position underneath it.
 */
export type PageScrollRootRef = RefObject<HTMLElement | null>;

const PageScrollRootContext = createContext<PageScrollRootRef | null>(null);

export function PageScrollRootProvider({
  scrollRootRef,
  children,
}: {
  scrollRootRef: PageScrollRootRef;
  children: ReactNode;
}) {
  return (
    <PageScrollRootContext.Provider value={scrollRootRef}>
      {children}
    </PageScrollRootContext.Provider>
  );
}

export function usePageScrollRoot(): PageScrollRootRef | null {
  return useContext(PageScrollRootContext);
}

export function readPageScrollTop(scrollRootRef: PageScrollRootRef | null) {
  if (scrollRootRef) return scrollRootRef.current?.scrollTop ?? 0;
  return window.scrollY;
}

export function pageScrollEventTarget(
  scrollRootRef: PageScrollRootRef | null
): Window | HTMLElement | null {
  if (scrollRootRef) return scrollRootRef.current;
  return window;
}

export function scrollPageTo(
  scrollRootRef: PageScrollRootRef | null,
  options: ScrollToOptions
) {
  const scrollRoot = scrollRootRef?.current;
  if (scrollRoot) {
    scrollRoot.scrollTo(options);
    return;
  }
  if (scrollRootRef) return;
  window.scrollTo(options);
}
