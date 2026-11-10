import { describe as suite, expect, it } from "vitest";
import type { Rule } from "./api";
import { describe, fromRule, newGroup, problems, rootFromRule, segmentRefs, toRule, type Group } from "./segmentRules";

const rule: Rule = {
  all: [
    { attr: "city", op: "in", value: ["Pune", "Mumbai"] },
    { attr: "age", op: "gte", value: 18 },
    { any: [{ event: "purchase", op: "gte", count: 2, days: 30 }, { event: "login", op: "eq", count: 0, days: 14 }] },
  ],
};

suite("segment rule editing model", () => {
  it("round-trips the API rule", () => {
    expect(toRule(fromRule(rule))).toEqual(rule);
  });

  it("represents 'never did' as count 0 and back", () => {
    const g = rootFromRule(rule);
    const inner = g.children[2] as Group;
    expect(inner.children[1]).toMatchObject({ kind: "event", op: "none" });
    expect(toRule(inner.children[1])).toEqual({ event: "login", op: "eq", count: 0, days: 14 });
  });

  it("wraps a single condition in a group", () => {
    const g = rootFromRule({ attr: "plan", op: "eq", value: "pro" });
    expect(g.kind).toBe("group");
    expect(g.children).toHaveLength(1);
  });

  it("sends numbers for numeric comparisons and lists for 'is one of'", () => {
    expect(toRule({ kind: "attr", attr: "age", op: "gt", value: "30" })).toEqual({ attr: "age", op: "gt", value: 30 });
    expect(toRule({ kind: "attr", attr: "city", op: "in", value: "Pune, , Delhi " })).toEqual({ attr: "city", op: "in", value: ["Pune", "Delhi"] });
  });

  it("reports what must be fixed", () => {
    expect(problems(newGroup())).toEqual(["Enter an attribute name, e.g. city."]);
    expect(problems({ kind: "attr", attr: "age", op: "gt", value: "old" })).toEqual(['"age" needs a number.']);
    expect(problems({ kind: "event", event: "buy", op: "gte", count: 1, days: 400, where: [] })).toContain("Event windows are 1 to 365 days.");
    expect(problems(rootFromRule(rule))).toEqual([]);
  });

  it("round-trips segment, tag and property-filter conditions", () => {
    const r: Rule = {
      any: [
        { segment: 4, op: "not_in" },
        { tag: "vip", op: "has" },
        { event: "purchase", op: "gte", count: 1, days: 7, where: [{ prop: "amount", op: "gte", value: 100 }, { prop: "sku", op: "in", value: ["A1", "B2"] }] },
      ],
    };
    expect(toRule(fromRule(r))).toEqual(r);
    expect(segmentRefs({ all: [r, { segment: 40, op: "in" }] })).toEqual([4, 40]);
    expect(describe(r, { 4: "Churned" })).toBe("not in “Churned” or tagged vip or did purchase where amount is at least 100 and sku is one of A1, B2 at least 1× in 7 days");
    expect(problems({ kind: "segment", segment: 0, op: "in" })).toEqual(["Choose a segment or list."]);
    expect(problems({ kind: "tag", tag: "a b", op: "has" })).toHaveLength(1);
    expect(problems({ kind: "event", event: "buy", op: "gte", count: 1, days: 7, where: [{ prop: "amount", op: "gt", value: "lots" }] })).toEqual(['"amount" needs a number.']);
    // No filters: the rule has no "where" key, so older rules stay byte-identical.
    expect(toRule({ kind: "event", event: "buy", op: "gte", count: 1, days: 7, where: [] })).toEqual({ event: "buy", op: "gte", count: 1, days: 7 });
  });

  it("describes rules in words", () => {
    expect(describe(rule)).toBe("city is one of Pune, Mumbai and age is at least 18 and (did purchase at least 2× in 30 days or never did login in 14 days)");
  });
});
