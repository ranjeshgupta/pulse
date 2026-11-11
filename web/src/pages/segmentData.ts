// Data the segment builder reads: segments, the workspace's attributes, and the objects rules point
// at by id (forms, campaigns, journeys, companies, templates, assets, landing pages, users).
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { api, type AssetList, type AttributeDef, type Broadcast, type EventSchema, type Form, type Journey, type LandingPageList, type Segment, type Template, type User } from "../api";
import { validPath, type RefNames } from "../segmentRules";
import { useCompanyOptions } from "./Companies";

/** Every segment and list of the workspace, polled so sizes stay live. */
export function useSegments(poll = 3000) {
  return useQuery({ queryKey: ["segments"], queryFn: () => api.get<{ segments: Segment[] }>("/v1/segments"), refetchInterval: poll });
}

/** Segment and list names by id, so rules can describe references in words. */
export function useSegmentNames() {
  const segs = useSegments(0).data?.segments ?? [];
  return useMemo(() => Object.fromEntries(segs.map((s) => [s.id, s.name])) as Record<number, string>, [segs]);
}

export type RefItem = { id: number; name: string; channel?: string };

/** Objects rules can point at, with names by id for describe(). A list the user may not view just
 * stays empty, and ids show as numbers. */
export function useRefObjects() {
  const opts = { staleTime: 30_000, retry: false } as const;
  const forms = useQuery({ queryKey: ["forms"], queryFn: () => api.get<{ forms: Form[] }>("/v1/forms"), ...opts }).data?.forms;
  const broadcasts = useQuery({ queryKey: ["broadcasts"], queryFn: () => api.get<{ broadcasts: Broadcast[] }>("/v1/broadcasts"), ...opts }).data?.broadcasts;
  const journeys = useQuery({ queryKey: ["journeys"], queryFn: () => api.get<{ journeys: Journey[] }>("/v1/journeys"), ...opts }).data?.journeys;
  const templates = useQuery({ queryKey: ["templates", ""], queryFn: () => api.get<{ templates: Template[] }>("/v1/templates"), ...opts }).data?.templates;
  const assets = useQuery({ queryKey: ["assets"], queryFn: () => api.get<AssetList>("/v1/assets"), ...opts }).data?.assets;
  const pages = useQuery({ queryKey: ["landing-pages"], queryFn: () => api.get<LandingPageList>("/v1/landing-pages"), ...opts }).data?.pages;
  const users = useQuery({ queryKey: ["users"], queryFn: () => api.get<{ users: User[] }>("/v1/users"), ...opts }).data?.users;
  const companies = useCompanyOptions().data;
  return useMemo(() => {
    const byId = (xs?: RefItem[]) => Object.fromEntries((xs ?? []).map((x) => [x.id, x.name])) as Record<number, string>;
    const lists: Record<keyof RefNames, RefItem[]> = {
      forms: forms ?? [], broadcasts: broadcasts ?? [], journeys: journeys ?? [], companies: companies ?? [],
      templates: (templates ?? []).map((t) => ({ id: t.id, name: t.name, channel: t.channel })),
      assets: (assets ?? []).map((a) => ({ id: a.id, name: a.title || a.filename })),
      pages: (pages ?? []).map((p) => ({ id: p.page.id, name: p.page.title || p.page.slug })),
      users: (users ?? []).map((u) => ({ id: u.id, name: u.name || u.email })),
    };
    const names: RefNames = Object.fromEntries(Object.entries(lists).map(([k, v]) => [k, byId(v)])) as RefNames;
    return { lists, names };
  }, [forms, broadcasts, journeys, companies, templates, assets, pages, users]);
}

/** Attribute definitions plus keys seen on contacts, for the picker and typed inputs. */
export function useAttributes() {
  return useQuery({ queryKey: ["attributes"], queryFn: () => api.get<{ attributes: AttributeDef[]; undefined: string[] }>("/v1/attributes"), staleTime: 30_000 });
}

/** Common values of an attribute (or of an event's property, with event), fetched only once asked for. */
export function useKnownValues(key: string, enabled: boolean, event?: string) {
  const k = key.trim();
  const valid = (event ? validPath(k) : /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/.test(k)) && (!event || /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(event));
  return useQuery({
    queryKey: ["known-values", event ?? "", k],
    queryFn: () => api.get<{ values: { value: string; count: number }[] }>(
      event ? `/v1/events/${encodeURIComponent(event)}/values?prop=${encodeURIComponent(k)}` : `/v1/attributes/${encodeURIComponent(k)}/values`),
    enabled: enabled && valid,
    staleTime: 5 * 60_000,
    retry: false,
  });
}

/** Event names recorded in the workspace (last 90 days), for the "Did an event" filter. */
export function useEventNames() {
  return useQuery({ queryKey: ["event-names"], queryFn: () => api.get<{ names: string[] }>("/v1/events/names"), staleTime: 60_000, retry: false });
}

/** The workspace's event schemas (Audience → Events), for typed property filters. */
export function useEventSchemas() {
  return useQuery({ queryKey: ["event-schemas"], queryFn: () => api.get<{ schemas: EventSchema[] }>("/v1/events/schemas"), staleTime: 60_000, retry: false });
}
