import { describe, expect, it } from "vitest";
import type { UsersDay } from "./api";
import {
  breakdown, compact, labelIndexes, linePath, linePoints, linesMax, newReturningMax, niceMax, platformLabel, rangeDays,
  shortDay, sourceLabel, sourceOptions, ticks, usersCsvName, usersCsvRows, usersPath,
} from "./usersDash";

const day = (d: Partial<UsersDay>): UsersDay => ({ day: "2026-11-01", dau: 0, wau: 0, mau: 0, new: 0, returning: 0, stickiness: 0, ...d });

describe("users dashboard ranges and paths", () => {
  const now = new Date("2026-11-11T20:30:00Z");
  it("turns presets into UTC days, both included", () => {
    expect(rangeDays({ preset: 90, from: "", to: "" }, now)).toEqual({ from: "2026-08-14", to: "2026-11-11" });
    expect(rangeDays({ preset: 7, from: "", to: "" }, now)).toEqual({ from: "2026-11-05", to: "2026-11-11" });
  });
  it("keeps custom dates as picked", () => {
    expect(rangeDays({ preset: "custom", from: "2026-01-01", to: "2026-02-01" }, now)).toEqual({ from: "2026-01-01", to: "2026-02-01" });
  });
  it("adds filters only when set; an empty source means none", () => {
    const d = { from: "2026-11-01", to: "2026-11-11" };
    expect(usersPath(d, "", null)).toBe("/v1/analytics/users?from=2026-11-01&to=2026-11-11");
    expect(usersPath(d, "ios", "")).toBe("/v1/analytics/users?from=2026-11-01&to=2026-11-11&platform=ios&source=");
    expect(usersPath(d, "", "news letter&x")).toContain("source=news+letter%26x");
  });
});

describe("labels", () => {
  it("names platforms and sources", () => {
    expect(platformLabel("ios")).toBe("iOS");
    expect(platformLabel("other")).toBe("Server / other");
    expect(platformLabel("tv")).toBe("tv");
    expect(platformLabel("")).toBe("(none)");
    expect(sourceLabel("")).toBe("(none)");
    expect(sourceLabel("google")).toBe("google");
  });
  it("formats days in UTC", () => {
    expect(shortDay("2026-11-05")).toMatch(/5 Nov/);
    expect(shortDay("2026-01-01T00:00:00Z")).toMatch(/1 Jan/);
  });
});

describe("chart scaling", () => {
  it("rounds the top up nicely", () => {
    expect(niceMax(0)).toBe(1);
    expect(niceMax(-3)).toBe(1);
    expect(niceMax(1)).toBe(1);
    expect(niceMax(7)).toBe(10);
    expect(niceMax(13)).toBe(20);
    expect(niceMax(21)).toBe(25);
    expect(niceMax(240)).toBe(250);
    expect(niceMax(251)).toBe(500);
    expect(niceMax(1000)).toBe(1000);
    expect(niceMax(1001)).toBe(2000);
  });
  it("spaces ticks evenly", () => {
    expect(ticks(100)).toEqual([0, 25, 50, 75, 100]);
    expect(ticks(10, 2)).toEqual([0, 5, 10]);
  });
  it("shortens axis numbers", () => {
    expect(compact(950)).toBe("950");
    expect(compact(1200)).toBe("1.2K");
    expect(compact(25000)).toBe("25K");
    expect(compact(2_500_000)).toBe("2.5M");
    expect(compact(2.5)).toBe("2.5");
  });
  it("maps values into the box", () => {
    expect(linePoints([0, 50, 100], 100, 200, 100)).toEqual([[0, 100], [100, 50], [200, 0]]);
    expect(linePoints([5], 10, 200, 100)).toEqual([[100, 50]]);
    expect(linePoints([500], 100, 200, 100)).toEqual([[100, 0]]);
    expect(linePath([0, 100], 100, 10, 10)).toBe("M0 10 L10 0");
    expect(linePath([], 100, 10, 10)).toBe("");
  });
  it("picks a few x labels including the ends", () => {
    expect(labelIndexes(0)).toEqual([]);
    expect(labelIndexes(3)).toEqual([0, 1, 2]);
    expect(labelIndexes(90, 5)).toEqual([0, 22, 45, 67, 89]);
  });
  it("tops the charts at the biggest value shown", () => {
    const days = [day({ dau: 3, wau: 9, mau: 40, new: 2, returning: 1 }), day({ dau: 5, wau: 12, mau: 41, new: 1, returning: 4 })];
    expect(linesMax(days, ["dau", "wau", "mau"])).toBe(50);
    expect(linesMax(days, ["dau"])).toBe(5);
    expect(linesMax([], ["dau"])).toBe(1);
    expect(newReturningMax(days)).toBe(5);
  });
});

describe("breakdowns", () => {
  it("sorts by count and shares of active users", () => {
    const rows = breakdown([{ key: "", count: 10 }, { key: "google", count: 30 }, { key: "bing", count: 10 }], 40, sourceLabel);
    expect(rows.map((r) => r.label)).toEqual(["google", "(none)", "bing"]);
    expect(rows[0].share).toBe(0.75);
    expect(breakdown([{ key: "web", count: 5 }], 0, platformLabel)[0].share).toBe(0);
    expect(breakdown([{ key: "web", count: 50 }], 40, platformLabel)[0].share).toBe(1);
  });
  it("keeps the chosen source among the options", () => {
    const by = [{ key: "a", count: 1 }, { key: "b", count: 5 }];
    expect(sourceOptions(by, null)).toEqual(["b", "a"]);
    expect(sourceOptions(by, "zz")).toEqual(["zz", "b", "a"]);
    expect(sourceOptions(by, "a")).toEqual(["b", "a"]);
  });
});

describe("csv", () => {
  it("writes one row per day", () => {
    const rows = usersCsvRows([day({ day: "2026-11-01T00:00:00Z", dau: 3, wau: 9, mau: 40, stickiness: 0.123456, new: 2, returning: 1 })]);
    expect(rows).toEqual([["day", "dau", "wau", "mau", "stickiness", "new", "returning"], ["2026-11-01", 3, 9, 40, 0.1235, 2, 1]]);
  });
  it("names the file after the filters", () => {
    expect(usersCsvName("2026-11-01", "2026-11-11", "", null)).toBe("Users 2026-11-01 to 2026-11-11");
    expect(usersCsvName("2026-11-01", "2026-11-11", "ios", "")).toBe("Users 2026-11-01 to 2026-11-11 iOS source (none)");
  });
});

describe("retries", () => {
  it("doesn't retry rate limits or bad requests", async () => {
    const { ApiError } = await import("./api");
    const { retryUnlessLimited } = await import("./usersDash");
    expect(retryUnlessLimited(0, new ApiError(429, "slow down"))).toBe(false);
    expect(retryUnlessLimited(0, new ApiError(400, "bad"))).toBe(false);
    expect(retryUnlessLimited(0, new ApiError(500, "oops"))).toBe(true);
    expect(retryUnlessLimited(2, new Error("network"))).toBe(false);
  });
});
