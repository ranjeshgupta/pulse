import { describe, expect, it } from "vitest";
import { templateFilters } from "./contactLinks";
import { bestTime, buckets, heatLevel, hourLabel, templateKeys } from "./templateReport";

describe("template report", () => {
  it("picks figures for the channel", () => {
    expect(templateKeys("email")).toContain("complained");
    expect(templateKeys("whatsapp")).toEqual(["sent", "delivered", "failed", "read", "fallback"]);
    expect(templateKeys("sms")).toEqual(["sent", "delivered", "failed"]);
  });
  it("fills every bucket of the period", () => {
    const days = buckets([{ t: "2026-11-02", kind: "sent", count: 4 }], "2026-11-01", "2026-11-03", false);
    expect(days.map((d) => d.t)).toEqual(["2026-11-01", "2026-11-02", "2026-11-03"]);
    expect(days[1].values.sent).toBe(4);
    const hours = buckets([{ t: "2026-11-01 09:00", kind: "opened", count: 2 }], "2026-11-01", "2026-11-01", true);
    expect(hours).toHaveLength(24);
    expect(hours[9].values.opened).toBe(2);
  });
  it("scales the heatmap and finds the busiest time", () => {
    expect(heatLevel(0, 10)).toBe(0);
    expect(heatLevel(1, 100)).toBe(1);
    expect(heatLevel(100, 100)).toBe(5);
    const heat = Array.from({ length: 7 }, () => Array(24).fill(0));
    heat[2][19] = 9;
    expect(bestTime(heat)).toEqual({ day: "Wed", hour: 19, count: 9 });
    expect(bestTime(Array.from({ length: 7 }, () => Array(24).fill(0)))).toBeNull();
    expect(hourLabel(0)).toBe("12 am");
    expect(hourLabel(13)).toBe("1 pm");
  });
  it("links counts to the template's contacts", () => {
    expect(templateFilters(7, "bounced", { from: "2026-11-01", to: "2026-11-21" }, { reason: "bounce_hard" }))
      .toEqual([["template", 7], ["outcome", "bounced"], ["reason", "bounce_hard"], ["period", "2026-11-01..2026-11-21"]]);
  });
});
