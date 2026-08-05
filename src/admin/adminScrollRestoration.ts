export type AdminScrollRouteIdentity = {
  key: string;
  pathname: string;
};

type ResolveAdminScrollTargetOptions = {
  previousRoute: AdminScrollRouteIdentity | null;
  nextPathname: string;
  storedScrollTop: number | undefined;
  currentScrollTop: number;
};

/**
 * Existing history entries own their saved position. A newly-created search
 * state on the same route inherits the visible position so filters and
 * pagination do not jump. A genuinely new route starts at the top.
 */
export function resolveAdminScrollTarget({
  previousRoute,
  nextPathname,
  storedScrollTop,
  currentScrollTop,
}: ResolveAdminScrollTargetOptions) {
  if (storedScrollTop !== undefined) return storedScrollTop;
  if (previousRoute?.pathname === nextPathname) return currentScrollTop;
  return 0;
}
