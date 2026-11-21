import { describe, expect, it } from "vitest";
import type { Estimate } from "./api";
import { deadlineProblem, describeDeadline, estimateSteps, normalizeTags, stepProblem, suggestTags, tagProblem, type WizardState } from "./campaigns";

describe("tags", () => {
  it("normalises like the server", () => {
    expect(normalizeTags(["  Diwali   2026 ", "diwali 2026", "", "Retention"])).toEqual(["Diwali 2026", "Retention"]);
  });
  it("explains bad tags", () => {
    expect(tagProblem("a,b", [])).toMatch(/commas/);
    expect(tagProblem("x".repeat(41), [])).toMatch(/40/);
    expect(tagProblem("new", Array.from({ length: 20 }, (_, i) => `t${i}`))).toMatch(/20/);
    expect(tagProblem("ok", ["fine"])).toBeNull();
  });
  it("suggests unused tags that match", () => {
    expect(suggestTags(["Festive", "Retention", "festive sale"], "fest", ["Festive"])).toEqual(["festive sale"]);
    expect(suggestTags(["A", "B"], "", [])).toEqual(["A", "B"]);
  });
});

describe("deadlines", () => {
  it("validates", () => {
    expect(deadlineProblem(null)).toBeNull();
    expect(deadlineProblem({})).toMatch(/time of day/);
    expect(deadlineProblem({ at: "9pm" })).toMatch(/HH:MM/);
    expect(deadlineProblem({ hours: 200 })).toMatch(/168/);
    expect(deadlineProblem({ at: "21:00", hours: 4 })).toBeNull();
  });
  it("describes", () => {
    expect(describeDeadline(null)).toMatch(/No deadline/);
    expect(describeDeadline({ at: "21:00" }, true)).toMatch(/after 21:00 in each contact's time zone/);
    expect(describeDeadline({ hours: 1, at: "21:00", timezone: "Asia/Kolkata" })).toMatch(/Asia\/Kolkata.*more than 1 hour after/);
  });
});

describe("estimate steps", () => {
  const e: Estimate = { audience: 1000, after_exclude: 900, after_consent: 850, reachable: 800, will_send: 790,
    capped: { capped: 10, approximate: false, checked: 800 }, cap: { max: 3, hours: 24 }, ready: true, computed_at: "", cached_until: "" };
  it("shows what each step drops", () => {
    const s = estimateSteps(e, "Email", true);
    expect(s.map((x) => [x.key, x.count, x.dropped])).toEqual([
      ["audience", 1000, 0], ["exclude", 900, 100], ["consent", 850, 50], ["reach", 800, 50], ["cap", 790, 10]]);
  });
  it("leaves out exclusions and caps when there are none", () => {
    const s = estimateSteps({ ...e, after_exclude: 1000, capped: undefined, cap: undefined }, "SMS", false);
    expect(s.map((x) => x.key)).toEqual(["audience", "consent", "reach"]);
    expect(s[1].dropped).toBe(150);
  });
});

describe("wizard checks", () => {
  const base: WizardState = { name: "Diwali", channel: "email", include: [1], exclude: [], templateMode: "write", start_at: "2026-11-12T09:00:00Z", repeat: "none" };
  it("audience", () => {
    expect(stepProblem({ ...base, name: "" }, "audience", {})).toMatch(/Name/);
    expect(stepProblem({ ...base, include: [] }, "audience", {})).toMatch(/segment/);
    expect(stepProblem({ ...base, exclude: [1] }, "audience", {})).toMatch(/both/);
    expect(stepProblem(base, "audience", {})).toBeNull();
  });
  it("content", () => {
    expect(stepProblem(base, "content", { subject: "", body: true })).toMatch(/subject/);
    expect(stepProblem({ ...base, channel: "sms" }, "content", { body: false })).toMatch(/message/);
    expect(stepProblem({ ...base, templateMode: "pick" }, "content", {})).toMatch(/template/);
    expect(stepProblem(base, "content", { subject: "Hi", body: true })).toBeNull();
  });
  it("schedule", () => {
    expect(stepProblem({ ...base, deadline: { at: "x" } }, "schedule", {})).toMatch(/HH:MM/);
    expect(stepProblem(base, "schedule", {})).toBeNull();
  });
});

import { speedProblem, speedWords } from "./campaigns";
describe("sending speed", () => {
  it("explains and checks a speed", () => {
    expect(speedWords(undefined)).toMatch(/As fast/);
    expect(speedWords(10000, 25000)).toBe("10,000 an hour: everyone has it within about 2.5 h");
    expect(speedWords(60, 30)).toMatch(/30 min/);
    expect(speedProblem(10)).toMatch(/60 to/);
    expect(speedProblem(500)).toBeNull();
  });
});

import { uplift } from "./campaigns";
describe("uplift", () => {
  it("compares recipients with the control group", () => {
    const u = uplift(1000, 80, 100, 5)!;
    expect(u.points).toBeCloseTo(3);
    expect(u.extra).toBe(30);
    expect(uplift(0, 0, 10, 1)).toBeNull();
  });
});
