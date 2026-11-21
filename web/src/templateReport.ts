// Template reports (batch O4): pure helpers for the Report tab — which figures fit the channel,
// the series as one row per bucket, the heatmap's colour steps, and the best time to send.
import type { MessageCounts } from "./api";
import type { CountKey } from "./reports";

export interface BucketCount { t: string; kind: string; count: number }
export interface TemplateReportData {
  totals: MessageCounts;
  series: BucketCount[];
  heatmap: number[][]; // [Mon..Sun][0..23]
  by_campaign: { source: "broadcast" | "journey" | "other"; source_id: number; counts: MessageCounts }[];
  by_channel: Record<string, MessageCounts>;
  by_provider: Record<string, MessageCounts>;
  reasons: { kind: string; code: string; label: string; contacts: number }[];
  links: { url: string; contacts: number; clicks: number }[];
}
export interface TemplateReportResponse {
  template: { id: number; name: string; channel: string };
  from: string; to: string; tz: string; bucket: "day" | "hour";
  report: TemplateReportData;
}

/** The figures that make sense for a template's channel. */
export function templateKeys(channel: string): CountKey[] {
  const keys: CountKey[] = ["sent", "delivered", "failed"];
  if (channel === "email") keys.push("bounced", "complained", "opened", "clicked", "unsubscribed");
  if (channel === "whatsapp" || channel === "rcs") keys.push("read", "fallback");
  if (channel === "push" || channel === "inbox") keys.push("clicked");
  return keys;
}

/** The lines the chart can draw for a channel, with their colours (categorical, colour-blind safe). */
export const SERIES_COLORS: Record<string, string> = {
  sent: "#3a5ccc", delivered: "#1d9a6c", failed: "#d64545", bounced: "#e8890c", complained: "#9b3fb5",
  opened: "#0f9bd7", clicked: "#2b2f8f", read: "#4aa84a", unsubscribed: "#8a8f99",
};
export function seriesKinds(channel: string): string[] {
  return templateKeys(channel).filter((k) => k in SERIES_COLORS) as string[];
}

/** buckets lays the series out as one row per bucket (every bucket of the period, zeros filled). */
export function buckets(series: BucketCount[], from: string, to: string, hourly: boolean): { t: string; values: Record<string, number> }[] {
  const by = new Map<string, Record<string, number>>();
  for (const p of series) {
    const row = by.get(p.t) ?? {};
    row[p.kind] = p.count;
    by.set(p.t, row);
  }
  const out: { t: string; values: Record<string, number> }[] = [];
  const start = new Date(`${from}T00:00:00Z`);
  const end = new Date(`${to}T00:00:00Z`);
  for (let d = start; d <= end; d = new Date(d.getTime() + 86_400_000)) {
    const day = d.toISOString().slice(0, 10);
    if (!hourly) { out.push({ t: day, values: by.get(day) ?? {} }); continue; }
    for (let h = 0; h < 24; h++) {
      const t = `${day} ${String(h).padStart(2, "0")}:00`;
      out.push({ t, values: by.get(t) ?? {} });
    }
  }
  return out;
}

/** heatLevel puts a heatmap cell on a 0–5 scale of the busiest cell. */
export function heatLevel(n: number, max: number): number {
  if (!n || !max) return 0;
  return Math.max(1, Math.min(5, Math.ceil((n / max) * 5)));
}

export const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

/** bestTime names the weekday and hour with the most engagement, or null without any. */
export function bestTime(heat: number[][]): { day: string; hour: number; count: number } | null {
  let best: { day: string; hour: number; count: number } | null = null;
  heat.forEach((row, d) => row.forEach((n, h) => { if (n > 0 && (!best || n > best.count)) best = { day: WEEKDAYS[d], hour: h, count: n }; }));
  return best;
}

/** hourLabel: 0 → "12 am", 13 → "1 pm". */
export const hourLabel = (h: number) => `${h % 12 || 12} ${h < 12 ? "am" : "pm"}`;
