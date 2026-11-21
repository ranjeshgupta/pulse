// Customer value (O6): the buyers' RFM groups and churn risks, as GET /v1/revenue/customer-value
// returns them (one row per group and risk), folded per group for the page. Pure for vitest.

export interface CVRow { group: string; churn_risk: string; contacts: number; revenue: number; avg_orders: number; avg_days_since: number }
export interface CVResponse {
  settings: { event: string; amount_prop: string; currency?: string };
  rows: CVRow[]; groups: { name: string; note: string }[]; risks: string[];
}
export interface CVGroup {
  name: string; note: string; contacts: number; revenue: number; avgOrders: number; avgDays: number;
  risks: Record<string, number>;
}

/** fold sums each group's rows (averages weighted by contacts), in the server's group order. */
export function fold(r: CVResponse): { groups: CVGroup[]; total: number; revenue: number; atRisk: number; atRiskRevenue: number } {
  const by = new Map<string, CVGroup>(r.groups.map((g) => [g.name, { name: g.name, note: g.note, contacts: 0, revenue: 0, avgOrders: 0, avgDays: 0, risks: {} }]));
  let total = 0, revenue = 0, atRisk = 0, atRiskRevenue = 0;
  for (const x of r.rows) {
    let g = by.get(x.group);
    if (!g) by.set(x.group, g = { name: x.group, note: "", contacts: 0, revenue: 0, avgOrders: 0, avgDays: 0, risks: {} });
    const n = g.contacts + x.contacts;
    if (n > 0) {
      g.avgOrders = (g.avgOrders * g.contacts + x.avg_orders * x.contacts) / n;
      g.avgDays = (g.avgDays * g.contacts + x.avg_days_since * x.contacts) / n;
    }
    g.contacts = n;
    g.revenue += x.revenue;
    g.risks[x.churn_risk] = (g.risks[x.churn_risk] ?? 0) + x.contacts;
    total += x.contacts;
    revenue += x.revenue;
    if (x.churn_risk === "high") { atRisk += x.contacts; atRiskRevenue += x.revenue; }
  }
  return { groups: [...by.values()], total, revenue, atRisk, atRiskRevenue };
}

/** RISK_LABEL words each churn risk. */
export const RISK_LABEL: Record<string, string> = { low: "Low", medium: "Medium", high: "High", lost: "Lost" };

/** segmentRule is the dynamic segment of a group (and optionally one churn risk). */
export function segmentRule(group: string, risk?: string) {
  const g = { attr: "rfm_group", op: "eq", value: group };
  return risk ? { all: [g, { attr: "churn_risk", op: "eq", value: risk }] } : g;
}
