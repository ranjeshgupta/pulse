import { describe as suite, expect, it } from "vitest";
import type { Rule } from "./api";
import {
  describe, describeWhere, filtersFromWhere, fromRule, oldRange, problems, propFieldType, rootFromRule, schemaProp, toRule, validPath, whereFromFilters,
  type Group,
} from "./segmentRules";

suite("none groups", () => {
  it("round-trips and reads as an exclusion", () => {
    const r: Rule = { all: [{ attr: "city", op: "eq", value: "Pune" }, { none: [{ tag: "churn", op: "has" }, { attr: "plan", op: "eq", value: "free" }] }] };
    const root = rootFromRule(r);
    expect((root.children[1] as Group).match).toBe("none");
    expect(toRule(root)).toEqual(r);
    expect(describe(r)).toBe("city is Pune and none of (tagged churn; plan is free)");
  });
});

suite("absolute date ranges", () => {
  it("round-trips between and since", () => {
    const between: Rule = { event: "purchase", op: "gte", count: 1, from: "2026-01-01", to: "2026-03-31" };
    expect(toRule(fromRule(between))).toEqual(between);
    const since: Rule = { event: "purchase", op: "eq", count: 0, from: "2026-01-01" };
    expect(toRule(fromRule(since))).toEqual(since);
    expect(describe(between)).toBe("did purchase at least 1× between 1 Jan 2026 and 31 Mar 2026");
    expect(describe(since)).toBe("never did purchase since 1 Jan 2026");
  });
  it("checks the range", () => {
    const bad = rootFromRule({ event: "purchase", op: "gte", count: 1, from: "2026-03-01", to: "2026-01-01" });
    expect(problems(bad)).toContain("The date range ends before it starts.");
  });
  it("knows when the sweep evaluates a range", () => {
    const today = new Date(Date.UTC(2026, 10, 11));
    expect(oldRange("2025-11-12", today)).toBe(false);
    expect(oldRange("2025-11-11", today)).toBe(true);
    expect(oldRange(undefined, today)).toBe(false);
  });
});

suite("date between, list and enum fields", () => {
  it("keeps dates as dates", () => {
    const r: Rule = { attr: "date_added", op: "between", value: ["2026-01-01", "2026-01-31"] };
    expect(toRule(fromRule(r))).toEqual(r);
    expect(describe(r)).toBe("date added is between 1 Jan 2026 and 31 Jan 2026");
    expect(toRule(fromRule({ attr: "age", op: "between", value: [18, 30] }))).toEqual({ attr: "age", op: "between", value: [18, 30] });
  });
  it("has / doesn't have", () => {
    const r: Rule = { attr: "products", op: "not_has", value: "SKU-1" };
    expect(toRule(fromRule(r))).toEqual(r);
    expect(describe(r)).toBe("products doesn't have SKU-1");
    expect(problems(rootFromRule({ attr: "products", op: "has", value: "" }))[0]).toMatch(/needs a value/);
  });
});

suite("property paths and schemas", () => {
  it("accepts dotted and list paths", () => {
    for (const p of ["amount", "product.code", "items[].code", "items[]"]) expect(validPath(p)).toBe(true);
    for (const p of ["a..b", "items[].tags[]", "a.b.c.d.e", ""]) expect(validPath(p)).toBe(false);
    expect(problems(rootFromRule({ event: "purchase", op: "gte", count: 1, days: 30, where: [{ prop: "items[].code", op: "eq", value: "A1" }] }))).toEqual([]);
  });
  it("types filters from the schema", () => {
    const props = [{ key: "method", type: "enum", values: [{ value: "social" }] }, { key: "item_qty", path: "items[].qty", type: "number" }];
    expect(schemaProp(props, "items[].qty")?.key).toBe("item_qty");
    expect(propFieldType(schemaProp(props, "method")!.type)).toBe("enum");
    expect(schemaProp(props, "item_qty")).toBeUndefined();
  });
  it("converts trigger filters both ways", () => {
    const f = [{ prop: "payment_method", op: "eq", value: "upi" }, { prop: "cart_value", op: "gte", value: 500 }];
    expect(filtersFromWhere(whereFromFilters(f))).toEqual(f);
    expect(describeWhere(f)).toBe("payment_method is upi and cart_value is at least 500");
  });
});
