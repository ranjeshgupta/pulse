import { describe, expect, it } from "vitest";
import type { ProductRow, ProductsDashboard } from "./api";
import {
  buyersSegment, conversion, defaultDir, eventNameProblem, fillDays, hasProductData, nextSort, productsCsvRows, productsPath, sortProducts,
} from "./productsDash";

const row = (r: Partial<ProductRow>): ProductRow => ({
  code: "p", views: 0, viewers: 0, carts: 0, carters: 0, purchases: 0, buyers: 0, revenue: 0, abandoned: 0, ...r,
});

describe("event names", () => {
  it("accepts event-style names", () => {
    expect(eventNameProblem("product_viewed")).toBeNull();
    expect(eventNameProblem(" add_to_cart ")).toBeNull();
    expect(eventNameProblem("shop.cart:add-1")).toBeNull();
  });
  it("rejects empty, odd and long names", () => {
    expect(eventNameProblem("")).toMatch(/Enter/);
    expect(eventNameProblem("1abc")).toMatch(/Start with a letter/);
    expect(eventNameProblem("add to cart")).toMatch(/Start with a letter/);
    expect(eventNameProblem("a".repeat(64))).toBeNull();
    expect(eventNameProblem("a".repeat(65))).toMatch(/64/);
  });
  it("sends only non-default events", () => {
    const d = { from: "2026-10-13", to: "2026-11-11" };
    expect(productsPath(d, "product_viewed", "add_to_cart")).toBe("/v1/analytics/products?from=2026-10-13&to=2026-11-11");
    expect(productsPath(d, "view_item", "add_to_cart")).toBe("/v1/analytics/products?from=2026-10-13&to=2026-11-11&view_event=view_item");
  });
});

describe("rates and data", () => {
  it("computes view to buy conversion", () => {
    expect(conversion({ buyers: 5, viewers: 20 })).toBe(0.25);
    expect(conversion({ buyers: 5, viewers: 0 })).toBeNull();
    expect(conversion({ buyers: 6, viewers: 3 })).toBe(2);
  });
  it("knows when there are no product events", () => {
    const d = (products: ProductRow[], totals: ProductRow) =>
      ({ report: { totals, products, categories: [], days: [] } }) as unknown as ProductsDashboard;
    expect(hasProductData(d([], row({})))).toBe(false);
    expect(hasProductData(d([], row({ views: 1 })))).toBe(true);
    expect(hasProductData(d([row({})], row({})))).toBe(true);
  });
  it("fills missing days with zeros", () => {
    const days = fillDays([{ day: "2026-11-02T00:00:00Z", views: 4, carts: 1, purchases: 1, revenue: 99 }], "2026-11-01", "2026-11-03");
    expect(days.map((d) => d.day)).toEqual(["2026-11-01", "2026-11-02", "2026-11-03"]);
    expect(days[1].revenue).toBe(99);
    expect(days[0].views).toBe(0);
    expect(fillDays([], "2026-11-03", "2026-11-01")).toEqual([]);
  });
});

describe("sorting", () => {
  const rows = [
    row({ code: "b", category: "shoes", views: 10, viewers: 10, buyers: 1, revenue: 100, purchases: 1 }),
    row({ code: "a", views: 30, viewers: 20, buyers: 10, revenue: 50, purchases: 12 }),
    row({ code: "c", category: "bags", views: 30, viewers: 0, buyers: 2, revenue: 300, purchases: 2 }),
  ];
  it("sorts numbers with ties broken by revenue", () => {
    expect(sortProducts(rows, "views", "desc").map((r) => r.code)).toEqual(["c", "a", "b"]);
    expect(sortProducts(rows, "views", "asc").map((r) => r.code)).toEqual(["b", "c", "a"]);
    expect(sortProducts(rows, "revenue", "desc").map((r) => r.code)).toEqual(["c", "b", "a"]);
  });
  it("sorts by conversion, products without viewers last", () => {
    expect(sortProducts(rows, "conversion", "desc").map((r) => r.code)).toEqual(["a", "b", "c"]);
  });
  it("sorts text, empty categories last", () => {
    expect(sortProducts(rows, "code", "asc").map((r) => r.code)).toEqual(["a", "b", "c"]);
    expect(sortProducts(rows, "category", "asc").map((r) => r.code)).toEqual(["c", "b", "a"]);
    expect(sortProducts(rows, "category", "desc").map((r) => r.code)).toEqual(["b", "c", "a"]);
  });
  it("does not change the input", () => {
    sortProducts(rows, "code", "desc");
    expect(rows[0].code).toBe("b");
  });
  it("flips the same column and starts others at their default", () => {
    expect(nextSort({ key: "revenue", dir: "desc" }, "revenue")).toEqual({ key: "revenue", dir: "asc" });
    expect(nextSort({ key: "revenue", dir: "desc" }, "code")).toEqual({ key: "code", dir: "asc" });
    expect(defaultDir("buyers")).toBe("desc");
  });
});

describe("csv and segments", () => {
  it("exports the products table", () => {
    const out = productsCsvRows([row({ code: "SKU-1", category: "shoes", views: 9, viewers: 4, buyers: 1, revenue: 10.456 })], "INR");
    expect(out[0]).toContain("revenue_inr");
    expect(out[1]).toEqual(["SKU-1", "shoes", 9, 4, 0, 0, 0, 1, 10.46, 0, 0.25]);
    expect(productsCsvRows([row({})], "")[0]).toContain("revenue");
    expect(productsCsvRows([row({})], "")[1][10]).toBe("");
  });
  it("builds the buyers segment", () => {
    expect(buyersSegment("purchase", "SKU-1", "2026-10-13", "2026-11-11")).toEqual({
      name: "Bought SKU-1 (2026-10-13–2026-11-11)",
      rule: { event: "purchase", op: "gte", count: 1, from: "2026-10-13", to: "2026-11-11", where: [{ prop: "product_code", op: "eq", value: "SKU-1" }] },
    });
  });
  it("keeps long names short but the rule exact", () => {
    const code = "x".repeat(300);
    const s = buyersSegment("purchase", code, "2026-10-13", "2026-11-11");
    expect(s.name.length).toBeLessThanOrEqual(200);
    expect(s.rule.where?.[0].value).toBe(code);
  });
});
