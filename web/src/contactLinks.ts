// M7: every count of contacts opens the Contacts list filtered to exactly those contacts. These
// helpers build the list's search (the same key:value filters the server parses in
// internal/api/contactview.go and contactsets.go) and say whether the signed-in role may open it
// (internal/api/perms.go viewFilterNeeds). Pure, so vitest covers them.

import { can, type Area, type Perms } from "./api";
import { chipToken } from "./contactFilters";
import { hasOwn, own } from "./safe";

/** A filter: key and value, joined into the list's query. */
export type ContactFilter = [key: string, value: string | number];

/** contactsQuery joins filters into the Contacts search text. */
export function contactsQuery(filters: ContactFilter[]): string {
  return filters.filter(([, v]) => v !== "" && v != null).map(([k, v]) => chipToken(k, String(v))).join(" ");
}

/** contactsHref is the Contacts list address for filters. */
export function contactsHref(filters: ContactFilter[]): string {
  const q = contactsQuery(filters);
  return q ? `/contacts?q=${encodeURIComponent(q)}` : "/contacts";
}

/** What each filter needs on top of contacts view: any one of these areas' view access. */
/** AREA_WORDS: role areas as the Roles page names them. */
const AREA_WORDS: Partial<Record<Area, string>> = { broadcasts: "one-time campaigns", journeys: "triggered & automation campaigns" };

const NEEDS: Record<string, Area[]> = {
  "campaign:broadcast": ["broadcasts", "reports"],
  "campaign:journey": ["journeys", "reports"],
  "campaign:any": ["reports"],
  journey: ["journeys", "reports"],
  amp_poll: ["content", "reports"],
  form: ["content", "reports"],
};

/** needKey names the permission rule a filter list falls under ("" = contacts view only). */
export function needKeys(filters: ContactFilter[]): string[] {
  const keys = filters.map(([k]) => k);
  const out: string[] = [];
  for (const [k, v] of filters) {
    if (k === "campaign") out.push(`campaign:${String(v).split("/")[0]}`);
    else if (hasOwn(NEEDS, k)) out.push(k); // own keys only: a filter named "constructor" needs nothing more
  }
  if (!keys.includes("campaign") && (keys.includes("outcome") || keys.includes("reason"))) out.push("campaign:any");
  return out;
}

/** canListContacts reports whether perms may open the list behind these filters. */
export function canListContacts(perms: Perms | undefined, filters: ContactFilter[]): boolean {
  if (!can(perms, "contacts", "view")) return false;
  return needKeys(filters).every((k) => (own(NEEDS, k) ?? []).some((a) => can(perms, a, "view")));
}

/** whyNoLink is the tooltip of a number the role can't open. */
export function whyNoLink(perms: Perms | undefined, filters: ContactFilter[]): string {
  if (!can(perms, "contacts", "view")) return "Your role can't open contact lists (it needs view access to contacts).";
  const missing = needKeys(filters).find((k) => !(own(NEEDS, k) ?? []).some((a) => can(perms, a, "view")));
  return missing ? `Your role can't list these contacts (it needs view access to ${(own(NEEDS, missing) ?? []).map((a) => AREA_WORDS[a] ?? a).join(" or ")}).` : "";
}

// ---- builders for the places that show counts ----

/** period is a dashboard's range as the list's period: filter (UTC days, both included). */
export const period = (from: string, to: string): ContactFilter => ["period", from === to ? from : `${from}..${to}`];

/** Estimate steps: the audience → exclusions → consent → reachable → cap (campaignUI's AudienceEstimate). */
export function estimateFilters(step: string, include: number[], exclude: number[], channel: string): ContactFilter[] {
  const f: ContactFilter[] = [["in", include.join(",")]];
  if (step === "audience") return f;
  if (exclude.length) f.push(["not_in", exclude.join(",")]);
  if (step === "exclude") return f;
  if (step === "consent") return [...f, ["subscribed", channel]];
  if (step === "opted_out") return [...f, ["unsubscribed", channel]];
  f.push(["reachable", channel]);
  if (step === "cap") f.push(["under_cap", channel]);
  return f;
}

/** A campaign's outcome (funnel steps, reasons, per-step message counts). */
export function outcomeFilters(kind: "broadcast" | "journey", id: number, outcome: string, extra: { step?: string; run?: string; reason?: string } = {}): ContactFilter[] {
  const f: ContactFilter[] = [["campaign", `${kind}/${id}`]];
  if (extra.run) f.push(["run", extra.run]);
  if (extra.step) f.push(["step", extra.step]);
  if (outcome) f.push(["outcome", outcome]);
  if (extra.reason) f.push(["reason", extra.reason]);
  return f;
}

/** A template's outcome, in any campaign (template reports), optionally in a period of days. */
export function templateFilters(id: number, outcome: string, days?: { from: string; to: string }, extra: { via?: string; reason?: string } = {}): ContactFilter[] {
  const f: ContactFilter[] = [["template", id]];
  if (outcome) f.push(["outcome", outcome]);
  if (extra.reason) f.push(["reason", extra.reason]);
  if (extra.via) f.push(["via", extra.via]);
  if (days) f.push(period(days.from, days.to));
  return f;
}

/** Funnel keys (funnelMath) → list outcomes. Steps not listed have no list (e.g. conversions). */
export const FUNNEL_OUTCOME: Record<string, string> = {
  targeted: "targeted", sent: "sent", delivered: "delivered", opened: "opened", read: "read", clicked: "clicked",
  downloaded: "downloaded", bounced: "bounced", hard_bounced: "hard_bounced", soft_bounced: "soft_bounced",
  complained: "complained", failed: "failed", unsubscribed: "unsubscribed",
};

/** Products dashboard: who viewed, added to cart, bought or abandoned a product (or a category). */
export function productFilters(what: "viewed" | "carted" | "bought" | "abandoned", events: { view: string; cart: string; purchase: string },
  item: { code?: string; category?: string }, from: string, to: string): ContactFilter[] {
  const f: ContactFilter[] = [];
  if (what === "viewed") f.push(["event", events.view]);
  if (what === "carted" || what === "abandoned") f.push(["event", events.cart]);
  if (what === "bought") f.push(["event", events.purchase]);
  if (what === "abandoned") f.push(["no_event", events.purchase]);
  if (item.code) f.push(["product", item.code]);
  if (item.category) f.push(["category", item.category]);
  f.push(period(from, to));
  return f;
}

/** addDays moves a YYYY-MM-DD day by n days (UTC). */
export function addDays(day: string, n: number): string {
  const d = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(d.getTime()) || !Number.isFinite(n)) return day; // toISOString would throw
  d.setUTCDate(d.getUTCDate() + n);
  return Number.isNaN(d.getTime()) ? day : d.toISOString().slice(0, 10);
}

/** Users dashboard: people active in a period (any, new or returning), optionally on a platform
 * and from a UTM source (null: any source; "" : none). */
export function activeFilters(kind: string, from: string, to: string, platform = "", source: string | null = null): ContactFilter[] {
  const f: ContactFilter[] = [["active", kind === "any" && platform ? platform : kind]];
  if (source !== null) f.push(["utm", source === "" ? "none" : source]);
  f.push(period(from, to));
  return f;
}

/** An on-site audience's segment part (what overlap counts use). */
export type SegmentAudience = { segment_ids?: number[]; exclude_segment_ids?: number[] };

/** audienceFilters: members of any of the audience's segments (everyone known without), minus its exclusions. */
export function audienceFilters(a: SegmentAudience): ContactFilter[] {
  const f: ContactFilter[] = [];
  if (a.segment_ids?.length) f.push(["in", a.segment_ids.join(",")]);
  if (a.exclude_segment_ids?.length) f.push(["not_in", a.exclude_segment_ids.join(",")]);
  return f;
}

/** overlapSeesFilters: who sees audience i first in order. Expressible only when every earlier
 * audience is plain segments without exclusions (then: not in any of them); otherwise null. */
export function overlapSeesFilters(auds: SegmentAudience[], i: number): ContactFilter[] | null {
  const earlier = auds.slice(0, i);
  if (earlier.some((a) => !a.segment_ids?.length || a.exclude_segment_ids?.length)) return null;
  const own = auds[i];
  const not = [...new Set([...(own.exclude_segment_ids ?? []), ...earlier.flatMap((a) => a.segment_ids ?? [])])];
  const f: ContactFilter[] = [];
  if (own.segment_ids?.length) f.push(["in", own.segment_ids.join(",")]);
  if (not.length) f.push(["not_in", not.join(",")]);
  return f;
}

/** overlapPairFilters: contacts in both audiences (each must name segments). */
export function overlapPairFilters(a: SegmentAudience, b: SegmentAudience): ContactFilter[] | null {
  if (!a.segment_ids?.length || !b.segment_ids?.length) return null;
  const not = [...new Set([...(a.exclude_segment_ids ?? []), ...(b.exclude_segment_ids ?? [])])];
  const f: ContactFilter[] = [["in", a.segment_ids.join(",")], ["in", b.segment_ids.join(",")]];
  if (not.length) f.push(["not_in", not.join(",")]);
  return f;
}

/** todayUTC is today's date as YYYY-MM-DD in UTC (the days dashboards and period: use). */
export const todayUTC = () => new Date().toISOString().slice(0, 10);
