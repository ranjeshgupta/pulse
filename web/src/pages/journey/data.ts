// What the journey builder's pickers and canvas summaries need: forms, assets, landing pages, focus
// items, DWC slots, teammates, companies, journeys, lists, stages, templates and CRM integrations. Each list the user
// may not view just stays empty.
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { api, type DWCSlot, type FocusItem, type Integration, type Segment } from "../../api";
import { useRefObjects } from "../segmentData";
import { useStages } from "../Stages";
import type { Names } from "./nodeText";

export type Pick = { id: number; name: string };

/** Integration kinds that can receive contacts (push_integration). */
const PUSH_KINDS = new Set(["hubspot", "salesforce"]);

export function usePickers(segments: Segment[]) {
  // Pickers refresh when the window regains focus: people add a stage or focus item in another tab.
  const opts = { staleTime: 30_000, retry: false, refetchOnWindowFocus: true } as const;
  const ref = useRefObjects();
  const focus = useQuery({ queryKey: ["focus"], queryFn: () => api.get<{ focus_items: FocusItem[] }>("/v1/focus-items"), ...opts }).data?.focus_items;
  // CRM integrations a push step can use (settings view; others see an empty list).
  const ints = useQuery({ queryKey: ["integrations"], queryFn: () => api.get<{ integrations: Integration[] }>("/v1/integrations"), ...opts }).data?.integrations;
  const dwc = useQuery({ queryKey: ["dwc"], queryFn: () => api.get<{ slots: DWCSlot[] }>("/v1/dwc"), ...opts }).data?.slots;
  const stagesQ = useStages();
  const refetchStages = stagesQ.refetch;
  useEffect(() => {
    const f = () => { void refetchStages(); };
    window.addEventListener("focus", f);
    return () => window.removeEventListener("focus", f);
  }, [refetchStages]);
  const stages = stagesQ.data;
  return useMemo(() => {
    const l = ref.lists;
    const lists = {
      forms: l.forms as Pick[], assets: l.assets as Pick[], pages: l.pages as Pick[], users: l.users as Pick[],
      companies: l.companies as Pick[], journeys: l.journeys as Pick[],
      templates: l.templates,
      focus: (focus ?? []).map((f) => ({ id: f.id, name: f.name })) as Pick[],
      integrations: (ints ?? []).filter((i) => PUSH_KINDS.has(i.kind)).map((i) => ({ id: i.id, name: i.name })) as Pick[],
      slots: (dwc ?? []).map((s) => ({ slot: s.slot, name: s.name || s.slot })),
      stages: (stages ?? []).map((s) => s.name),
      segments,
      staticLists: segments.filter((s) => s.kind === "static"),
    };
    const byId = (xs: { id: number; name: string }[]) => Object.fromEntries(xs.map((x) => [x.id, x.name])) as Record<number, string>;
    const names: Names = {
      segments: byId(segments), templates: byId(l.templates), forms: byId(lists.forms), assets: byId(lists.assets),
      pages: byId(lists.pages), focus: byId(lists.focus), users: byId(lists.users), companies: byId(lists.companies),
      journeys: byId(lists.journeys), integrations: byId(lists.integrations), slots: Object.fromEntries(lists.slots.map((s) => [s.slot, s.name])), refs: ref.names,
    };
    return { lists, names };
  }, [ref, focus, dwc, stages, segments, ints]);
}
export type Pickers = ReturnType<typeof usePickers>;
