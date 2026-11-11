// Pure logic of Settings → Automation & limits: effective retention, which changes delete data
// sooner (they need the "what will be deleted" preview before saving), the retry schedule a
// setting gives, range checks, and labels. Mirrors package automation on the server.

import type { AutomationLimit, AutomationRetention, AutomationSettings, RetentionPreviewItem } from "./api";

/** The base retry waits in seconds (30 s .. 1 h), as Pulse always used them. */
export const BASE_BACKOFF = [30, 60, 120, 300, 600, 1800, 3600];

export const WEB_VIEW_NAMES_DEFAULT = ["page_view", "screen_view", "landing_page_viewed"];

export const messageDays = (r: AutomationRetention) => r.message_event_days ?? r.event_days;
export const webViewDays = (r: AutomationRetention) => r.web_view_days ?? r.event_days;

/** daysFor is how long events of one name are kept (0 = forever): its override, else views, else events. */
export function daysFor(r: AutomationRetention, name: string, webNames = WEB_VIEW_NAMES_DEFAULT): number {
  const o = r.event_overrides ?? {};
  if (name in o) return o[name];
  if (webNames.includes(name)) return webViewDays(r);
  return r.event_days;
}

/** shorter: newDays keeps less than oldDays (0 is forever). */
export const shorter = (oldDays: number, newDays: number) => newDays > 0 && (oldDays === 0 || newDays < oldDays);

/**
 * retentionShortened lists what a change deletes sooner than before, in words, e.g.
 * ["Events: forever → 365 days", "page_view: 365 → 90 days"]. Empty: saving deletes nothing new,
 * so no preview is needed.
 */
export function retentionShortened(cur: AutomationRetention, next: AutomationRetention, webNames = WEB_VIEW_NAMES_DEFAULT): string[] {
  const out: string[] = [];
  const add = (label: string, a: number, b: number) => { if (shorter(a, b)) out.push(`${label}: ${formatDays(a)} → ${formatDays(b)}`); };
  add("Events", cur.event_days, next.event_days);
  add("Message events", messageDays(cur), messageDays(next));
  add("Page and screen views", webViewDays(cur), webViewDays(next));
  const names = new Set([...Object.keys(cur.event_overrides ?? {}), ...Object.keys(next.event_overrides ?? {})]);
  for (const n of [...names].sort()) add(n, daysFor(cur, n, webNames), daysFor(next, n, webNames));
  add("Anonymous visitors", cur.anonymous_days, next.anonymous_days);
  add("Audit log", cur.audit_days, next.audit_days);
  add("Import history", cur.import_days, next.import_days);
  add("Webhook deliveries", cur.webhook_delivery_days, next.webhook_delivery_days);
  add("Integration log", cur.integration_log_days, next.integration_log_days);
  add("Test inbox", cur.sandbox_days, next.sandbox_days);
  return [...new Set(out)];
}

export function formatDays(n: number): string {
  if (!n) return "forever";
  if (n % 365 === 0) return n === 365 ? "365 days (1 year)" : `${n} days (${n / 365} years)`;
  return n === 1 ? "1 day" : `${n} days`;
}

/** formatDuration: 30 → "30 s", 3600 → "1 h", 6600 → "1 h 50 min". */
export function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds} s`;
  const h = Math.floor(seconds / 3600), m = Math.floor((seconds % 3600) / 60), s = seconds % 60;
  const parts = [h && `${h} h`, m && `${m} min`, s && `${s} s`].filter(Boolean);
  return parts.join(" ");
}

/** retrySchedule is the wait (seconds) before each retry, like automation.Retries.Schedule. */
export function retrySchedule(attempts: number, maxSeconds: number): number[] {
  const out: number[] = [];
  let last = 0;
  for (let i = 0; i < Math.max(attempts - 1, 0); i++) {
    const w = Math.min(i < BASE_BACKOFF.length ? BASE_BACKOFF[i] : last * 2, maxSeconds > 0 ? maxSeconds : 3600);
    out.push(w);
    last = w;
  }
  return out;
}

/** checkValue returns a problem with value under its limit, or null. */
export function checkValue(limit: AutomationLimit | undefined, value: number, what: string): string | null {
  if (!limit) return null;
  if (!Number.isInteger(value)) return `${what}: a whole number`;
  if (limit.zero && value === 0) return null;
  if (value < limit.min || value > limit.max) return `${what}: ${limit.min} to ${limit.max}${limit.zero ? ", or 0" : ""}`;
  return null;
}

const EVENT_NAME = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;

export type OverrideRow = { name: string; days: number };

export const overridesToRows = (o: Record<string, number> | null): OverrideRow[] =>
  Object.entries(o ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, days]) => ({ name, days }));

/** rowsToOverrides turns edited rows back into the map, or says what is wrong. Empty names are skipped. */
export function rowsToOverrides(rows: OverrideRow[], limit?: AutomationLimit): { value: Record<string, number> | null; error: string | null } {
  const value: Record<string, number> = {};
  for (const r of rows) {
    const name = r.name.trim();
    if (!name) continue;
    if (!EVENT_NAME.test(name)) return { value: null, error: `"${name}" is not an event name` };
    if (name in value) return { value: null, error: `${name} is listed twice` };
    const p = checkValue(limit, r.days, name);
    if (p) return { value: null, error: p };
    value[name] = r.days;
  }
  return { value: Object.keys(value).length ? value : null, error: null };
}

/** validate checks every number against the server's limits; it returns the first problem. */
export function validate(s: AutomationSettings, limits: Record<string, AutomationLimit>): string | null {
  const r = s.retention;
  const checks: [string, number | null, string][] = [
    ["retention.event_days", r.event_days, "Events"],
    ["retention.message_event_days", r.message_event_days, "Message events"],
    ["retention.web_view_days", r.web_view_days, "Page and screen views"],
    ["retention.anonymous_days", r.anonymous_days, "Anonymous visitors"],
    ["retention.audit_days", r.audit_days, "Audit log"],
    ["retention.import_days", r.import_days, "Import history"],
    ["retention.webhook_delivery_days", r.webhook_delivery_days, "Webhook deliveries"],
    ["retention.integration_log_days", r.integration_log_days, "Integration log"],
    ["retention.sandbox_days", r.sandbox_days, "Test inbox"],
    ["retention.live_feed_size", r.live_feed_size, "Live feed size"],
    ["retries.attempts", s.retries.attempts, "Tries"],
    ["retries.max_backoff_seconds", s.retries.max_backoff_seconds, "Longest wait"],
    ["schedules.sweep_minutes", s.schedules.sweep_minutes, "Sweep interval"],
    ["schedules.reach_rebuild_hour", s.schedules.reach_rebuild_hour, "Rebuild hour"],
    ["messages.inbox_expiry_days", s.messages.inbox_expiry_days, "Inbox expiry"],
  ];
  for (const [key, v, what] of checks) {
    if (v === null) continue;
    const p = checkValue(limits[key], v, what);
    if (p) return p;
  }
  for (const [name, days] of Object.entries(r.event_overrides ?? {})) {
    const p = checkValue(limits["retention.event_overrides"], days, name);
    if (p) return p;
  }
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(s.schedules.daily_segment_at)) return "Daily segment time: like 06:00";
  return null;
}

const LOGS: Record<string, string> = {
  audit: "Audit log entries",
  imports: "Finished imports (history only; contacts stay)",
  webhook_deliveries: "Webhook delivery attempts",
  integration_log: "Integration log entries",
  sandbox: "Test inbox messages",
  anonymous: "Anonymous visitors (with their events)",
};

/** previewLabel says what one preview item deletes. */
export function previewLabel(it: RetentionPreviewItem): string {
  if (it.store === "postgres") return LOGS[it.table] ?? it.table;
  if (it.table === "message_events") return "Message events (campaign reports and dashboards)";
  if (it.key === "web_views") return `Page and screen views (${(it.names ?? []).join(", ")})`;
  if (it.not_in || !it.names?.length) return it.names?.length ? `Events of every other name (not ${it.names.join(", ")})` : "Events of every name";
  return `Events named ${it.names.join(", ")}`;
}

/** previewTotal is the rows the preview counted (items that could not be counted are left out). */
export const previewTotal = (items: RetentionPreviewItem[]) => items.reduce((n, it) => n + (it.error ? 0 : it.rows), 0);

/** sameSettings compares two settings, with empty override and limit maps equal to null and key order ignored. */
export function sameSettings(a: AutomationSettings, b: AutomationSettings): boolean {
  const norm = (s: AutomationSettings) => JSON.stringify({ ...s, retention: { ...s.retention, event_overrides: Object.keys(s.retention.event_overrides ?? {}).length ? Object.fromEntries(Object.entries(s.retention.event_overrides ?? {}).sort()) : null }, default_limits: Object.keys(s.default_limits ?? {}).length ? Object.fromEntries(Object.entries(s.default_limits ?? {}).sort()) : null });
  return norm(a) === norm(b);
}

/** dailySegmentDefault is the time and zone a new "refresh daily" segment starts with: the
 * workspace's setting, else 06:00 in the browser's zone. */
export function dailySegmentDefault(s: AutomationSettings | undefined, browserZone: string): { at: string; tz: string } {
  return { at: s?.schedules.daily_segment_at || "06:00", tz: s?.schedules.daily_segment_tz || browserZone };
}
