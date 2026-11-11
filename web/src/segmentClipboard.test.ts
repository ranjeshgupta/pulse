import { describe, expect, it } from "vitest";
import { copyText, parseText } from "./segmentClipboard";
import { newAttr, newGroup, rootFromRule, toRule, type Group } from "./segmentRules";

const group: Group = newGroup("any", [newAttr("city", "eq", "Pune"), newAttr("city", "eq", "Mumbai")]);

describe("segment clipboard", () => {
  it("round-trips a group into another segment", () => {
    const root = newGroup("all", [newAttr("plan", "eq", "pro")]);
    const r = parseText(copyText(group), root, []);
    expect(r.error).toBeUndefined();
    expect(toRule(r.node!)).toEqual(toRule(group));
  });
  it("refuses what isn't segment filters", () => {
    expect(parseText("nope", newGroup(), []).error).toMatch(/doesn't hold segment filters/);
    expect(parseText(JSON.stringify({ pulse: "journey-steps", version: 1 }), newGroup(), []).error).toMatch(/doesn't hold/);
    expect(parseText(JSON.stringify({ pulse: "segment-rules", version: 9, rule: {} }), newGroup(), []).error).toMatch(/newer/);
    expect(parseText(JSON.stringify({ pulse: "segment-rules", version: 1, rule: { all: [{ bogus: 1 }] } }), newGroup(), []).error).toMatch(/damaged/);
    expect(parseText(JSON.stringify({ pulse: "segment-rules", version: 1, rule: { any: [] } }), newGroup(), []).error).toMatch(/empty/);
  });
  it("respects the size and depth limits", () => {
    let deep: Group = newGroup("all", [newAttr("a", "eq", "1")]);
    for (let i = 0; i < 6; i++) deep = newGroup("all", [deep]);
    expect(parseText(copyText(deep), newGroup(), [0, 0]).error).toMatch(/too deep/);
    const big = rootFromRule({ all: Array.from({ length: 150 }, (_, i) => ({ attr: "x", op: "eq", value: String(i) })) });
    expect(parseText(copyText(big), big, []).error).toMatch(/at most 200/);
  });
});
