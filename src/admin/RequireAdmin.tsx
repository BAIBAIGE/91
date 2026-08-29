import { ReactNode } from "react";
import { Navigate } from "react-router";
import { useAuth } from "./AuthContext";
import { AuthUnavailable } from "./AuthUnavailable";

export function RequireAdmin({ children }: { children: ReactNode }) {
  const { status, isAdmin } = useAuth();

  if (status === "loading") {
    return null;
  }

  if (status === "unavailable") {
    return <AuthUnavailable />;
  }

  if (!isAdmin) {
    return <Navigate to="/" replace />;
  }

  return <>{children}</>;
}
