// Product catalog CSV import (O6): parse a CSV file (RFC 4180: quotes, commas and line breaks in
// quotes) and turn its rows into products by header name. Pure for vitest.
import { own } from "./safe";

export interface Product {
  code: string; name: string; url?: string; image_url?: string; price?: number; sale_price?: number;
  currency?: string; category?: string; in_stock: boolean; updated_at?: string;
}

/** parseCsv splits CSV text into rows of cells. */
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [], cell = "", q = false;
  const s = text.replace(/^﻿/, "");
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (q) {
      if (c === '"' && s[i + 1] === '"') { cell += '"'; i++; }
      else if (c === '"') q = false;
      else cell += c;
    } else if (c === '"' && cell === "") q = true;
    else if (c === ",") { row.push(cell); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && s[i + 1] === "\n") i++;
      row.push(cell); cell = "";
      if (row.some((x) => x.trim() !== "")) rows.push(row);
      row = [];
    } else cell += c;
  }
  row.push(cell);
  if (row.some((x) => x.trim() !== "")) rows.push(row);
  return rows;
}

/** HEADERS: accepted column names (lower case) for each field. */
const HEADERS: Record<string, keyof Product> = {
  code: "code", sku: "code", id: "code", product_code: "code",
  name: "name", title: "name", product_name: "name",
  url: "url", link: "url", product_url: "url",
  image_url: "image_url", image: "image_url", image_link: "image_url",
  price: "price", sale_price: "sale_price", currency: "currency", category: "category", product_type: "category",
  in_stock: "in_stock", availability: "in_stock", stock: "in_stock",
};

const num = (s: string) => {
  const t = s.replace(/[^\d.-]/g, "");
  return t === "" || Number.isNaN(Number(t)) ? undefined : Number(t);
};

/** inStock reads yes/no, true/false, 1/0, "in stock" / "out of stock" and quantities. */
export function inStock(s: string): boolean {
  const t = s.trim().toLowerCase();
  if (t === "") return true;
  if (/^(no|false|0|n|out[ _]of[ _]stock|sold out|unavailable)$/.test(t)) return false;
  const n = Number(t);
  return Number.isNaN(n) ? true : n > 0;
}

/** toProducts maps CSV rows (the first is the header) to products, with the rows that can't be. */
export function toProducts(rows: string[][]): { products: Product[]; problems: string[]; unknown: string[] } {
  const problems: string[] = [];
  if (rows.length < 2) return { products: [], problems: ["The file needs a header row and at least one product."], unknown: [] };
  // own(): a column named "constructor" or "toString" is unknown, not Object's.
  const head = rows[0].map((h) => own(HEADERS, String(h ?? "").trim().toLowerCase().replace(/\s+/g, "_")));
  const unknown = rows[0].filter((_, i) => !head[i]).map((h) => String(h ?? "").trim()).filter(Boolean);
  if (!head.includes("code") || !head.includes("name")) return { products: [], problems: ["The header needs a code (or sku) and a name (or title) column."], unknown };
  const products: Product[] = [];
  const seen = new Set<string>();
  rows.slice(1).forEach((r, i) => {
    const p: Product = { code: "", name: "", in_stock: true };
    head.forEach((k, j) => {
      const v = (r[j] ?? "").trim();
      if (!k || v === "") return;
      if (k === "price" || k === "sale_price") p[k] = num(v);
      else if (k === "in_stock") p.in_stock = inStock(v);
      else if (k === "currency") p.currency = v.toUpperCase().slice(0, 3);
      else (p as unknown as Record<string, string>)[k] = v;
    });
    if (!p.code || !p.name) { if (problems.length < 20) problems.push(`Row ${i + 2}: needs a code and a name.`); return; }
    if (seen.has(p.code)) { if (problems.length < 20) problems.push(`Row ${i + 2}: ${p.code} is in the file twice (the first is kept).`); return; }
    if (p.image_url && !p.image_url.startsWith("https://")) delete p.image_url;
    seen.add(p.code);
    products.push(p);
  });
  return { products, problems, unknown };
}

/** chunks splits a list for upload. */
export function chunks<T>(list: T[], size = 1000): T[][] {
  const out: T[][] = [];
  const n = Number.isFinite(size) && size >= 1 ? Math.floor(size) : 1000; // 0, a negative or NaN size would never end
  for (let i = 0; i < list.length; i += n) out.push(list.slice(i, i + n));
  return out;
}

/** priceText shows a product's price (the sale price struck through the regular one). */
export function priceText(p: Pick<Product, "price" | "sale_price" | "currency">): string {
  const f = (n: number) => (p.currency ? `${p.currency} ` : "") + n.toLocaleString("en-IN");
  if (p.sale_price != null && p.price != null && p.sale_price < p.price) return `${f(p.sale_price)} (was ${f(p.price)})`;
  const v = p.sale_price ?? p.price;
  return v == null ? "" : f(v);
}
