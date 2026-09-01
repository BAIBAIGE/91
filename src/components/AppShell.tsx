import { ReactNode, useEffect, useState } from "react";
import { TopBar } from "./TopBar";
import { MainNav } from "./MainNav";
import { SubNav } from "./SubNav";
import { Footer } from "./Footer";
import { BackToTop } from "./BackToTop";
import {
  pageScrollEventTarget,
  readPageScrollTop,
  usePageScrollRoot,
} from "@/lib/pageScroll";
import { useRouteActivity } from "@/lib/routeActivity";

type Props = {
  children: ReactNode;
  mobileAutoHideNav?: boolean;
};

const MOBILE_NAV_QUERY = "(max-width: 768px)";
const SCROLL_DELTA_THRESHOLD = 6;
const HIDE_AFTER_SCROLL_Y = 56;

export function AppShell({ children, mobileAutoHideNav = false }: Props) {
  const scrollRootRef = usePageScrollRoot();
  const routeActive = useRouteActivity();
  const [mobileNavHidden, setMobileNavHidden] = useState(false);
  const [backToTopVisible, setBackToTopVisible] = useState(false);

  useEffect(() => {
    if (!routeActive) return;
    if (!mobileAutoHideNav) {
      setMobileNavHidden(false);
      return;
    }

    const mediaQuery = window.matchMedia(MOBILE_NAV_QUERY);
    const scrollTarget = pageScrollEventTarget(scrollRootRef);
    if (!scrollTarget) return;
    let lastScrollY = Math.max(readPageScrollTop(scrollRootRef), 0);
    let ticking = false;

    const showNav = () => setMobileNavHidden(false);

    const updateNavVisibility = () => {
      ticking = false;
      const currentScrollY = Math.max(readPageScrollTop(scrollRootRef), 0);

      if (!mediaQuery.matches || currentScrollY <= 0) {
        showNav();
        lastScrollY = currentScrollY;
        return;
      }

      const delta = currentScrollY - lastScrollY;
      if (Math.abs(delta) < SCROLL_DELTA_THRESHOLD) return;

      if (delta > 0 && currentScrollY > HIDE_AFTER_SCROLL_Y) {
        setMobileNavHidden(true);
      } else if (delta < 0) {
        showNav();
      }

      lastScrollY = currentScrollY;
    };

    const handleScroll = () => {
      if (ticking) return;
      ticking = true;
      window.requestAnimationFrame(updateNavVisibility);
    };

    const handleMediaChange = () => {
      lastScrollY = Math.max(readPageScrollTop(scrollRootRef), 0);
      showNav();
    };

    handleMediaChange();
    scrollTarget.addEventListener("scroll", handleScroll, { passive: true });
    mediaQuery.addEventListener("change", handleMediaChange);

    return () => {
      scrollTarget.removeEventListener("scroll", handleScroll);
      mediaQuery.removeEventListener("change", handleMediaChange);
    };
  }, [mobileAutoHideNav, routeActive, scrollRootRef]);

  const className = [
    "app-shell",
    mobileAutoHideNav ? "app-shell--mobile-auto-hide-nav" : "",
    mobileNavHidden ? "is-mobile-nav-hidden" : "",
    backToTopVisible ? "is-back-to-top-visible" : "",
  ].filter(Boolean).join(" ");

  return (
    <div className={className}>
      <div className="app-shell__nav-stack">
        <TopBar />
        <MainNav />
        <SubNav />
      </div>
      <main className="app-shell__main">{children}</main>
      <Footer />
      <BackToTop onVisibilityChange={setBackToTopVisible} />
    </div>
  );
}
