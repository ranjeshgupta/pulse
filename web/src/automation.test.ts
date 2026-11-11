import { describe, expect, it } from "vitest";
import type { AutomationRetention, AutomationSettings } from "./api";
import {
  checkValue, dailySegmentDefault, daysFor, formatDays, formatDuration, previewLabel, previewTotal, retentionShortened, retrySchedule,
  rowsToOverrides, overridesToRows, sameSettings, validate,
} from "./automation";

const defaults = (): AutomationSettings => ({
  retention: {
    event_days: 0, message_event_days: null, web_view_days: null, event_overrides: null, anonymous_days: 0, audit_days: 365,
    import_days: 0, webhook_delivery_days: 7, integration_log_days: 7, sandbox_days: 0, live_feed_size: 1000,
  },
  retries: { attempts: 8, max_backoff_seconds: 3600 },
  schedules: { sweep_minutes: 0, reach_rebuild_hour: -1, daily_segment_at: "06:00", daily_segment_tz: "" },
  messages: { inbox_expiry_days: 30 },
  default_limits: null,
});
const limits = {
  "retention.event_days": { min: 30, max: 3650, zero: true },
  "retention.event_overrides": { min: 7, max: 3650, zero: true },
  "retention.live_feed_size": { min: 100, max: 10000, zero: false },
  "retries.attempts": { min: 1, max: 20, zero: false },
};
const ret = (p: Partial<AutomationRetention>): AutomationRetention => ({ ...defaults().retention, ...p });

describe("retention", () => {
  it("per-name settings win over views and events", () => {
    const r = ret({ event_days: 365, web_view_days: 30, event_overrides: { page_view: 90, purchase: 0 } });
    expect(daysFor(r, "page_view")).toBe(90);
    expect(daysFor(r, "screen_view")).toBe(30);
    expect(daysFor(r, "purchase")).toBe(0);
    expect(daysFor(r, "signup")).toBe(365);
    expect(daysFor(ret({ event_days: 200 }), "screen_view")).toBe(200); // views follow events by default
  });
  it("lists only what gets shorter", () => {
    const cur = ret({ event_days: 365, event_overrides: { purchase: 0 } });
    expect(retentionShortened(cur, cur)).toEqual([]);
    expect(retentionShortened(cur, { ...cur, event_days: 400 })).toEqual([]);
    expect(retentionShortened(cur, { ...cur, event_overrides: { purchase: 0, page_view: 90 } })).toEqual(["page_view: 365 days (1 year) → 90 days"]);
    expect(retentionShortened(cur, { ...cur, event_overrides: null })).toEqual(["purchase: forever → 365 days (1 year)"]);
    expect(retentionShortened(cur, { ...cur, audit_days: 90, anonymous_days: 30 })).toEqual([
      "Anonymous visitors: forever → 30 days", "Audit log: 365 days (1 year) → 90 days",
    ]);
    expect(retentionShortened(ret({}), ret({ event_days: 90 }))).toEqual([
      "Events: forever → 90 days", "Message events: forever → 90 days", "Page and screen views: forever → 90 days",
    ]);
  });
});

describe("retries", () => {
  it("the default schedule is today's", () => {
    expect(retrySchedule(8, 3600)).toEqual([30, 60, 120, 300, 600, 1800, 3600]);
    expect(retrySchedule(8, 3600).reduce((a, b) => a + b, 0)).toBe(6510);
  });
  it("cut, capped and extended", () => {
    expect(retrySchedule(1, 3600)).toEqual([]);
    expect(retrySchedule(4, 3600)).toEqual([30, 60, 120]);
    expect(retrySchedule(8, 300)).toEqual([30, 60, 120, 300, 300, 300, 300]);
    expect(retrySchedule(10, 14400)).toEqual([30, 60, 120, 300, 600, 1800, 3600, 7200, 14400]);
  });
});

describe("format and checks", () => {
  it("formats", () => {
    expect(formatDays(0)).toBe("forever");
    expect(formatDays(1)).toBe("1 day");
    expect(formatDays(730)).toBe("730 days (2 years)");
    expect(formatDuration(30)).toBe("30 s");
    expect(formatDuration(3600)).toBe("1 h");
    expect(formatDuration(6510)).toBe("1 h 48 min 30 s");
  });
  it("checks values against limits", () => {
    expect(checkValue(limits["retention.event_days"], 0, "Events")).toBeNull();
    expect(checkValue(limits["retention.event_days"], 10, "Events")).toBe("Events: 30 to 3650, or 0");
    expect(checkValue(limits["retries.attempts"], 0, "Tries")).toBe("Tries: 1 to 20");
    expect(checkValue(limits["retries.attempts"], 2.5, "Tries")).toBe("Tries: a whole number");
    expect(validate(defaults(), limits)).toBeNull();
    const s = defaults();
    s.retention.live_feed_size = 5;
    expect(validate(s, limits)).toBe("Live feed size: 100 to 10000");
    const t = defaults();
    t.schedules.daily_segment_at = "25:00";
    expect(validate(t, limits)).toMatch(/like 06:00/);
  });
  it("edits overrides as rows", () => {
    expect(overridesToRows({ b: 1, a: 0 })).toEqual([{ name: "a", days: 0 }, { name: "b", days: 1 }]);
    const lim = limits["retention.event_overrides"];
    expect(rowsToOverrides([{ name: " page_view ", days: 90 }, { name: "", days: 5 }], lim)).toEqual({ value: { page_view: 90 }, error: null });
    expect(rowsToOverrides([], lim)).toEqual({ value: null, error: null });
    expect(rowsToOverrides([{ name: "a b", days: 30 }], lim).error).toMatch(/not an event name/);
    expect(rowsToOverrides([{ name: "x", days: 30 }, { name: "x", days: 40 }], lim).error).toMatch(/twice/);
    expect(rowsToOverrides([{ name: "x", days: 3 }], lim).error).toBe("x: 7 to 3650, or 0");
  });
  it("compares settings ignoring empty maps and order", () => {
    const a = defaults(), b = defaults();
    b.retention.event_overrides = {};
    expect(sameSettings(a, b)).toBe(true);
    a.retention.event_overrides = { x: 1, y: 2 };
    b.retention.event_overrides = { y: 2, x: 1 };
    expect(sameSettings(a, b)).toBe(true);
    b.retries.attempts = 3;
    expect(sameSettings(a, b)).toBe(false);
  });
  it("labels preview items", () => {
    const base = { store: "clickhouse" as const, table: "events", days: 90, cutoff: "", old_days: 0, rows: 5 };
    expect(previewLabel({ ...base, key: "override:90", names: ["click", "page_view"] })).toBe("Events named click, page_view");
    expect(previewLabel({ ...base, key: "events", names: ["purchase"], not_in: true })).toBe("Events of every other name (not purchase)");
    expect(previewLabel({ ...base, key: "events" })).toBe("Events of every name");
    expect(previewLabel({ ...base, key: "message_events", table: "message_events" })).toMatch(/Message events/);
    expect(previewLabel({ ...base, key: "audit", store: "postgres", table: "audit" })).toBe("Audit log entries");
    expect(previewTotal([{ ...base, key: "a" }, { ...base, key: "b", rows: 3, error: "x" }])).toBe(5);
  });
});

describe("daily segment default", () => {
  it("uses the workspace setting, else 06:00 in the browser's zone", () => {
    expect(dailySegmentDefault(undefined, "Asia/Kolkata")).toEqual({ at: "06:00", tz: "Asia/Kolkata" });
    const s = defaults();
    s.schedules.daily_segment_at = "07:30";
    expect(dailySegmentDefault(s, "Europe/Berlin")).toEqual({ at: "07:30", tz: "Europe/Berlin" });
    s.schedules.daily_segment_tz = "UTC";
    expect(dailySegmentDefault(s, "Europe/Berlin")).toEqual({ at: "07:30", tz: "UTC" });
  });
});
