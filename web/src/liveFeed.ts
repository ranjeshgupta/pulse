// Live events page: the poll's query string, merging polled events into the list (newest first,
// deduplicated, capped), filter checks, backoff and the one-line props preview. Pure, so vitest covers it.

import type { LiveEvent } from "./api";

export const POLL_MS = 2_000;
/** After HTTP 429 (more than 120 polls a minute for the workspace). */
export const RATE_LIMIT_BACKOFF_MS = 5_000;
/** After any other error: retry slower. */
export const ERROR_BACKOFF_MS = 10_000;
export const MAX_ROWS = 500;
export const PAGE_LIMIT = 200;

export interface LiveFilters { name: string; contact: string }

/** liveQuery builds the query string of GET /v1/live/events; no cursor means "the latest events". */
export function liveQuery(cursor: string, f: LiveFilters, limit = PAGE_LIMIT): string {
  const p = new URLSearchParams();
  if (cursor) p.set("cursor", cursor);
  const name = f.name.trim();
  const contact = f.contact.trim();
  if (name) p.set("name", name);
  if (contact) p.set("contact", contact);
  p.set("limit", String(Math.max(1, Math.min(PAGE_LIMIT, Math.floor(limit)))));
  return p.toString();
}

const NAME_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}\*?$/;

/** nameProblem explains why an event name filter can't be used, or null (empty is fine: every event). */
export function nameProblem(name: string): string | null {
  const n = name.trim();
  if (!n) return null;
  if (n.indexOf("*") !== -1 && n.indexOf("*") !== n.length - 1) return "A * can only come at the end, to match names starting with what's before it.";
  if (n === "*") return null;
  if (!NAME_RE.test(n)) return "Event names start with a letter and use letters, digits and _ . : - (up to 64 characters).";
  return null;
}

/** normalizedName drops a lone "*" (it matches everything) so it doesn't reach the server. */
export const normalizedName = (name: string) => (name.trim() === "*" ? "" : name.trim());

/** eventKey identifies an event across polls (its stream cursor is unique too, but the id is stable). */
export const eventKey = (e: LiveEvent) => e.id || e.cursor;

/** mergeEvents adds a poll's events (oldest first, as the server sends them) to the list (newest first):
 * events already shown are skipped, the newest end up on top and the list keeps at most `cap` rows.
 * It returns the new list and how many events were actually added. */
export function mergeEvents(current: LiveEvent[], incoming: LiveEvent[], cap = MAX_ROWS): { events: LiveEvent[]; added: number } {
  if (incoming.length === 0) return { events: current.length > cap ? current.slice(0, cap) : current, added: 0 };
  const seen = new Set(current.map(eventKey));
  const fresh: LiveEvent[] = [];
  for (let i = incoming.length - 1; i >= 0; i--) {
    const e = incoming[i];
    const k = eventKey(e);
    if (seen.has(k)) continue;
    seen.add(k);
    fresh.push(e);
  }
  if (fresh.length === 0) return { events: current.length > cap ? current.slice(0, cap) : current, added: 0 };
  const events = fresh.concat(current);
  return { events: events.length > cap ? events.slice(0, cap) : events, added: fresh.length };
}

/** retryDelay: how long to wait before the next poll given how the last one ended. */
export function retryDelay(error: unknown): number {
  if (!error) return POLL_MS;
  const status = (error as { status?: number }).status;
  return status === 429 ? RATE_LIMIT_BACKOFF_MS : ERROR_BACKOFF_MS;
}

export const isRateLimited = (error: unknown) => (error as { status?: number } | null)?.status === 429;

/** clockTime: the event's local time as HH:MM:SS (24 hour), or "" for a bad timestamp. */
export function clockTime(ts: string): string {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function previewValue(v: unknown): string {
  if (v === null) return "null";
  if (typeof v === "string") return JSON.stringify(v);
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `[${v.length}]`;
  if (typeof v === "object") return "{…}";
  return String(v);
}

/** propsPreview: a compact one-line view of the properties (key=value, nested values summarised),
 * cut at max characters. Truncated events (too large to send whole) say so. */
export function propsPreview(props: Record<string, unknown> | undefined, truncated = false, max = 140): string {
  const keys = props ? Object.keys(props) : [];
  if (keys.length === 0) return truncated ? "Properties too large to show" : "No properties";
  let s = keys.map((k) => `${k}=${previewValue(props![k])}`).join("  ");
  if (s.length > max) s = s.slice(0, Math.max(0, max - 1)).trimEnd() + "…";
  return truncated ? `${s} (cut short)` : s;
}

/** propsJSON: the properties formatted for the expanded view. */
export const propsJSON = (props: Record<string, unknown> | undefined) => JSON.stringify(props ?? {}, null, 2);

/** announcement: what the polite live region says about events that arrived since the last one. */
export function announcement(n: number): string {
  if (n <= 0) return "";
  return n === 1 ? "1 new event" : `${n.toLocaleString("en-IN")} new events`;
}

/** contactHref links an external id to the contact page. */
export const contactHref = (externalId: string) => `/contacts/${encodeURIComponent(externalId)}`;
