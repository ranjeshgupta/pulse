import { describe, expect, it } from "vitest";
import type { Credit } from "./api";
import {
  averageOrder, creditKey, creditLabel, creditLink, formatConversions, modelDescription, MODELS, normalizeSettings,
  sameSettings, settingsProblems, share, sourceMedium, unattributed, unattributedOrders, windowProblem,
} from "./revenue";

const credit = (c: Partial<Credit>): Credit => ({ kind: "broadcast", revenue: 0, conversions: 0, ...c });

describe("revenue credits", () => {
  it("names messages, falling back to kind and id", () => {
    expect(creditLabel(credit({ id: 12, name: "Diwali sale" }))).toBe("Diwali sale");
    expect(creditLabel(credit({ id: 12 }))).toBe("One-time campaign 12");
    expect(creditLabel(credit({ kind: "journey", id: 3, name: "  " }))).toBe("Journey 3");
  });

  it("names UTM credits by campaign", () => {
    expect(creditLabel(credit({ kind: "utm", campaign: "spring", source: "google" }))).toBe("spring");
    expect(creditLabel(credit({ kind: "utm", source: "google" }))).toBe("(no campaign)");
  });

  it("links broadcasts and journeys only", () => {
    expect(creditLink(credit({ id: 12 }))).toBe("/broadcasts/12");
    expect(creditLink(credit({ kind: "journey", id: 3 }))).toBe("/journeys/3");
    expect(creditLink(credit({ kind: "utm", campaign: "x" }))).toBeUndefined();
    expect(creditLink(credit({ name: "no id" }))).toBeUndefined();
  });

  it("formats source / medium", () => {
    expect(sourceMedium(credit({ kind: "utm", source: "google", medium: "cpc" }))).toBe("google / cpc");
    expect(sourceMedium(credit({ kind: "utm", source: "newsletter" }))).toBe("newsletter / (no medium)");
    expect(sourceMedium(credit({ kind: "utm" }))).toBe("–");
  });

  it("keys rows uniquely", () => {
    const a = creditKey(credit({ kind: "utm", source: "g", medium: "cpc", campaign: "x" }));
    const b = creditKey(credit({ kind: "utm", source: "g", medium: "email", campaign: "x" }));
    expect(a).not.toBe(b);
    expect(creditKey(credit({ id: 1 }))).not.toBe(creditKey(credit({ kind: "journey", id: 1 })));
  });
});

describe("revenue numbers", () => {
  it("computes shares safely", () => {
    expect(share(25, 100)).toBe(0.25);
    expect(share(5, 0)).toBe(0);
    expect(share(150, 100)).toBe(1);
    expect(share(-1, 100)).toBe(0);
  });

  it("computes unattributed revenue without float noise or negatives", () => {
    expect(unattributed(100.3, 100.1)).toBe(0.2);
    expect(unattributed(100, 100.0000001)).toBe(0);
    expect(unattributed(0, 0)).toBe(0);
    expect(unattributedOrders(10, 6.5)).toBe(3.5);
    expect(unattributedOrders(3, 3.0000001)).toBe(0);
  });

  it("computes the average order value", () => {
    expect(averageOrder(1000, 3)).toBe(333.33);
    expect(averageOrder(500, 0)).toBe(0);
  });

  it("formats conversions with one decimal only when fractional", () => {
    expect(formatConversions(12)).toBe("12");
    expect(formatConversions(1200)).toBe("1,200");
    expect(formatConversions(2.5)).toBe("2.5");
    expect(formatConversions(0.333)).toBe("0.3");
    expect(formatConversions(2.96)).toBe("3");
    expect(formatConversions(0.04)).toBe("<0.1");
    expect(formatConversions(0)).toBe("0");
  });
});

describe("attribution models", () => {
  it("describes every model with the window", () => {
    expect(MODELS.map((m) => m.model)).toEqual(["last_touch", "first_touch", "linear"]);
    expect(modelDescription("last_touch", 7)).toMatch(/latest touch in the 7 days/);
    expect(modelDescription("first_touch", 1)).toMatch(/earliest touch in the 1 day /);
    expect(modelDescription("linear", 30)).toMatch(/equal share/);
  });

  it("checks the window", () => {
    expect(windowProblem(7)).toBeNull();
    expect(windowProblem(90)).toBeNull();
    expect(windowProblem(0)).toMatch(/1 to 90/);
    expect(windowProblem(91)).toMatch(/1 to 90/);
    expect(windowProblem(2.5)).toMatch(/1 to 90/);
  });
});

describe("revenue settings", () => {
  it("accepts valid settings", () => {
    expect(settingsProblems({ event: "purchase", amount_prop: "amount", currency: "" })).toEqual([]);
    expect(settingsProblems({ event: "order:completed", amount_prop: "_total", currency: "inr" })).toEqual([]);
  });

  it("reports invalid fields", () => {
    const p = settingsProblems({ event: "1buy", amount_prop: "total-amount", currency: "RUPEE" });
    expect(p).toHaveLength(3);
    expect(p[0]).toMatch(/event/);
    expect(p[1]).toMatch(/amount property/);
    expect(p[2]).toMatch(/3-letter/);
    expect(settingsProblems({ event: "", amount_prop: "amount", currency: "" })).toHaveLength(1);
  });

  it("normalizes like the server", () => {
    expect(normalizeSettings({ event: " purchase ", amount_prop: "amount ", currency: " inr" })).toEqual({ event: "purchase", amount_prop: "amount", currency: "INR" });
    expect(sameSettings({ event: "purchase", amount_prop: "amount", currency: "inr" }, { event: "purchase", amount_prop: "amount", currency: "INR" })).toBe(true);
    expect(sameSettings({ event: "purchase", amount_prop: "amount", currency: "" }, { event: "order", amount_prop: "amount", currency: "" })).toBe(false);
  });
});
