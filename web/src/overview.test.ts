import { describe, expect, it } from "vitest";
import type { MessageCounts } from "./api";
import { change, keyRate, overviewSections, percent, whenText } from "./overview";

const counts = (c: Partial<MessageCounts>): MessageCounts => ({
  sent: 0, delivered: 0, read: 0, failed: 0, fallback: 0, suppressed: 0, capped: 0, deferred: 0, opened: 0, clicked: 0,
  unsubscribed: 0, converted: 0, bounced: 0, complained: 0, downloaded: 0, ...c,
} as MessageCounts);

describe("change against the week before", () => {
  it("says up, down and the same", () => {
    expect(change(30, 20)).toMatchObject({ text: "+50% on the week before", dir: "up" });
    expect(change(15, 20)).toMatchObject({ text: "−25% on the week before", dir: "down" });
    expect(change(103, 100)).toMatchObject({ text: "+3% on the week before", dir: "up" });
    expect(change(1001, 1000)).toMatchObject({ text: "+0.1% on the week before", dir: "up" });
    expect(change(20, 20)).toMatchObject({ text: "same as the week before", dir: "flat" });
    expect(change(100001, 100000).dir).toBe("flat");
  });
  it("has no percentage when there was nothing before", () => {
    expect(change(5, 0)).toMatchObject({ text: "none the week before", dir: "up" });
    expect(change(0, 0).dir).toBe("flat");
    expect(change(0, 8)).toMatchObject({ text: "−100% on the week before", dir: "down" });
    expect(change(3, 1200).title).toBe("1,200 in the 7 days before");
  });
});

describe("sections by role", () => {
  it("follows the server's rule", () => {
    expect(overviewSections({ contacts: "view" })).toEqual({ audience: true, campaigns: false, recent: false, revenue: false });
    expect(overviewSections({ broadcasts: "publish" })).toEqual({ audience: false, campaigns: true, recent: false, revenue: false });
    expect(overviewSections({ broadcasts: "view", reports: "view" })).toEqual({ audience: false, campaigns: true, recent: true, revenue: true });
    expect(overviewSections(undefined)).toEqual({ audience: false, campaigns: false, recent: false, revenue: false });
  });
});

describe("key rate", () => {
  it("is opens for email and clicks for the rest", () => {
    expect(keyRate("email", counts({ sent: 200, opened: 50, clicked: 4 }))).toEqual({ label: "opened", value: 0.25 });
    expect(keyRate("sms", counts({ sent: 200, opened: 50, clicked: 4 }))).toEqual({ label: "clicked", value: 0.02 });
    expect(keyRate("email", counts({}))).toBeNull();
    expect(keyRate("email", undefined)).toBeNull();
  });
  it("formats like the dashboards", () => {
    expect(percent(0.25)).toBe("25%");
    expect(percent(0.035)).toBe("3.5%");
    expect(percent(0)).toBe("0%");
  });
});

describe("when", () => {
  const now = new Date(2026, 10, 14, 12, 0);
  it("names today, tomorrow and yesterday", () => {
    expect(whenText(new Date(2026, 10, 14, 14, 30).toISOString(), now)).toBe("today 14:30");
    expect(whenText(new Date(2026, 10, 15, 9, 0).toISOString(), now)).toBe("tomorrow 09:00");
    expect(whenText(new Date(2026, 10, 13, 9, 0).toISOString(), now)).toBe("yesterday 09:00");
    expect(whenText(new Date(2026, 10, 17, 9, 0).toISOString(), now)).toBe("Tue 17 Nov, 09:00");
  });
});
