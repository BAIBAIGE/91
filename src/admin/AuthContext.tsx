import {
  ReactNode,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import * as api from "./api";

type AuthStatus = "loading" | "authed" | "guest";

type AuthCtx = {
  status: AuthStatus;
  role: string;
  isAdmin: boolean;
  login: (username: string, password: string) => Promise<string | undefined>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
  invalidateSession: () => void;
};

const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [role, setRole] = useState<string>("");

  const invalidateSession = useCallback(() => {
    setStatus("guest");
    setRole("");
  }, []);

  const refresh = useCallback(async () => {
    try {
      const r = await api.me();
      if (!r.authenticated) {
        invalidateSession();
        return;
      }
      setStatus("authed");
      setRole(r.role ?? "");
    } catch {
      invalidateSession();
    }
  }, [invalidateSession]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const login = useCallback(async (u: string, p: string) => {
    const result = await api.login(u, p);
    setStatus("authed");
    setRole(result.role ?? "");
    return result.role;
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      invalidateSession();
    }
  }, [invalidateSession]);

  const isAdmin = role === "admin";

  const value = useMemo(
    () => ({ status, role, isAdmin, login, logout, refresh, invalidateSession }),
    [status, role, isAdmin, login, logout, refresh, invalidateSession]
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}
