import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  UNSAFE_LocationContext,
  useLocation,
  useNavigationType,
  useOutlet,
} from "react-router";
import { ADMIN_PAGE_TITLES } from "./adminPageTitle";

type CachedAdminRoute = {
  outlet: ReactElement;
  locationContext: {
    location: ReturnType<typeof useLocation>;
    navigationType: ReturnType<typeof useNavigationType>;
  };
};

const AdminRouteActiveContext = createContext(true);

/**
 * Resolve nested URLs to their owning top-level admin page. The fixed list also
 * bounds the cache instead of retaining arbitrary or redirect-only routes.
 */
export function getAdminRouteCacheKey(pathname: string): string | null {
  const normalizedPath = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  const page = ADMIN_PAGE_TITLES.find(
    ({ path }) => normalizedPath === path || normalizedPath.startsWith(`${path}/`)
  );
  return page?.path ?? null;
}

export function useAdminRouteActive(): boolean {
  return useContext(AdminRouteActiveContext);
}

/** Run a silent freshness check whenever a retained page becomes active again. */
export function useAdminRouteRevalidation(revalidate: () => void): void {
  const active = useAdminRouteActive();
  const previouslyActiveRef = useRef(active);
  const revalidateRef = useRef(revalidate);
  revalidateRef.current = revalidate;

  useEffect(() => {
    if (active && !previouslyActiveRef.current) revalidateRef.current();
    previouslyActiveRef.current = active;
  }, [active]);
}

function CachedAdminRouteView({
  active,
  children,
}: {
  active: boolean;
  children: ReactNode;
}) {
  const containerRef = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    if (containerRef.current) containerRef.current.inert = !active;
  }, [active]);

  return (
    <div
      ref={containerRef}
      className="admin-route-cache-entry"
      hidden={!active}
      aria-hidden={!active ? true : undefined}
    >
      {children}
    </div>
  );
}

/**
 * Keep each visited admin page mounted for the lifetime of AdminLayout. A
 * cached page receives its last active location so URL-driven effects do not
 * react to another page's search params while it is hidden.
 */
export function AdminRouteCache() {
  const location = useLocation();
  const navigationType = useNavigationType();
  const outlet = useOutlet();
  const cacheKey = getAdminRouteCacheKey(location.pathname);
  const cachedRoutesRef = useRef(new Map<string, CachedAdminRoute>());

  if (!cacheKey || !outlet) return outlet;

  cachedRoutesRef.current.set(cacheKey, {
    outlet,
    locationContext: { location, navigationType },
  });

  return Array.from(cachedRoutesRef.current, ([key, route]) => {
    const active = key === cacheKey;
    return (
      <UNSAFE_LocationContext.Provider key={key} value={route.locationContext}>
        <AdminRouteActiveContext.Provider value={active}>
          <CachedAdminRouteView active={active}>
            {route.outlet}
          </CachedAdminRouteView>
        </AdminRouteActiveContext.Provider>
      </UNSAFE_LocationContext.Provider>
    );
  });
}
