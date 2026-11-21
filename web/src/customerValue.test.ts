import { describe, expect, it } from "vitest";
import { fold, segmentRule, type CVResponse } from "./customerValue";

const resp: CVResponse = {
  settings: { event: "purchase", amount_prop: "amount" },
  groups: [{ name: "Champions", note: "best" }, { name: "At risk", note: "win back" }, { name: "Lost", note: "" }],
  risks: ["low", "medium", "high", "lost"],
  rows: [
    { group: "Champions", churn_risk: "low", contacts: 30, revenue: 9000, avg_orders: 10, avg_days_since: 5 },
    { group: "Champions", churn_risk: "medium", contacts: 10, revenue: 1000, avg_orders: 6, avg_days_since: 25 },
    { group: "At risk", churn_risk: "high", contacts: 20, revenue: 4000, avg_orders: 4, avg_days_since: 150 },
  ],
};

describe("customer value", () => {
  it("folds rows per group with weighted averages", () => {
    const f = fold(resp);
    expect(f.total).toBe(60);
    expect(f.revenue).toBe(14000);
    expect(f.atRisk).toBe(20);
    expect(f.atRiskRevenue).toBe(4000);
    const champ = f.groups[0];
    expect(champ.contacts).toBe(40);
    expect(champ.avgOrders).toBe(9);
    expect(champ.avgDays).toBe(10);
    expect(champ.risks).toEqual({ low: 30, medium: 10 });
    expect(f.groups[2].contacts).toBe(0); // groups keep the server's order, empty ones too
  });
  it("makes segment rules", () => {
    expect(segmentRule("At risk")).toEqual({ attr: "rfm_group", op: "eq", value: "At risk" });
    expect(segmentRule("Loyal", "high")).toEqual({ all: [{ attr: "rfm_group", op: "eq", value: "Loyal" }, { attr: "churn_risk", op: "eq", value: "high" }] });
  });
});
