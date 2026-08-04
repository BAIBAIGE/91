import { createContext, useContext, type ReactNode } from "react";
import { createPortal } from "react-dom";

const AdminPageActionsTargetContext = createContext<HTMLElement | null>(null);

type AdminPageActionsProviderProps = {
  children: ReactNode;
  target: HTMLElement | null;
};

export function AdminPageActionsProvider({
  children,
  target,
}: AdminPageActionsProviderProps) {
  return (
    <AdminPageActionsTargetContext.Provider value={target}>
      {children}
    </AdminPageActionsTargetContext.Provider>
  );
}

export function AdminPageActions({ children }: { children: ReactNode }) {
  const target = useContext(AdminPageActionsTargetContext);
  return target ? createPortal(children, target) : null;
}
