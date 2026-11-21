// The Overview's summary (GET /v1/stats/summary, internal/api/overview.go): its types, which
// sections a role sees (the same rule as the server's summaryFor, so the page can show the right
// placeholders before the answer arrives), the change against the 7 days before, and each recent
// campaign's key rate. Pure, so vitest covers it.

import { can, type MessageCounts, type Perms } from "./api";

export interface BroadcastBrief {
  id: number; name: string; channel: string; status: string; repeat: string;
  /** Next run (coming up), or when the current or latest run started. */
  at: string;
  run?: number;
  counts?: MessageCounts;
}

export interface OverviewSummary {
  computed_at: string;
  today: string; week_from: string; prev_from: string;
  audience?: { new?: { today: number; week: number; prev_week: number }; unsubscribed?: { week: number; prev: number } };
  campaigns?: { sending: BroadcastBrief[]; next: BroadcastBrief[]; recent?: BroadcastBrief[] };
  revenue?: { event: string; currency: string; week: RevenueTotals; prev: RevenueTotals };
  attention: Attention[];
  unavailable?: string[];
}
export interface RevenueTotals { orders: number; revenue: number; buyers: number }
export interface Attention { kind: string; level: "problem" | "warning"; text: string; link?: string; link_text?: string }

/** The sections a role sees (internal/api/overview.go summaryFor). */
export function overviewSections(perms: Perms | undefined) {
  const reports = can(perms, "reports", "view");
  const broadcasts = can(perms, "broadcasts", "view");
  return {
    audience: can(perms, "contacts", "view"),
    campaigns: broadcasts,
    recent: broadcasts && reports,
    revenue: reports,
  };
}

export interface Change { text: string; dir: "up" | "down" | "flat"; title: string }

/** change compares the last 7 days with the 7 before: "+25%", "−40%", "same", or "new" when there
 * was nothing before. */
export function change(now: number, before: number): Change {
  const title = `${before.toLocaleString("en-IN")} in the 7 days before`;
  if (now === before) return { text: "same as the week before", dir: "flat", title };
  if (before === 0) return { text: "none the week before", dir: "up", title };
  const p = ((now - before) / before) * 100;
  const r = Math.abs(p) < 10 ? Math.round(Math.abs(p) * 10) / 10 : Math.round(Math.abs(p));
  if (r === 0) return { text: "about the same as the week before", dir: "flat", title };
  return { text: `${p > 0 ? "+" : "−"}${r}% on the week before`, dir: p > 0 ? "up" : "down", title };
}

/** keyRate is the number that says how a sent campaign did: opens for email, clicks otherwise,
 * as a share of the messages sent. Null before anything was sent. */
export function keyRate(channel: string, c: MessageCounts | undefined): { label: string; value: number } | null {
  if (!c || c.sent <= 0) return null;
  return channel === "email" ? { label: "opened", value: c.opened / c.sent } : { label: "clicked", value: c.clicked / c.sent };
}

/** percent formats a 0-1 share like the dashboards: "24%", "3.5%", "0%". */
export function percent(x: number): string {
  if (!Number.isFinite(x)) return "–";
  const p = x * 100;
  if (p === 0) return "0%";
  return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`;
}

/** whenText says when a campaign runs or ran, relative to now: "today 14:30", "tomorrow 09:00",
 * "Tue 17 Nov, 09:00". Times are the viewer's local time. */
export function whenText(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return ""; // not "Invalid Date"
  const time = d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const diff = Math.round((day(d) - day(now)) / 86_400_000);
  if (diff === 0) return `today ${time}`;
  if (diff === 1) return `tomorrow ${time}`;
  if (diff === -1) return `yesterday ${time}`;
  return `${d.toLocaleDateString("en-GB", { weekday: "short", day: "numeric", month: "short" })}, ${time}`;
}

/** REPEAT_WORDS describe a repeating campaign. */
export const REPEAT_WORDS: Record<string, string> = { daily: "every day", weekly: "every week", monthly: "every month" };
