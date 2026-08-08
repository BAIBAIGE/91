import { ReactNode } from "react";
import { Navigate } from "react-router";
import { useAuth } from "./AuthContext";

export function RequireAdmin({ children }: { children: ReactNode }) {
  const { status, isAdmin } = useAuth();

  if (status === "loading") {
    return null;
  }

  if (!isAdmin) {
    return <Navigate to="/" replace />;
  }

  return <>{children}</>;
}
