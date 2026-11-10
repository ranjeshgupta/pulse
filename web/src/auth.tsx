import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { api, ApiError, type Me } from "./api";

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api.get<Me>("/v1/auth/me"),
    retry: (n, err) => !(err instanceof ApiError && err.status === 401) && n < 2,
    staleTime: 60_000,
  });
}

export function useIsAdmin() {
  return useMe().data?.role === "admin";
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
