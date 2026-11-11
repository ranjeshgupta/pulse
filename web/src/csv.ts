// CSV files built in the browser (report exports). Cells are made formula-safe the same way the
// server's exports are (csvSafe in internal/api): a spreadsheet must never run a cell as a formula.

const NUMBER_RE = /^[+-]?(\d+\.?\d*|\.\d+)(e[+-]?\d+)?$/i;

/** csvSafe prefixes a cell that starts with = + - @ tab or CR with a single quote, so spreadsheets
 * show it as text; plain numbers (-5, +1.5) stay numbers. */
export function csvSafe(v: string): string {
  if (v !== "" && "=+-@\t\r".includes(v[0]) && !NUMBER_RE.test(v)) return "'" + v;
  return v;
}

/** csvCell: a safe cell, quoted when it holds a comma, a quote or a line break. */
export function csvCell(v: string | number): string {
  const s = csvSafe(String(v));
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

/** toCsv joins rows of cells into CSV text (CRLF line ends, as RFC 4180 has it). */
export function toCsv(rows: (string | number)[][]): string {
  return rows.map((r) => r.map(csvCell).join(",")).join("\r\n") + "\r\n";
}

/** csvFileName: "Revenue by product" on 11 Nov 2026 → revenue-by-product-2026-11-11.csv. */
export function csvFileName(name: string, at = new Date()): string {
  const slug = name.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "report";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${slug}-${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}.csv`;
}

/** downloadCsv saves CSV text as a file (with a BOM so Excel reads UTF-8). */
export function downloadCsv(fileName: string, text: string) {
  const url = URL.createObjectURL(new Blob(["﻿" + text], { type: "text/csv;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
