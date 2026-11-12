import { describe as suite, expect, it } from "vitest";
import type { ReportSpec } from "./api";
import {
  describe, formatValue, fromSpec, groupLabel, GROUPS, labelIndices, lineGeometry, METRICS, newDraft, niceMax, PRESETS,
  rangeDaysOf, shortTimeLabel, specProblems, toSpec, withGroup, withMetric, withSource, type Draft,
} from "./reportBuilder";

const types = (xs: { type: string }[]) => xs.map((x) => x.type);

suite("what each source allows", () => {
  it("lists the server's metrics per source", () => {
    expect(types(METRICS.events)).toEqual(["count", "unique_contacts", "sum", "avg"]);
    expect(types(METRICS.messages)).toEqual(["count", "unique_contacts"]);
    expect(types(METRICS.contacts)).toEqual(["count", "sum", "avg"]);
  });

  it("lists the server's groupings per source", () => {
    expect(types(GROUPS.events)).toEqual(["none", "event", "prop", "attr", "day", "week", "month"]);
    expect(types(GROUPS.messages)).toEqual(["none", "kind", "channel", "campaign", "day", "week", "month"]);
    expect(types(GROUPS.contacts)).toEqual(["none", "attr", "day", "week", "month"]);
  });
});

suite("spec validation", () => {
  const ok = (d: Draft, days = 30) => expect(specProblems(d, days)).toEqual([]);

  it("accepts every preset", () => {
    for (const p of PRESETS) ok(fromSpec(p.spec));
  });

  it("needs a key for sums, averages and property groups", () => {
    const d = withMetric(newDraft("events"), "sum");
    expect(specProblems(d, 30)[0]).toMatch(/property to add up/);
    ok(withMetric(d, "sum", "amount"));
    expect(specProblems(withGroup(newDraft("events"), "prop"), 30)[0]).toMatch(/group by/);
    expect(specProblems({ ...newDraft("events"), metric: { type: "avg", key: "1bad" } }, 30)).toHaveLength(1);
  });

  it("rejects metrics and groups the source doesn't have", () => {
    expect(specProblems({ ...newDraft("messages"), metric: { type: "sum", key: "amount" } }, 30)[0]).toMatch(/can't show/);
    expect(specProblems({ ...newDraft("contacts"), group: { type: "event", key: "" } }, 30)[0]).toMatch(/can't be grouped/);
  });

  it("needs a time group for line charts and short ranges for days", () => {
    expect(specProblems({ ...newDraft(), chart: "line" }, 30)).toContain("A line chart needs grouping by day, week or month.");
    const daily: Draft = { ...withGroup(newDraft(), "day"), chart: "line" };
    ok(daily, 100);
    expect(specProblems(daily, 101)[0]).toMatch(/week or month/);
    ok(withGroup(daily, "week"), 365);
  });

  it("checks event names and filters", () => {
    expect(specProblems({ ...newDraft(), events: ["purchase", "9bad"] }, 30)).toEqual([`"9bad" isn't a valid event name.`]);
    expect(specProblems({ ...newDraft(), events: Array.from({ length: 11 }, (_, i) => `e${i}`) }, 30)[0]).toMatch(/at most 10/);
    expect(specProblems({ ...newDraft(), where: [{ prop: "amount", op: "gt", value: "x" }] }, 30)).toEqual([`"amount" needs a number.`]);
    expect(specProblems({ ...newDraft(), where: [{ prop: "", op: "eq", value: "x" }] }, 30)[0]).toMatch(/property name/);
    const six = Array.from({ length: 6 }, () => ({ prop: "plan", op: "eq" as const, value: "pro" }));
    expect(specProblems({ ...newDraft("contacts"), where: six }, 30)).toContain("Use at most 5 filters.");
  });

  it("allows relative dates on contact attributes only", () => {
    const w = [{ prop: "last_seen", op: "within_days" as const, value: "7" }];
    ok({ ...newDraft("contacts"), where: w });
    expect(specProblems({ ...newDraft("events"), where: w }, 30)[0]).toMatch(/Event properties/);
    expect(specProblems({ ...newDraft("contacts"), where: [{ prop: "signup", op: "before", value: "soon" }] }, 30)[0]).toMatch(/needs a date/);
  });
});

suite("editing", () => {
  it("resets metric, group and filters when the source changes", () => {
    const d: Draft = {
      ...newDraft("events"), metric: { type: "sum", key: "amount" }, group: { type: "day", key: "" }, chart: "line",
      events: ["purchase"], where: [{ prop: "sku", op: "eq", value: "A" }], segment_id: 4, range_days: 90,
    };
    const m = withSource(d, "messages");
    expect(m).toMatchObject({ source: "messages", metric: { type: "count" }, group: { type: "none" }, events: [], where: [], chart: "bars", segment_id: 4, range_days: 90 });
    expect(specProblems(m, 30)).toEqual([]);
    expect(withSource(d, "events")).toBe(d);
  });

  it("drops the line chart when grouping by something other than time", () => {
    const d: Draft = { ...withGroup(newDraft(), "week"), chart: "line" };
    expect(withGroup(d, "month").chart).toBe("line");
    expect(withGroup(d, "event").chart).toBe("bars");
    expect(withGroup(d, "event", "ignored").group.key).toBe("");
  });

  it("builds only the fields that apply, and round-trips", () => {
    const spec: ReportSpec = {
      source: "events", metric: { type: "sum", key: "amount" }, group_by: { type: "prop", key: "sku" },
      events: ["purchase"], where: [{ prop: "city", op: "in", value: ["Pune", "Mumbai"] }, { prop: "amount", op: "gte", value: 10 }],
      segment_id: 3, chart: "bars", range_days: 7,
    };
    expect(toSpec(fromSpec(spec))).toEqual(spec);
    const messages = toSpec({ ...newDraft("messages"), where: [{ prop: "x", op: "eq", value: "y" }], events: ["a"], channels: ["email"], kinds: ["opened"] });
    expect(messages).toEqual({ source: "messages", metric: { type: "count" }, group_by: { type: "none" }, channels: ["email"], kinds: ["opened"], chart: "bars" });
  });
});

suite("describe", () => {
  it("sums up specs in a line", () => {
    expect(describe(PRESETS[0].spec)).toBe("Sum of amount of purchase, by sku · bars");
    expect(describe(PRESETS[1].spec)).toBe("Count of purchase, per day · line");
    expect(describe(PRESETS[2].spec)).toBe("Messages from one-time campaigns via Email, by campaign · bars");
    expect(describe(PRESETS[3].spec)).toBe("Contacts, by geo_country · bars");
    expect(describe({ source: "contacts", metric: { type: "avg", key: "points" }, group_by: { type: "none" }, chart: "table", where: [{ prop: "plan", op: "eq", value: "pro" }] }))
      .toBe("Average points (1 filter) · total");
    expect(describe({ source: "messages", metric: { type: "unique_contacts" }, group_by: { type: "kind" }, kinds: ["opened", "clicked"], chart: "table" }))
      .toBe("Contacts messaged opened, clicked, by outcome · table");
  });
});

suite("results", () => {
  it("labels groups", () => {
    expect(groupLabel("", "prop")).toBe("(not set)");
    expect(groupLabel("broadcast|12", "campaign", { "broadcast|12": "Diwali sale" })).toBe("Diwali sale");
    expect(groupLabel("journey|3", "campaign")).toBe("Journey #3");
    expect(groupLabel("email", "channel")).toBe("Email");
    expect(groupLabel("opened", "kind")).toBe("Opened");
    expect(groupLabel("2026-11-03", "day")).toBe("3 Nov 2026");
    expect(groupLabel("2026-11-03T00:00:00Z", "week")).toBe("Week of 3 Nov 2026");
    expect(groupLabel("2026-11-01", "month")).toBe("Nov 2026");
    expect(shortTimeLabel("2026-11-03", "day")).toBe("3 Nov");
    expect(shortTimeLabel("2026-11-01", "month")).toBe("Nov 26");
  });

  it("formats values", () => {
    expect(formatValue(1234567, "count")).toBe("12,34,567");
    expect(formatValue(12.3456, "sum")).toBe("12.35");
    expect(formatValue(1500, "avg")).toBe("1,500");
    expect(formatValue(2.5, "unique_contacts")).toBe("3");
  });

  it("rounds axis maxima", () => {
    expect([0, 1, 3, 7, 12, 250, 251].map(niceMax)).toEqual([1, 1, 5, 10, 20, 250, 500]);
  });

  it("places line chart points", () => {
    const box = { width: 120, height: 70, left: 10, right: 10, top: 10, bottom: 10 };
    const g = lineGeometry([0, 5, 10], box);
    expect(g.max).toBe(10);
    expect(g.points).toEqual([{ x: 10, y: 60, value: 0 }, { x: 60, y: 35, value: 5 }, { x: 110, y: 10, value: 10 }]);
    expect(g.ticks).toEqual([{ value: 0, y: 60 }, { value: 5, y: 35 }, { value: 10, y: 10 }]);
    expect(g.path).toBe("M10,60 L60,35 L110,10");
    expect(lineGeometry([4], box).points[0].x).toBe(60);
    const zeros = lineGeometry([0, 0], box);
    expect(zeros.max).toBe(1);
    expect(zeros.points.every((p) => p.y === 60)).toBe(true);
  });

  it("spreads axis labels", () => {
    expect(labelIndices(3)).toEqual([0, 1, 2]);
    expect(labelIndices(30)).toEqual([0, 7, 15, 22, 29]);
    expect(labelIndices(0)).toEqual([]);
  });

  it("measures ranges", () => {
    expect(rangeDaysOf({ preset: 30, from: "", to: "" })).toBe(30);
    expect(rangeDaysOf({ preset: "custom", from: "2026-01-01", to: "2026-01-31" })).toBe(31);
  });
});
