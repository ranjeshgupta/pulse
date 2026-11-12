// Pure helpers for the Revenue (attribution) page: credit labels and links, shares, unattributed
// revenue, conversion formatting, model descriptions and settings validation. No React or DOM here.
import type { AttributionModel, Credit, RevenueSettings } from "./api";

export const EVENT_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;
export const PROP_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;
export const CURRENCY_RE = /^[a-zA-Z]{3}$/;

export const MIN_WINDOW = 1;
export const MAX_WINDOW = 90;
export const DEFAULT_WINDOW = 7;

export const DEFAULT_SETTINGS: RevenueSettings = { event: "purchase", amount_prop: "amount", currency: "" };

const days = (n: number) => `${n} ${n === 1 ? "day" : "days"}`;

export const MODELS: { model: AttributionModel; label: string }[] = [
  { model: "last_touch", label: "Last touch" },
  { model: "first_touch", label: "First touch" },
  { model: "linear", label: "Linear" },
];

/** modelDescription explains how a model shares out a purchase's revenue. */
export function modelDescription(model: AttributionModel, windowDays: number): string {
  const w = days(windowDays);
  switch (model) {
    case "last_touch": return `The latest touch in the ${w} before a purchase gets all of its revenue.`;
    case "first_touch": return `The earliest touch in the ${w} before a purchase gets all of its revenue.`;
    case "linear": return `Every source touched in the ${w} before a purchase gets an equal share of its revenue.`;
  }
}

export function windowProblem(n: number): string | null {
  return Number.isInteger(n) && n >= MIN_WINDOW && n <= MAX_WINDOW ? null : `The attribution window is ${MIN_WINDOW} to ${MAX_WINDOW} days.`;
}

// ---- credits ----

export const KIND_LABELS: Record<Credit["kind"], string> = { broadcast: "One-time campaign", journey: "Journey", utm: "UTM campaign" };

/** creditLabel names a credit: the message name ("Broadcast 12" without one) or the UTM campaign. */
export function creditLabel(c: Credit): string {
  const name = c.name?.trim();
  if (c.kind === "utm") return c.campaign?.trim() || name || "(no campaign)";
  if (name) return name;
  return c.id ? `${KIND_LABELS[c.kind]} ${c.id}` : KIND_LABELS[c.kind];
}

/** creditLink is the page of the broadcast or journey, if there is one. */
export function creditLink(c: Credit): string | undefined {
  if (!c.id) return undefined;
  if (c.kind === "broadcast") return `/broadcasts/${c.id}`;
  if (c.kind === "journey") return `/journeys/${c.id}`;
  return undefined;
}

/** sourceMedium formats a UTM credit's source and medium: "google / cpc". */
export function sourceMedium(c: Credit): string {
  const s = c.source?.trim();
  const m = c.medium?.trim();
  if (!s && !m) return "–";
  return `${s || "(no source)"} / ${m || "(no medium)"}`;
}

/** creditKey is a stable React key for a credit row. */
export function creditKey(c: Credit): string {
  return c.kind === "utm" ? `utm:${c.source ?? ""}|${c.medium ?? ""}|${c.campaign ?? ""}` : `${c.kind}:${c.id ?? c.name ?? ""}`;
}

// ---- numbers ----

/** round2 drops floating-point noise from sums of money. */
const round2 = (n: number) => Math.round(n * 100) / 100;

/** share is part / total as a 0-1 fraction (0 when there is no total), clamped to 0..1. */
export function share(part: number, total: number): number {
  if (!total || !Number.isFinite(part) || !Number.isFinite(total)) return 0;
  return Math.min(1, Math.max(0, part / total));
}

/** unattributed is what is left of total once attributed is taken out, never below 0. */
export function unattributed(total: number, attributed: number): number {
  return Math.max(0, round2(total - attributed));
}

/** unattributedOrders: orders minus attributed conversions (fractional with the linear model). */
export function unattributedOrders(orders: number, conversions: number): number {
  return Math.max(0, Math.round((orders - conversions) * 10) / 10);
}

/** averageOrder is revenue per order, 0 when there are no orders. */
export function averageOrder(revenue: number, orders: number): number {
  return orders > 0 ? round2(revenue / orders) : 0;
}

/** formatConversions shows whole numbers as counts and fractional ones (linear model) with one decimal. */
export function formatConversions(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0";
  const r = Math.round(n * 10) / 10;
  if (r === 0) return "<0.1";
  if (Number.isInteger(r)) return r.toLocaleString("en-IN");
  return r.toLocaleString("en-IN", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

// ---- settings ----

/** normalizeSettings trims the fields and upper-cases the currency, as the server does. */
export function normalizeSettings(s: RevenueSettings): RevenueSettings {
  return { event: s.event.trim(), amount_prop: s.amount_prop.trim(), currency: s.currency.trim().toUpperCase() };
}

/** settingsProblems lists what the server would reject in the revenue settings. */
export function settingsProblems(s: RevenueSettings): string[] {
  const n = normalizeSettings(s);
  const out: string[] = [];
  if (!EVENT_RE.test(n.event)) out.push("Enter the revenue event name, e.g. purchase (letters, digits, _ . : -, starting with a letter).");
  if (!PROP_RE.test(n.amount_prop)) out.push("Enter the amount property, e.g. amount (letters, digits and _).");
  if (n.currency && !CURRENCY_RE.test(n.currency)) out.push("The currency is a 3-letter code such as INR or USD, or leave it empty.");
  return out;
}

export const sameSettings = (a: RevenueSettings, b: RevenueSettings) => {
  const x = normalizeSettings(a);
  const y = normalizeSettings(b);
  return x.event === y.event && x.amount_prop === y.amount_prop && x.currency === y.currency;
};
