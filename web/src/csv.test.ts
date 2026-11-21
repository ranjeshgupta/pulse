import { describe, expect, it } from "vitest";
import { csvCell, csvFileName, csvSafe, toCsv } from "./csv";
import { reportRows } from "./reportBuilder";
import type { ReportSpec } from "./api";

describe("csv", () => {
  it("makes formula-like cells text, keeping numbers", () => {
    for (const v of ["=SUM(A1)", "+cmd", "-x", "@me", "\tx", "\rx"]) expect(csvSafe(v)).toBe("'" + v);
    for (const v of ["-5", "+1.5", "-0.25", "plain", "", "a=b"]) expect(csvSafe(v)).toBe(v);
  });
  it("sees formulas behind leading spaces and line breaks", () => {
    for (const v of [" =1+1", "\n=cmd|' /C calc'!A0", "\r\n+SUM(A1)", "  @x", "\u00a0=1", " -5"]) expect(csvSafe(v)).toBe("'" + v);
    for (const v of ["   ", " Pune", "\n\nnote"]) expect(csvSafe(v)).toBe(v);
  });
  it("quotes cells with commas, quotes and line breaks", () => {
    expect(csvCell("a,b")).toBe('"a,b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("two\nlines")).toBe('"two\nlines"');
    expect(csvCell("=1,2")).toBe(`"'=1,2"`);
    expect(csvCell(42)).toBe("42");
    expect(toCsv([["a", 1], ["b,c", -2]])).toBe('a,1\r\n"b,c",-2\r\n');
  });
  it("names the file after the report and the day", () => {
    expect(csvFileName("Revenue by product", new Date(2026, 10, 1))).toBe("revenue-by-product-2026-11-01.csv");
    expect(csvFileName("  ", new Date(2026, 0, 9))).toBe("report-2026-01-09.csv");
    expect(csvFileName("Café / Q4 — Pune", new Date(2026, 0, 9))).toBe("cafe-q4-pune-2026-01-09.csv");
  });
});

describe("report export rows", () => {
  const spec = (p: Partial<ReportSpec>): ReportSpec => ({ source: "events", metric: { type: "count" }, group_by: { type: "none" }, chart: "table", ...p } as ReportSpec);
  it("exports a single figure", () => {
    expect(reportRows(spec({}), { rows: [{ group: "", value: 12 }], labels: {} })).toEqual([["Events"], [12]]);
  });
  it("exports grouped rows with labels, ISO days and rounded sums", () => {
    const s = spec({ metric: { type: "sum", key: "amount" }, group_by: { type: "day" } });
    expect(reportRows(s, { rows: [{ group: "2026-11-03T00:00:00Z", value: 10.456 }], labels: {} })).toEqual([["Day", "Sum of amount"], ["2026-11-03", 10.46]]);
    const c = spec({ source: "messages", group_by: { type: "campaign" } });
    expect(reportRows(c, { rows: [{ group: "journey|4", value: 3 }], labels: { "journey|4": "=Welcome" } })).toEqual([["Campaign", "Type", "Messages"], ["=Welcome", "Journey", 3]]);
  });
});
