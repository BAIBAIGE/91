import { createContext, useContext, useMemo, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";

type AdminPageActionsTarget = {
  activePathname: string;
  target: HTMLElement | null;
};

const AdminPageActionsTargetContext = createContext<AdminPageActionsTarget | null>(null);

type AdminPageActionsProviderProps = {
  activePathname: string;
  children: ReactNode;
  target: HTMLElement | null;
};

export function AdminPageActionsProvider({
  activePathname,
  children,
  target,
}: AdminPageActionsProviderProps) {
  const value = useMemo(
    () => ({ activePathname, target }),
    [activePathname, target]
  );

  return (
    <AdminPageActionsTargetContext.Provider value={value}>
      {children}
    </AdminPageActionsTargetContext.Provider>
  );
}

export function AdminPageActions({ children }: { children: ReactNode }) {
  const context = useContext(AdminPageActionsTargetContext);
  const ownerPathnameRef = useRef(context?.activePathname);

  if (
    !context?.target ||
    ownerPathnameRef.current !== context.activePathname
  ) {
    return null;
  }

  return createPortal(children, context.target);
}
