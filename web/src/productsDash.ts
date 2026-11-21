// Pure helpers for the Products dashboard (Analyze → Products): event name checks, sorting, rates,
// daily series, the CSV export and the "buyers of this product" segment rule. Tested in
// productsDash.test.ts.
import type { EventRule, ProductRow, ProductsDashboard } from "./api";

export const DEFAULT_VIEW_EVENT = "product_viewed";
export const DEFAULT_CART_EVENT = "add_to_cart";
/** The server's event name rule (internal/event ValidName). */
export const EVENT_NAME_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;

export function eventNameProblem(name: string): string | null {
  const n = name.trim();
  if (!n) return "Enter an event name.";
  if (n.length > 64) return "Use at most 64 characters.";
  if (!EVENT_NAME_RE.test(n)) return "Start with a letter; use letters, digits and _ . : - only.";
  return null;
}

export function productsPath(days: { from: string; to: string }, view: string, cart: string): string {
  const q = new URLSearchParams({ from: days.from, to: days.to });
  if (view && view !== DEFAULT_VIEW_EVENT) q.set("view_event", view);
  if (cart && cart !== DEFAULT_CART_EVENT) q.set("cart_event", cart);
  return `/v1/analytics/products?${q}`;
}

/** conversion: buyers ÷ viewers, null without viewers (buyers can exceed viewers when people buy
 * without a tracked view, so it isn't capped). */
export const conversion = (r: Pick<ProductRow, "buyers" | "viewers">): number | null => (r.viewers > 0 ? r.buyers / r.viewers : null);

export const hasProductData = (d: ProductsDashboard) => {
  const t = d.report.totals;
  return d.report.products.length > 0 || t.views + t.carts + t.purchases > 0;
};

export type SortKey = "code" | "category" | "views" | "carters" | "purchases" | "buyers" | "revenue" | "abandoned" | "conversion";
export type SortDir = "asc" | "desc";

export const COLUMNS: { key: SortKey; label: string; num: boolean; title?: string }[] = [
  { key: "code", label: "Product", num: false },
  { key: "category", label: "Category", num: false },
  { key: "views", label: "Views", num: true, title: "Views (people who viewed in brackets)" },
  { key: "carters", label: "Add to cart", num: true, title: "People who added it to their cart" },
  { key: "purchases", label: "Purchases", num: true },
  { key: "buyers", label: "Buyers", num: true },
  { key: "revenue", label: "Revenue", num: true },
  { key: "abandoned", label: "Abandoned", num: true, title: "Added it to their cart but didn't buy it in the range" },
  { key: "conversion", label: "Conversion", num: true, title: "Buyers ÷ viewers" },
];

/** defaultDir: numbers sort biggest first, text A–Z. */
export const defaultDir = (key: SortKey): SortDir => (key === "code" || key === "category" ? "asc" : "desc");

function sortValue(r: ProductRow, key: SortKey): number | string {
  switch (key) {
    case "code": return r.code;
    case "category": return r.category ?? "";
    case "conversion": return conversion(r) ?? -1;
    default: return r[key];
  }
}

/** sortProducts returns a sorted copy; ties fall back to revenue, then purchases, then code. */
export function sortProducts(rows: ProductRow[], key: SortKey, dir: SortDir): ProductRow[] {
  const sign = dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const va = sortValue(a, key), vb = sortValue(b, key);
    let c = typeof va === "string" && typeof vb === "string" ? va.localeCompare(vb) : (va as number) - (vb as number);
    // Empty categories go last whichever way you sort.
    if (key === "category" && (va === "") !== (vb === "")) return va === "" ? 1 : -1;
    c *= sign;
    return c || b.revenue - a.revenue || b.purchases - a.purchases || a.code.localeCompare(b.code);
  });
}

/** nextSort: clicking the current column flips it; another column starts at its default. */
export function nextSort(cur: { key: SortKey; dir: SortDir }, key: SortKey): { key: SortKey; dir: SortDir } {
  if (cur.key === key) return { key, dir: cur.dir === "asc" ? "desc" : "asc" };
  return { key, dir: defaultDir(key) };
}

export type Metric = "views" | "carts" | "purchases" | "revenue";
export const METRICS: { key: Metric; label: string }[] = [
  { key: "views", label: "Views" },
  { key: "carts", label: "Add to cart" },
  { key: "purchases", label: "Purchases" },
  { key: "revenue", label: "Revenue" },
];

export type ProductDay = ProductsDashboard["report"]["days"][number];

/** fillDays gives one row per UTC day from–to (both included), zeros where the answer had none. */
export function fillDays(days: ProductDay[], from: string, to: string): ProductDay[] {
  const byDay = new Map((days ?? []).map((d) => [String(d.day).slice(0, 10), d]));
  const out: ProductDay[] = [];
  const end = Date.parse(`${to}T00:00:00Z`);
  for (let t = Date.parse(`${from}T00:00:00Z`); t <= end && out.length < 400; t += 86_400_000) {
    const day = new Date(t).toISOString().slice(0, 10);
    const d = byDay.get(day);
    out.push(d ? { ...d, day } : { day, views: 0, carts: 0, purchases: 0, revenue: 0 });
  }
  return out;
}

/** productsCsvRows: the products table as CSV (revenue as a plain number; currency in its header). */
export function productsCsvRows(rows: ProductRow[], currency: string): (string | number)[][] {
  return [
    ["product_code", "category", "views", "viewers", "add_to_cart", "add_to_cart_contacts", "purchases", "buyers",
      currency ? `revenue_${currency.toLowerCase()}` : "revenue", "abandoned", "conversion"],
    ...rows.map((r) => {
      const c = conversion(r);
      return [r.code, r.category ?? "", r.views, r.viewers, r.carts, r.carters, r.purchases, r.buyers,
        Number.isFinite(r.revenue) ? Number(r.revenue.toFixed(2)) : "", r.abandoned, c === null ? "" : Number(c.toFixed(4))];
    }),
  ];
}

const MAX_SEGMENT_NAME = 200;

/** buyersSegment: the body of POST /v1/segments for "contacts who bought this product in the range"
 * (the same rule the server's BuyersRule builds). */
export function buyersSegment(purchaseEvent: string, code: string, from: string, to: string): { name: string; rule: EventRule } {
  let name = `Bought ${code} (${from}–${to})`;
  if (name.length > MAX_SEGMENT_NAME) name = `Bought ${code.slice(0, MAX_SEGMENT_NAME - 40)}… (${from}–${to})`;
  return {
    name,
    rule: { event: purchaseEvent, op: "gte", count: 1, from, to, where: [{ prop: "product_code", op: "eq", value: code }] },
  };
}
