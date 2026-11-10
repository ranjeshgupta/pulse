import { describe, expect, it } from "vitest";
import type { CohortSpec } from "./api";
import {
  addPeriods, averageCell, cell, clampInt, cohortCount, cohortLabel, cohortTitle, columnLabel, comingBack, contrast, formedBy,
  heat, lastBounds, MAX_RECENT, periodsIssue, periodsText, periodStart, rangeIssue,
} from "./cohorts";

const at = (iso: string) => new Date(iso);
// Wednesday 11 Nov 2026, 14:37:52 UTC.
const NOW = at("2026-11-11T14:37:52.345Z");

describe("UTC periods", () => {
  it("truncates to the day, the Monday and the 1st, in UTC", () => {
    expect(periodStart(NOW, "day").toISOString()).toBe("2026-11-11T00:00:00.000Z");
    expect(periodStart(NOW, "week").toISOString()).toBe("2026-11-09T00:00:00.000Z");
    expect(periodStart(NOW, "month").toISOString()).toBe("2026-11-01T00:00:00.000Z");
  });

  it("puts Sunday in the week that started the Monday before, and Monday in its own", () => {
    expect(periodStart(at("2026-11-15T23:59:00Z"), "week").toISOString()).toBe("2026-11-09T00:00:00.000Z");
    expect(periodStart(at("2026-11-16T00:00:00Z"), "week").toISOString()).toBe("2026-11-16T00:00:00.000Z");
  });

  it("moves by periods, across month and year ends", () => {
    expect(addPeriods(at("2026-11-09T00:00:00Z"), "week", -2).toISOString()).toBe("2026-10-26T00:00:00.000Z");
    expect(addPeriods(at("2026-11-01T00:00:00Z"), "month", -11).toISOString()).toBe("2025-12-01T00:00:00.000Z");
    expect(addPeriods(at("2026-12-31T00:00:00Z"), "day", 1).toISOString()).toBe("2027-01-01T00:00:00.000Z");
  });
});

describe("which cohorts to show", () => {
  it("starts the last N at the period N-1 before the current one and ends now, to the minute", () => {
    expect(lastBounds("week", 8, NOW)).toEqual({ from: "2026-09-21T00:00:00.000Z", to: "2026-11-11T14:37:00.000Z" });
    expect(lastBounds("day", 1, NOW)).toEqual({ from: "2026-11-11T00:00:00.000Z", to: "2026-11-11T14:37:00.000Z" });
    expect(lastBounds("month", 12, NOW).from).toBe("2025-12-01T00:00:00.000Z");
  });

  it("stays the same within a minute, so the query key is stable", () => {
    expect(lastBounds("week", 8, at("2026-11-11T14:37:01Z"))).toEqual(lastBounds("week", 8, at("2026-11-11T14:37:59Z")));
  });

  it("counts the cohorts a range produces, like the server", () => {
    const b = lastBounds("week", 8, NOW);
    expect(cohortCount("week", b.from, b.to)).toBe(8);
    expect(cohortCount("day", "2026-11-01T10:00:00Z", "2026-11-03T00:00:00Z")).toBe(2);
    expect(cohortCount("month", "2026-01-15T00:00:00Z", "2026-03-01T00:00:00Z")).toBe(2);
  });

  it("the largest last N of each period is always accepted", () => {
    for (const p of ["day", "week", "month"] as const) {
      for (const now of [NOW, at("2026-03-31T23:59:00Z"), at("2028-02-29T12:00:00Z"), at("2026-11-15T23:59:00Z")]) {
        const b = lastBounds(p, MAX_RECENT[p], now);
        expect(rangeIssue(p, b.from, b.to)).toBeNull();
        expect(cohortCount(p, b.from, b.to)).toBe(MAX_RECENT[p]);
      }
    }
  });

  it("explains ranges the server refuses", () => {
    expect(rangeIssue("day", "2026-01-01T00:00:00Z", "2026-05-01T00:00:00Z")).toMatch(/more than 60 days/);
    expect(rangeIssue("week", "2025-01-01T00:00:00Z", "2026-03-01T00:00:00Z")).toMatch(/year or less/);
    expect(rangeIssue("week", "2026-03-01T00:00:00Z", "2026-03-01T00:00:00Z")).toMatch(/ends after/);
    expect(rangeIssue("week", "2026-01-01T00:00:00Z", "2026-11-01T00:00:00Z")).toBeNull();
  });
});

describe("periods to follow", () => {
  it("checks against the per-period maximum", () => {
    expect(periodsIssue("week", 26)).toBeNull();
    expect(periodsIssue("week", 27)).toBe("Follow 1 to 26 weeks.");
    expect(periodsIssue("month", 0)).toBe("Follow 1 to 12 months.");
    expect(periodsIssue("day", 2.5)).toBe("Follow 1 to 60 days.");
  });

  it("clamps typed numbers", () => {
    expect(clampInt(99, 12)).toBe(12);
    expect(clampInt(0, 12)).toBe(1);
    expect(clampInt(NaN, 12)).toBe(1);
    expect(clampInt(3.4, 12)).toBe(3);
  });
});

describe("labels", () => {
  it("formats cohort starts per period in UTC", () => {
    expect(cohortLabel("2026-11-09T00:00:00Z", "day")).toBe("Mon 9 Nov");
    expect(cohortLabel("2026-11-09T00:00:00Z", "week")).toBe("Week of 9 Nov");
    expect(cohortLabel("2026-11-01T00:00:00Z", "month")).toBe("Nov 2026");
    // UTC, whatever the viewer's zone: 1 Dec 00:00 UTC is still in December.
    expect(cohortLabel("2026-12-01T00:00:00.000Z", "month")).toBe("Dec 2026");
  });

  it("spells out the span for tooltips", () => {
    expect(cohortTitle("2026-11-09T00:00:00Z", "week")).toBe("9 Nov 2026 – 15 Nov 2026");
    expect(cohortTitle("2026-02-01T00:00:00Z", "month")).toBe("1 Feb 2026 – 28 Feb 2026");
    expect(cohortTitle("2026-11-09T00:00:00Z", "day")).toBe("9 Nov 2026");
  });

  it("names columns and counts", () => {
    expect(columnLabel("week", 0)).toBe("Week 0");
    expect(periodsText("day", 1)).toBe("1 day");
    expect(periodsText("month", 6)).toBe("6 months");
  });

  it("describes a saved report", () => {
    const s: CohortSpec = { by: "first_seen", period: "week", periods: 8, return: {} };
    expect(formedBy(s)).toBe("First seen");
    expect(comingBack(s)).toBe("Any activity");
    const e: CohortSpec = { ...s, by: "event", event: "signup", return: { event: "purchase", where: [{ prop: "amount", op: "gt", value: 0 }] } };
    expect(formedBy(e)).toBe("First did signup");
    expect(comingBack(e)).toBe("Did purchase (filtered)");
  });
});

describe("cells", () => {
  it("shows shares, periods not reached yet and empty cohorts", () => {
    expect(cell(25, 100)).toEqual({ kind: "value", share: 0.25, count: 25 });
    expect(cell(0, 100)).toEqual({ kind: "value", share: 0, count: 0 });
    expect(cell(null, 100)).toEqual({ kind: "pending" });
    expect(cell(undefined, 100)).toEqual({ kind: "pending" });
    expect(cell(0, 0)).toEqual({ kind: "empty" });
    expect(cell(null, 0)).toEqual({ kind: "pending" });
    expect(averageCell(0.4)).toEqual({ kind: "value", share: 0.4 });
    expect(averageCell(null)).toEqual({ kind: "pending" });
  });
});

describe("heat map", () => {
  it("goes from white at 0% to cobalt at 100%", () => {
    expect(heat(0).background).toBe("#ffffff");
    expect(heat(1).background).toBe("#2747d6");
    expect(heat(-1)).toEqual(heat(0));
    expect(heat(2)).toEqual(heat(1));
    expect(heat(NaN)).toEqual(heat(0));
  });

  it("keeps the number readable (4.5:1) at every share", () => {
    for (let i = 0; i <= 1000; i++) {
      const h = heat(i / 1000);
      expect(contrast(h.background, h.color), `share ${i / 1000}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("gets darker as the share grows", () => {
    let prev = Infinity;
    for (let i = 0; i <= 100; i++) {
      const c = contrast(heat(i / 100).background, "#000000");
      expect(c).toBeLessThanOrEqual(prev);
      prev = c;
    }
    expect(heat(0.05).color).toBe("#152238");
    expect(heat(0.9).color).toBe("#ffffff");
  });

  it("computes WCAG contrast", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 5);
    expect(contrast("#777777", "#777777")).toBe(1);
  });
});
