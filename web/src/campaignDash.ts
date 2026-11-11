// Campaign performance dashboard (M4): rates, sorting, totals, failure reasons grouped for the bars,
// the request's query string and the CSV export. Pure, so vitest covers it.

import type { CampaignDashRow, MessageCounts, ReasonCount, ReasonKind, SkipGroup } from "./api";
import { CATEGORIES } from "./campaigns";

export const DASH_CATEGORIES = [{ key: "", label: "All" }, ...CATEGORIES.map((c) => ({ key: c.key, label: c.label }))];

// ---- request ----

export interface DashFilters { from: string; to: string; category: string; channel: string; tag: string; q: string }

/** dashQuery builds the query string of GET /v1/analytics/campaigns, leaving out empty filters. */
export function dashQuery(f: DashFilters): string {
  const p = new URLSearchParams({ from: f.from, to: f.to });
  if (f.category) p.set("category", f.category);
  if (f.channel) p.set("channel", f.channel);
  if (f.tag) p.set("tag", f.tag);
  const q = f.q.trim();
  if (q) p.set("q", q);
  return p.toString();
}

// ---- rates ----

/** ratio is n / of, or null when there is nothing to divide by. */
export const ratio = (n: number, of: number): number | null => (of > 0 ? n / of : null);

/** rate formats a 0-1 share: "24%", "3.5%", "0%"; null (a zero denominator) is "–". */
export function rate(x: number | null): string {
  if (x === null || !isFinite(x)) return "–";
  const p = x * 100;
  if (p === 0) return "0%";
  return p < 10 ? `${p.toFixed(1)}%` : `${Math.round(p)}%`;
}

/** deliveryReported: every channel of the campaign has a default provider that reports deliveries
 * (channels missing from the map count as reported). */
export function deliveryReported(channels: string[], reports: Record<string, boolean>): boolean {
  return channels.every((c) => reports[c] !== false);
}

export interface Rates {
  delivery: number | null; open: number | null; click: number | null; bounce: number | null;
  complaint: number | null; unsubscribe: number | null; conversion: number | null;
  /** What the open rate is a share of. */
  openOf: "delivered" | "sent";
  /** False when the provider doesn't report deliveries: delivery is then null, not 0. */
  deliveryReported: boolean;
}

/** rates computes a campaign's (or the totals') rates. Open rate is of delivered when deliveries are
 * reported, else of sent; the rest are of sent; conversion is buyers / sent. */
export function rates(c: MessageCounts, buyers: number, reported: boolean): Rates {
  return {
    delivery: reported ? ratio(c.delivered, c.sent) : null,
    open: reported ? ratio(c.opened, c.delivered) : ratio(c.opened, c.sent),
    click: ratio(c.clicked, c.sent),
    bounce: ratio(c.bounced, c.sent),
    complaint: ratio(c.complained, c.sent),
    unsubscribe: ratio(c.unsubscribed, c.sent),
    conversion: ratio(buyers, c.sent),
    openOf: reported ? "delivered" : "sent",
    deliveryReported: reported,
  };
}

export const rowRates = (r: CampaignDashRow, reports: Record<string, boolean>) => rates(r.counts, r.buyers, deliveryReported(r.channels, reports));

// ---- sorting ----

export type SortKey =
  | "name" | "category" | "sent" | "delivery" | "open" | "click" | "downloaded" | "buyers" | "revenue"
  | "unsubscribed" | "complained" | "bounced" | "failed";
export type SortDir = "asc" | "desc";
export interface Sort { key: SortKey; dir: SortDir }

export const DEFAULT_SORT: Sort = { key: "sent", dir: "desc" };

/** sortValue is what a column sorts by; null (an unknown rate) always sorts last. */
export function sortValue(r: CampaignDashRow, key: SortKey, reports: Record<string, boolean>): string | number | null {
  switch (key) {
    case "name": return r.name.toLowerCase();
    case "category": return r.category;
    case "delivery": return rowRates(r, reports).delivery;
    case "open": return rowRates(r, reports).open;
    case "click": return rowRates(r, reports).click;
    case "buyers": return r.buyers;
    case "revenue": return r.revenue;
    default: return r.counts[key];
  }
}

/** sortRows returns a sorted copy; ties keep the more-sent campaign first, then the name. */
export function sortRows(rows: CampaignDashRow[], sort: Sort, reports: Record<string, boolean>): CampaignDashRow[] {
  const sign = sort.dir === "asc" ? 1 : -1;
  return rows
    .map((r) => ({ r, v: sortValue(r, sort.key, reports) }))
    .sort((a, b) => {
      if (a.v === null && b.v !== null) return 1;
      if (b.v === null && a.v !== null) return -1;
      if (a.v !== null && b.v !== null && a.v !== b.v) return (a.v < b.v ? -1 : 1) * sign;
      return b.r.counts.sent - a.r.counts.sent || a.r.name.localeCompare(b.r.name);
    })
    .map((x) => x.r);
}

/** nextSort: clicking the sorted column flips it; another column starts descending (names ascending). */
export function nextSort(cur: Sort, key: SortKey): Sort {
  if (cur.key === key) return { key, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { key, dir: key === "name" || key === "category" ? "asc" : "desc" };
}

// ---- totals ----

export interface DashTotals { counts: MessageCounts; revenue: number; buyers: number; campaigns: number }

const ZERO: MessageCounts = {
  sent: 0, delivered: 0, read: 0, failed: 0, fallback: 0, suppressed: 0, capped: 0, deferred: 0, opened: 0, clicked: 0, unsubscribed: 0,
  converted: 0, bounced: 0, complained: 0, downloaded: 0,
};

/** totals adds up the rows (each count is unique contacts per campaign, so a contact in two campaigns counts twice). */
export function totals(rows: CampaignDashRow[]): DashTotals {
  const counts = { ...ZERO };
  let revenue = 0;
  let buyers = 0;
  for (const r of rows) {
    for (const k of Object.keys(counts) as (keyof MessageCounts)[]) counts[k] += r.counts[k] ?? 0;
    revenue += r.revenue;
    buyers += r.buyers;
  }
  return { counts, revenue, buyers, campaigns: rows.length };
}

/** totalsReported: deliveries are reported for every channel of every row (so the overall delivery rate means something). */
export const totalsReported = (rows: CampaignDashRow[], reports: Record<string, boolean>) => rows.every((r) => deliveryReported(r.channels, reports));

// ---- failure reasons ----

export type ReasonSection = "skipped" | "bounced" | "failed";

export const SECTION_LABELS: Record<ReasonSection, { label: string; hint: string }> = {
  skipped: { label: "Skipped", hint: "Not sent: no consent, not reachable, over the frequency cap, a missing value, or past the send deadline." },
  bounced: { label: "Bounced or complained", hint: "Sent, then bounced by the receiving server or marked as spam." },
  failed: { label: "Failed when tried", hint: "The provider refused or errored when the message was sent." },
};

export const GROUP_LABELS: Record<SkipGroup, string> = {
  consent: "Consent", reachability: "Reachability", cap: "Frequency cap", missing_value: "Missing value", deadline: "Deadline",
};

export const KIND_LABELS: Record<ReasonKind, string> = {
  failed: "Failed", bounced: "Bounced", complained: "Complaint", suppressed: "Opted out", capped: "Over limit",
};

/** sectionOf places a reason: anything with a skip group (or opted out / capped) was skipped; bounces and
 * complaints together; the rest failed when tried. */
export function sectionOf(r: ReasonCount): ReasonSection {
  if (r.group || r.kind === "suppressed" || r.kind === "capped") return "skipped";
  if (r.kind === "bounced" || r.kind === "complained") return "bounced";
  return "failed";
}

export interface ReasonGroup { section: ReasonSection; label: string; hint: string; total: number; reasons: ReasonCount[] }

/** groupReasons splits reasons into the three sections (empty ones left out), most contacts first;
 * max is the largest single reason, to scale the bars. */
export function groupReasons(reasons: ReasonCount[]): { groups: ReasonGroup[]; max: number } {
  const order: ReasonSection[] = ["skipped", "bounced", "failed"];
  const by: Record<ReasonSection, ReasonCount[]> = { skipped: [], bounced: [], failed: [] };
  let max = 0;
  for (const r of reasons) {
    if (r.contacts <= 0) continue;
    by[sectionOf(r)].push(r);
    max = Math.max(max, r.contacts);
  }
  const groups = order
    .filter((s) => by[s].length > 0)
    .map((s) => ({
      section: s, ...SECTION_LABELS[s],
      total: by[s].reduce((n, r) => n + r.contacts, 0),
      reasons: [...by[s]].sort((a, b) => b.contacts - a.contacts || a.label.localeCompare(b.label)),
    }));
  return { groups, max };
}

/** reasonLabel: the label, or the code when the server sent none. */
export const reasonLabel = (r: ReasonCount) => r.label || r.code || "Unknown";

/** barWidth: a reason's bar as a percentage of the largest one (at least 1% so a bar shows). */
export const barWidth = (n: number, max: number) => (max > 0 && n > 0 ? Math.max(1, Math.round((n / max) * 1000) / 10) : 0);

// ---- links and CSV ----

export const campaignHref = (r: Pick<CampaignDashRow, "kind" | "id">) => (r.kind === "broadcast" ? `/broadcasts/${r.id}` : `/journeys/${r.id}`);
export const categoryLabel = (c: string) => CATEGORIES.find((x) => x.key === c)?.label ?? c;

export const CSV_HEADER = [
  "Campaign", "Kind", "Type", "Channels", "Status", "Tags", "Sent", "Delivered", "Delivery rate", "Opened", "Open rate", "Open rate of",
  "Clicked", "Click rate", "Downloads", "Buyers", "Conversion rate", "Revenue", "Currency", "Unsubscribes", "Complaints", "Bounces", "Failures",
];

/** csvRows: the header and one row per campaign, numbers as numbers and rates as shown ("–" when unknown). */
export function csvRows(rows: CampaignDashRow[], reports: Record<string, boolean>, currency: string): (string | number)[][] {
  return [
    CSV_HEADER,
    ...rows.map((r) => {
      const x = rowRates(r, reports);
      const c = r.counts;
      return [
        r.name, r.kind, categoryLabel(r.category), r.channels.join(" "), r.status, r.tags.join(" | "),
        c.sent, x.deliveryReported ? c.delivered : "", x.deliveryReported ? rate(x.delivery) : "not reported", c.opened, rate(x.open), x.openOf,
        c.clicked, rate(x.click), c.downloaded, r.buyers, rate(x.conversion), r.revenue, currency, c.unsubscribed, c.complained, c.bounced, c.failed,
      ];
    }),
  ];
}
