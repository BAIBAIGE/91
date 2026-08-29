import { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { useAuth } from "./AuthContext";
import { AuthUnavailable } from "./AuthUnavailable";

// 登录守卫：未登录跳 /login，并把目的地放到 state，登录后可回跳
export function RequireAuth({ children }: { children: ReactNode }) {
  const { status } = useAuth();
  const location = useLocation();

  if (status === "loading") {
    return null;
  }

  if (status === "unavailable") {
    return <AuthUnavailable />;
  }

  if (status === "guest") {
    return (
      <Navigate
        to="/login"
        replace
        state={{ from: location.pathname + location.search }}
      />
    );
  }

  return <>{children}</>;
}
