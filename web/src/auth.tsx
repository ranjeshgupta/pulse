import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { api, ApiError, can, type Area, type Level, type Me } from "./api";

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api.get<Me>("/v1/auth/me"),
    retry: (n, err) => !(err instanceof ApiError && err.status === 401) && n < 2,
    staleTime: 60_000,
  });
}

/** useIsAdmin: may change workspace settings (admins, or a custom role with settings edit). */
export function useIsAdmin() {
  return can(useMe().data?.permissions, "settings", "edit");
}

/** useCan reports whether the signed-in user's role reaches level in area. */
export function useCan(area: Area, level: Exclude<Level, "none">) {
  return can(useMe().data?.permissions, area, level);
}

// RequireAuth sends signed-out visitors to the login page, remembering where they were going.
export function RequireAuth({ children }: { children: ReactNode }) {
  const me = useMe();
  const loc = useLocation();
  if (me.isLoading) return null;
  if (me.error instanceof ApiError && me.error.status === 401) {
    return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname)}`} replace />;
  }
  return <>{children}</>;
}

export function useSignOut() {
  const qc = useQueryClient();
  return async () => {
    await api.post("/v1/auth/logout");
    qc.clear();
    window.location.assign("/login");
  };
}
