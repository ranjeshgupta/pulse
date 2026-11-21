// Pure helpers for the Users dashboard (Analyze → Users): date ranges as UTC days, chart scaling,
// breakdown shares and the daily CSV. No React, no DOM: tested in usersDash.test.ts.
import { ApiError, type KeyCount, type UsersDay } from "./api";
import type { Range } from "./insights";

export type Platform = "" | "web" | "android" | "ios" | "other";
export const PLATFORMS: { value: Platform; label: string }[] = [
  { value: "", label: "All" },
  { value: "web", label: "Web" },
  { value: "android", label: "Android" },
  { value: "ios", label: "iOS" },
  { value: "other", label: "Server / other" },
];

export function platformLabel(key: string): string {
  return PLATFORMS.find((p) => p.value !== "" && p.value === key)?.label ?? (key || "(none)");
}

/** sourceLabel: "" is "no UTM source". */
export const sourceLabel = (key: string) => (key === "" ? "(none)" : key);

const DAY_MS = 86_400_000;
export const utcDay = (d: Date) => d.toISOString().slice(0, 10);

/** rangeDays turns the picker's range into the API's UTC days (both included): "Last 30 days" is
 * today and the 29 days before it. */
export function rangeDays(r: Range, now = new Date()): { from: string; to: string } {
  if (r.preset === "custom") return { from: r.from, to: r.to };
  const to = utcDay(Number.isNaN(now.getTime()) ? new Date() : now);
  const n = Number.isFinite(r.preset) ? Math.min(Math.max(Math.floor(r.preset), 1), 3660) : 30; // toISOString throws on an invalid date
  return { from: utcDay(new Date(Date.parse(`${to}T00:00:00Z`) - (n - 1) * DAY_MS)), to };
}

/** source: null = every source (no filter), "" = contacts without a UTM source. */
export function usersPath(days: { from: string; to: string }, platform: Platform, source: string | null): string {
  const q = new URLSearchParams({ from: days.from, to: days.to });
  if (platform) q.set("platform", platform);
  if (source !== null) q.set("source", source);
  return `/v1/analytics/users?${q}`;
}

/** niceMax rounds a chart's top up to 1, 2, 2.5 or 5 times a power of ten (at least 1). */
export function niceMax(n: number): number {
  if (!(n > 0)) return 1;
  const p = 10 ** Math.floor(Math.log10(n));
  for (const m of [1, 2, 2.5, 5, 10]) if (n <= m * p + 1e-9) return Math.max(1, m * p);
  return 10 * p;
}

/** ticks: evenly spaced axis values from 0 to max (parts steps). */
export function ticks(max: number, parts = 4): number[] {
  return Array.from({ length: parts + 1 }, (_, i) => (max * i) / parts);
}

/** compact: 1234 → "1.2K", 2500000 → "2.5M" (axis labels). */
export function compact(n: number): string {
  if (!Number.isFinite(n)) return "–";
  const a = Math.abs(n);
  const fmt = (x: number, s: string) => `${Number(x.toFixed(x < 10 ? 1 : 0))}${s}`;
  if (a >= 1e9) return fmt(n / 1e9, "B");
  if (a >= 1e6) return fmt(n / 1e6, "M");
  if (a >= 1e3) return fmt(n / 1e3, "K");
  return String(Number(n.toFixed(1)));
}

/** linePoints maps values to x,y in a width × height box (y grows downward; 0 at the bottom). */
export function linePoints(values: number[], max: number, width: number, height: number): [number, number][] {
  const n = values.length;
  const top = max > 0 && Number.isFinite(max) ? max : 1;
  // A missing value sits on the baseline: NaN would break the whole SVG path.
  return values.map((v, i) => [n === 1 ? width / 2 : (i / (n - 1)) * width, height - (Math.min(Number.isFinite(v) ? v : 0, top) / top) * height]);
}

/** linePath is an SVG path through the points ("M x y L x y …"). */
export function linePath(values: number[], max: number, width: number, height: number): string {
  return linePoints(values, max, width, height)
    .map(([x, y], i) => `${i === 0 ? "M" : "L"}${round2(x)} ${round2(y)}`)
    .join(" ");
}
const round2 = (x: number) => Math.round(x * 100) / 100;

/** labelIndexes picks up to `count` evenly spaced indexes of n points, always the first and last. */
export function labelIndexes(n: number, count = 5): number[] {
  if (!(n > 0) || !Number.isFinite(n)) return [];
  n = Math.floor(n);
  count = Number.isFinite(count) ? Math.max(2, Math.floor(count)) : 5;
  if (n <= count) return Array.from({ length: n }, (_, i) => i);
  const out = new Set<number>();
  for (let i = 0; i < count; i++) out.add(Math.round((i * (n - 1)) / (count - 1)));
  return [...out];
}

/** shortDay: "2026-11-05" → "5 Nov" (the day as the server counted it, in UTC). */
export function shortDay(day: string): string {
  const d = new Date(`${String(day ?? "").slice(0, 10)}T00:00:00Z`);
  return Number.isNaN(d.getTime()) ? String(day ?? "") : d.toLocaleDateString("en-IN", { day: "numeric", month: "short", timeZone: "UTC" });
}

export function longDay(day: string): string {
  const d = new Date(`${String(day ?? "").slice(0, 10)}T00:00:00Z`);
  return Number.isNaN(d.getTime()) ? String(day ?? "") : d.toLocaleDateString("en-IN", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
}

export const share = (n: number, total: number) => (total > 0 ? Math.min(1, Math.max(0, n / total)) : 0);

export interface BreakdownRow { key: string; label: string; count: number; share: number }

/** breakdown sorts by count (then label) and gives each value's share of the active users. */
export function breakdown(rows: KeyCount[], active: number, label: (k: string) => string): BreakdownRow[] {
  return rows
    .map((r) => ({ key: r.key, label: label(r.key), count: r.count, share: share(r.count, active) }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));
}

/** sourceOptions: the sources to offer in the filter, most active first; the current choice stays
 * listed even when the filtered answer no longer has it. */
export function sourceOptions(bySource: KeyCount[], current: string | null): string[] {
  const keys = [...bySource].sort((a, b) => b.count - a.count || a.key.localeCompare(b.key)).map((r) => r.key);
  if (current !== null && !keys.includes(current)) keys.unshift(current);
  return keys;
}

/** newReturningMax: the chart top for stacked new + returning bars. */
export const newReturningMax = (days: UsersDay[]) => niceMax(Math.max(0, ...days.map((d) => d.new + d.returning)));

/** linesMax: the chart top for the chosen lines. */
export function linesMax(days: UsersDay[], lines: ("dau" | "wau" | "mau")[]): number {
  return niceMax(Math.max(0, ...days.flatMap((d) => lines.map((l) => d[l]))));
}

/** usersCsvRows: the daily rows for the CSV export (stickiness as a 0–1 ratio, 4 decimals). */
export function usersCsvRows(days: UsersDay[]): (string | number)[][] {
  return [
    ["day", "dau", "wau", "mau", "stickiness", "new", "returning"],
    ...days.map((d) => [d.day.slice(0, 10), d.dau, d.wau, d.mau, Number.isFinite(d.stickiness) ? Number(d.stickiness.toFixed(4)) : "", d.new, d.returning]),
  ];
}

/** csvName: the export's name, with the filters in it. */
export function usersCsvName(from: string, to: string, platform: Platform, source: string | null): string {
  const bits = [`Users ${from} to ${to}`];
  if (platform) bits.push(platformLabel(platform));
  if (source !== null) bits.push(`source ${sourceLabel(source)}`);
  return bits.join(" ");
}

/** updatedAt: when the server computed the answer ("4:05 pm"). */
export const updatedAt = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString("en-IN", { hour: "numeric", minute: "2-digit" });
};

/** retryUnlessLimited: react-query retry rule — don't retry rate limits (429) or bad requests (400). */
export const retryUnlessLimited = (n: number, err: unknown) => !(err instanceof ApiError && (err.status === 429 || err.status === 400)) && n < 2;
