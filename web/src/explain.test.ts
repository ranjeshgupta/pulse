import { describe as suite, expect, it } from "vitest";
import { explainRows, verdict } from "./explain";

suite("explainRows", () => {
  it("lists groups and filters with what the contact has", () => {
    const rows = explainRows({
      kind: "all", match: true, children: [
        { kind: "attr", match: true, value: "Pune", rule: { attr: "city", op: "eq", value: "Pune" } },
        { kind: "event", match: true, count: 3, rule: { event: "purchase", op: "gte", count: 1, from: "2026-01-01", to: "2026-03-31" } },
        { kind: "none", match: true, children: [{ kind: "tag", match: false, rule: { tag: "churn", op: "has" } }] },
      ],
    });
    expect(rows.map((r) => [r.depth, r.text, r.match, r.detail])).toEqual([
      [0, "All of these", true, "3 of 3 match"],
      [1, "city is Pune", true, "their value: Pune"],
      [1, "did purchase at least 1× between 1 Jan 2026 and 31 Mar 2026", true, "3 matching events"],
      [1, "None of these", true, "0 of 1 match"],
      [2, "tagged churn", false, undefined],
    ]);
  });
  it("marks rules left to the sweep", () => {
    const [r] = explainRows({ kind: "event", match: false, sweep: true, rule: { event: "login", op: "gte", count: 1, from: "2020-01-01" } });
    expect(r.sweep).toBe(true);
    expect(r.detail).toMatch(/sweep/);
  });
});

suite("verdict", () => {
  it("explains daily segments that lag", () => {
    expect(verdict(false, true, true)).toMatch(/next daily refresh/);
    expect(verdict(true, true, false)).toMatch(/match its rule now/);
  });
});
