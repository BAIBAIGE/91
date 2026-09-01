import { createContext, useContext, type ReactNode } from "react";

const RouteActivityContext = createContext(true);

/**
 * Retained routes stay mounted while another route is shown above them. This
 * context separates "still mounted" from "currently active" so background
 * pages can pause navigation-sensitive work without losing their UI state.
 */
export function RouteActivityProvider({
  active,
  children,
}: {
  active: boolean;
  children: ReactNode;
}) {
  return (
    <RouteActivityContext.Provider value={active}>
      {children}
    </RouteActivityContext.Provider>
  );
}

export function useRouteActivity(): boolean {
  return useContext(RouteActivityContext);
}
