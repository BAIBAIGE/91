import { useEffect, useState } from "react";
import { ArrowUp } from "lucide-react";
import {
  pageScrollEventTarget,
  readPageScrollTop,
  scrollPageTo,
  usePageScrollRoot,
} from "@/lib/pageScroll";
import { useRouteActivity } from "@/lib/routeActivity";

type Props = {
  onVisibilityChange?: (visible: boolean) => void;
};

/**
 * 虚拟列表会在滚动过程中按实测行高做位置补偿，平滑滚动动画会被这些补偿
 * 打断、停在半路，所以返回顶部直接落到 0。
 */
export function BackToTop({ onVisibilityChange }: Props) {
  const scrollRootRef = usePageScrollRoot();
  const routeActive = useRouteActivity();
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (!routeActive) return;
    const scrollTarget = pageScrollEventTarget(scrollRootRef);
    if (!scrollTarget) return;
    function onScroll() {
      const nextVisible = readPageScrollTop(scrollRootRef) > 400;
      setVisible((current) => {
        if (current !== nextVisible) {
          onVisibilityChange?.(nextVisible);
        }
        return nextVisible;
      });
    }
    scrollTarget.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
    return () => scrollTarget.removeEventListener("scroll", onScroll);
  }, [onVisibilityChange, routeActive, scrollRootRef]);

  return (
    <button
      className={`back-to-top ${visible ? "is-visible" : ""}`}
      onClick={() =>
        scrollPageTo(scrollRootRef, { top: 0, behavior: "auto" })
      }
      aria-label="返回顶部"
    >
      <ArrowUp size={18} />
    </button>
  );
}
