// Pure helpers of the Cohorts (retention) page: UTC period arithmetic, which cohorts to show,
// labels, and the heat-map shading of the retention triangle. No React, no DOM.
import { MAX_COHORTS, MAX_PERIODS, type CohortSpec, type Period } from "./api";

const DAY_MS = 86_400_000;
/** The longest range of cohort starts the server accepts (analytics.MaxRange). */
export const MAX_RANGE_DAYS = 366;
/** Most "last N" cohorts per period that always fit in MAX_RANGE_DAYS (and MAX_COHORTS). */
export const MAX_RECENT: Record<Period, number> = { day: MAX_COHORTS, week: 52, month: 12 };

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const UNIT: Record<Period, string> = { day: "Day", week: "Week", month: "Month" };

/** periodStart truncates t to the start of its period in UTC; weeks start on Monday. */
export function periodStart(t: Date, period: Period): Date {
  const d = Date.UTC(t.getUTCFullYear(), t.getUTCMonth(), t.getUTCDate());
  if (period === "month") return new Date(Date.UTC(t.getUTCFullYear(), t.getUTCMonth(), 1));
  if (period === "week") return new Date(d - ((t.getUTCDay() + 6) % 7) * DAY_MS);
  return new Date(d);
}

/** addPeriods moves t by n periods (n may be negative), in UTC. */
export function addPeriods(t: Date, period: Period, n: number): Date {
  if (period === "month") {
    return new Date(Date.UTC(t.getUTCFullYear(), t.getUTCMonth() + n, t.getUTCDate(), t.getUTCHours(), t.getUTCMinutes()));
  }
  return new Date(t.getTime() + n * (period === "week" ? 7 : 1) * DAY_MS);
}

/** lastBounds is the range showing the last n cohorts: from the start of the period n-1 periods
 * before the current one, to now rounded down to the minute (so the request stays the same while
 * you edit). */
export function lastBounds(period: Period, n: number, now = new Date()): { from: string; to: string } {
  const t = Number.isNaN(now.getTime()) ? Date.now() : now.getTime();
  const to = new Date(Math.floor(t / 60_000) * 60_000);
  const count = Number.isFinite(n) ? Math.min(Math.max(Math.round(n), 1), MAX_COHORTS) : 1; // toISOString throws on an invalid date
  const from = addPeriods(periodStart(to, period), period, -(count - 1));
  return { from: from.toISOString(), to: to.toISOString() };
}

/** cohortCount is how many cohorts the range [from, to) produces: the period starts covering it. */
export function cohortCount(period: Period, from: string, to: string): number {
  const end = new Date(to).getTime();
  let n = 0;
  for (let b = periodStart(new Date(from), period); b.getTime() < end && n <= MAX_COHORTS; b = addPeriods(b, period, 1)) n++;
  return n;
}

/** rangeIssue explains why the server would refuse this range of cohort starts, if it would. */
export function rangeIssue(period: Period, from: string, to: string): string | null {
  const span = new Date(to).getTime() - new Date(from).getTime();
  if (!(span > 0)) return "Pick a range that ends after it starts.";
  if (span > MAX_RANGE_DAYS * DAY_MS) return "Pick a range of a year or less.";
  if (cohortCount(period, from, to) > MAX_COHORTS) return `That range has more than ${MAX_COHORTS} ${unit(period, 2).toLowerCase()}: pick a shorter one or a longer period.`;
  return null;
}

/** periodsIssue checks the number of periods to follow. */
export function periodsIssue(period: Period, n: number): string | null {
  const max = MAX_PERIODS[period];
  return Number.isInteger(n) && n >= 1 && n <= max ? null : `Follow 1 to ${max} ${unit(period, 2).toLowerCase()}.`;
}

/** clampInt keeps n a whole number in [1, max] (1 when it isn't a number). */
export const clampInt = (n: number, max: number) => (Number.isFinite(n) ? Math.min(max, Math.max(1, Math.round(n))) : 1);

/** unit names the period: "Week", "Weeks". */
export const unit = (period: Period, n = 1) => UNIT[period] + (n === 1 ? "" : "s");

/** periodsText: "8 weeks", "1 day". */
export const periodsText = (period: Period, n: number) => `${n} ${unit(period, n).toLowerCase()}`;

/** columnLabel is the header of retention column i: "Day 0", "Week 3". */
export const columnLabel = (period: Period, i: number) => `${UNIT[period]} ${i}`;

/** cohortLabel formats a cohort's start in UTC: "Mon 9 Nov", "Week of 9 Nov", "Nov 2026". */
export function cohortLabel(iso: string, period: Period): string {
  const d = new Date(iso);
  const day = `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
  if (period === "month") return `${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
  if (period === "week") return `Week of ${day}`;
  return `${WEEKDAYS[d.getUTCDay()]} ${day}`;
}

/** cohortTitle is the cohort's full span, for a tooltip: "9 Nov 2026 – 15 Nov 2026". */
export function cohortTitle(iso: string, period: Period): string {
  const d = new Date(iso);
  const full = (x: Date) => `${x.getUTCDate()} ${MONTHS[x.getUTCMonth()]} ${x.getUTCFullYear()}`;
  if (period === "day") return full(d);
  return `${full(d)} – ${full(new Date(addPeriods(d, period, 1).getTime() - DAY_MS))}`;
}

// ---- describing a saved report ----

/** formedBy: "First seen", "First did signup". */
export const formedBy = (s: CohortSpec) => (s.by === "event" ? `First did ${s.event ?? "an event"}${s.where?.length ? " (filtered)" : ""}` : "First seen");

/** comingBack: "Any activity", "Did purchase", "Did purchase (filtered)". */
export const comingBack = (s: CohortSpec) => (s.return.event ? `Did ${s.return.event}${s.return.where?.length ? " (filtered)" : ""}` : "Any activity");

// ---- cells ----

export type Cell =
  | { kind: "pending" } // the period hasn't started yet
  | { kind: "empty" } // nobody in the cohort, so no share
  | { kind: "value"; share: number; count?: number };

/** cell is what one retention cell shows. */
export function cell(returned: number | null | undefined, size: number): Cell {
  if (returned === null || returned === undefined) return { kind: "pending" };
  if (size <= 0) return { kind: "empty" };
  return { kind: "value", share: returned / size, count: returned };
}

/** averageCell is a cell of the "All cohorts" row (shares only). */
export const averageCell = (share: number | null | undefined): Cell =>
  share === null || share === undefined ? { kind: "pending" } : { kind: "value", share };

// ---- heat map ----

const WHITE: RGB = [255, 255, 255];
const COBALT: RGB = [39, 71, 214]; // --cobalt
const INK = "#152238"; // --ink
type RGB = [number, number, number];

const channel = (c: number) => {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
};
const luminance = ([r, g, b]: RGB) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
const hexRGB = (hex: string): RGB => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as RGB;
const hex = (c: RGB) => "#" + c.map((x) => Math.round(x).toString(16).padStart(2, "0")).join("");

/** contrast is the WCAG contrast ratio of two #rrggbb colours. */
export function contrast(a: string, b: string): number {
  const [la, lb] = [luminance(hexRGB(a)), luminance(hexRGB(b))];
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

const mix = (t: number): RGB => WHITE.map((w, i) => w + (COBALT[i] - w) * t) as RGB;

// The lightest shade white text is readable on (4.5:1); shades darker than ink text allows but
// lighter than this are snapped to it.
const WHITE_TEXT_FROM = (() => {
  let t = 0;
  while (t < 1 && contrast(hex(mix(t)), "#ffffff") < 4.5) t += 0.001;
  return Math.min(1, t);
})();

/** heat shades a 0-1 share from white to cobalt (square root, so small shares still show), and
 * picks ink or white text, whichever keeps the number readable (WCAG AA, 4.5:1). Mid shades where
 * neither would be are darkened just enough for white text, so shading still grows with the share. */
export function heat(share: number): { background: string; color: string } {
  const t = Math.sqrt(Math.min(1, Math.max(0, Number.isFinite(share) ? share : 0)));
  const bg = hex(mix(t));
  if (contrast(bg, INK) >= 4.5) return { background: bg, color: INK };
  return { background: hex(mix(Math.max(t, WHITE_TEXT_FROM))), color: "#ffffff" };
}
