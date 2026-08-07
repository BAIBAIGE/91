import { createContext, useContext, useMemo, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useAdminRouteActive } from "./AdminRouteCache";

type AdminPageActionsTarget = {
  target: HTMLElement | null;
};

const AdminPageActionsTargetContext = createContext<AdminPageActionsTarget | null>(null);

type AdminPageActionsProviderProps = {
  children: ReactNode;
  target: HTMLElement | null;
};

export function AdminPageActionsProvider({
  children,
  target,
}: AdminPageActionsProviderProps) {
  const value = useMemo(() => ({ target }), [target]);

  return (
    <AdminPageActionsTargetContext.Provider value={value}>
      {children}
    </AdminPageActionsTargetContext.Provider>
  );
}

export function AdminPageActions({ children }: { children: ReactNode }) {
  const context = useContext(AdminPageActionsTargetContext);
  const routeActive = useAdminRouteActive();

  if (!context?.target || !routeActive) {
    return null;
  }

  return createPortal(children, context.target);
}
