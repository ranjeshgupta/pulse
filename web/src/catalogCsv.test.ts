import { describe, expect, it } from "vitest";
import { chunks, inStock, parseCsv, priceText, toProducts } from "./catalogCsv";

describe("catalog CSV", () => {
  it("parses quotes, commas and line breaks", () => {
    expect(parseCsv('﻿sku,title\r\n"A1","Kurta, linen"\n"B2","Line\nbreak ""quoted"""\n\n')).toEqual([
      ["sku", "title"], ["A1", "Kurta, linen"], ["B2", 'Line\nbreak "quoted"'],
    ]);
  });
  it("maps rows to products", () => {
    const r = toProducts(parseCsv([
      "SKU,Title,Price,Sale price,Currency,Availability,Image link,Color",
      "A1,Kurta,\"₹1,299\",999,inr,in stock,https://cdn.test/a.jpg,red",
      "B2,Saree,5999,,INR,out of stock,http://insecure.test/b.jpg,blue",
      ",No code,1,,,,,",
      "A1,Duplicate,1,,,,,",
    ].join("\n")));
    expect(r.products).toEqual([
      { code: "A1", name: "Kurta", price: 1299, sale_price: 999, currency: "INR", in_stock: true, image_url: "https://cdn.test/a.jpg" },
      { code: "B2", name: "Saree", price: 5999, currency: "INR", in_stock: false },
    ]);
    expect(r.problems).toHaveLength(2);
    expect(r.unknown).toEqual(["Color"]);
    expect(toProducts(parseCsv("foo,bar\n1,2")).problems[0]).toMatch(/code/);
  });
  it("reads stock and prices", () => {
    expect([inStock(""), inStock("0"), inStock("12"), inStock("Sold out"), inStock("yes")]).toEqual([true, false, true, false, true]);
    expect(priceText({ price: 1299, sale_price: 999, currency: "INR" })).toBe("INR 999 (was INR 1,299)");
    expect(priceText({})).toBe("");
    expect(chunks([1, 2, 3], 2)).toEqual([[1, 2], [3]]);
  });
});
