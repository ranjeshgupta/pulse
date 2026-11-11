// Shared pieces of the Analyze pages (funnels, cohorts, revenue, reports): date ranges, the segment
// picker, property filter rows and number formatting.
import { useEffect, useState } from "react";
import type { PropFilter } from "./api";
import { ATTR_OPS, NUMERIC_OPS, newWhere, type AttrOp, type Where } from "./segmentRules";
import { Field } from "./ui";
import { useSegments } from "./pages/Segments";
import "./insights.css";

export const EVENT_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;
export const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;
export const MAX_FILTERS = 5;

export const count = (n: number) => n.toLocaleString("en-IN");

/** pct formats a 0-1 share: "24%", "3.5%". */
export function pct(x: number) {
  const p = x * 100;
  return `${p > 0 && p < 10 ? p.toFixed(1) : Math.round(p)}%`;
}

/** money formats an amount with the workspace's currency code when it has one. */
export function money(n: number, currency?: string) {
  if (currency) {
    try { return n.toLocaleString("en-IN", { style: "currency", currency, maximumFractionDigits: 2 }); } catch { /* unknown code */ }
  }
  return n.toLocaleString("en-IN", { maximumFractionDigits: 2 });
}

// ---- date range ----

export type Preset = 7 | 30 | 90;
/** from/to are YYYY-MM-DD, used when preset is custom. */
export type Range = { preset: Preset | "custom"; from: string; to: string };

export const ymd = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
export const daysAgo = (n: number) => new Date(Date.now() - n * 86_400_000);
export const defaultRange = (preset: Preset = 30): Range => ({ preset, from: ymd(daysAgo(preset)), to: ymd(new Date()) });

export function rangeProblem(r: Range): string | null {
  if (r.preset !== "custom") return null;
  if (!r.from || !r.to) return "Pick both dates.";
  if (r.from > r.to) return "The start date is after the end date.";
  if (new Date(r.to).getTime() - new Date(r.from).getTime() > 366 * 86_400_000) return "Pick a range of a year or less.";
  return null;
}

/** rangeBounds turns the range into ISO timestamps. "Now" is rounded to the minute so the request,
 * and its cache key, stay the same while you edit. */
export function rangeBounds(r: Range): { from: string; to: string } {
  const now = new Date(Math.floor(Date.now() / 60_000) * 60_000);
  if (r.preset !== "custom") return { from: new Date(now.getTime() - r.preset * 86_400_000).toISOString(), to: now.toISOString() };
  const end = new Date(`${r.to}T23:59:59`);
  return { from: new Date(`${r.from}T00:00:00`).toISOString(), to: (end > now ? now : end).toISOString() };
}

const PRESETS: Preset[] = [7, 30, 90];

export function RangePicker({ range, onChange, label = "Date range" }: { range: Range; onChange: (r: Range) => void; label?: string }) {
  const today = ymd(new Date());
  const earliest = ymd(daysAgo(366));
  return (
    <div>
      <p className="small muted" style={{ margin: "0 0 6px" }}>{label}</p>
      <div className="funnel-range" role="group" aria-label={label}>
        {PRESETS.map((p) => (
          <button key={p} type="button" aria-pressed={range.preset === p} onClick={() => onChange({ ...range, preset: p })}>Last {p} days</button>
        ))}
        <button type="button" aria-pressed={range.preset === "custom"} onClick={() => onChange({ ...range, preset: "custom" })}>Custom</button>
      </div>
      {range.preset === "custom" && (
        <div className="funnel-dates">
          <Field label="From"><input className="input" type="date" min={earliest} max={today} value={range.from} onChange={(e) => onChange({ ...range, from: e.target.value })} /></Field>
          <Field label="To"><input className="input" type="date" min={earliest} max={today} value={range.to} onChange={(e) => onChange({ ...range, to: e.target.value })} /></Field>
        </div>
      )}
    </div>
  );
}

export function SegmentPicker({ value, onChange, label = "Only contacts in" }: { value: number; onChange: (id: number) => void; label?: string }) {
  const segs = useSegments(0).data?.segments ?? [];
  const lists = segs.filter((s) => s.kind === "static");
  const rules = segs.filter((s) => s.kind !== "static");
  return (
    <Field label={label} hint={value ? "Counts contacts who are in it right now." : undefined}>
      <select className="select" value={value || ""} onChange={(e) => onChange(Number(e.target.value) || 0)}>
        <option value="">Everyone</option>
        {lists.length > 0 && <optgroup label="Static segments">{lists.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
        {rules.length > 0 && <optgroup label="Segments">{rules.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}</optgroup>}
      </select>
    </Field>
  );
}

// ---- property filters (as in segment event rules) ----

/** CompareFields is the operator picker and value input of one filter. */
export function CompareFields({ op, value, onChange, ops = ATTR_OPS }: { op: AttrOp; value: string; onChange: (op: AttrOp, value: string) => void; ops?: { op: AttrOp; label: string }[] }) {
  return (
    <>
      <select className="select" value={op} onChange={(e) => onChange(e.target.value as AttrOp, value)} aria-label="Comparison">
        {ops.map((o) => <option key={o.op} value={o.op}>{o.label}</option>)}
      </select>
      {op !== "exists" && (
        <input className="input" placeholder={op === "in" ? "Pune, Mumbai" : "value"} value={value} onChange={(e) => onChange(op, e.target.value)} aria-label="Value" />
      )}
    </>
  );
}

/** Ops for event property filters: relative dates ("within the last N days") don't apply to events. */
export const PROP_OPS = ATTR_OPS.filter((o) => o.op !== "within_days" && o.op !== "older_than_days");

export function compareText(v: unknown) {
  return Array.isArray(v) ? v.join(", ") : v === undefined || v === null ? "" : String(v);
}

/** toFilter converts an edited filter to the API's shape (same conversion as segment filters). */
export function toFilter(w: Where): PropFilter {
  const prop = w.prop.trim();
  switch (w.op) {
    case "exists": return { prop, op: w.op };
    case "in": return { prop, op: w.op, value: w.value.split(",").map((s) => s.trim()).filter(Boolean) };
    case "gt": case "gte": case "lt": case "lte": case "within_days": case "older_than_days": return { prop, op: w.op, value: Number(w.value) };
    default: return { prop, op: w.op, value: w.value };
  }
}

export const fromFilter = (f: PropFilter): Where => ({ prop: f.prop, op: f.op as AttrOp, value: compareText(f.value) });

/** filterProblems lists what's wrong with edited filters (what: "Step 2", "Filter"...). */
export function filterProblems(what: string, ws: Where[]): string[] {
  const out: string[] = [];
  if (ws.length > MAX_FILTERS) out.push(`${what}: use at most ${MAX_FILTERS} filters.`);
  for (const w of ws) {
    if (!KEY_RE.test(w.prop.trim())) out.push(`${what}: enter a property name, e.g. amount.`);
    else if ((NUMERIC_OPS.includes(w.op) || w.op === "within_days" || w.op === "older_than_days") && (w.value.trim() === "" || isNaN(Number(w.value)))) out.push(`${what}: "${w.prop}" needs a number.`);
    else if (w.op === "in" && w.value.split(",").every((v) => !v.trim())) out.push(`${what}: list at least one value, separated by commas.`);
  }
  return out;
}

/** FilterRows edits up to MAX_FILTERS property (or attribute) filters. */
export function FilterRows({ where, onChange, noun = "property", addLabel = "Only count events where…", ops = PROP_OPS }: {
  where: Where[]; onChange: (w: Where[]) => void; noun?: string; addLabel?: string; ops?: { op: AttrOp; label: string }[];
}) {
  const set = (i: number, w: Where) => onChange(where.map((x, j) => (j === i ? w : x)));
  return (
    <>
      {where.map((w, i) => (
        <div className="rule-row rule-where" key={i}>
          <span className="small">{i === 0 ? "where" : "and"}</span>
          <input className="input" placeholder={`${noun}, e.g. ${noun === "attribute" ? "plan" : "amount"}`} value={w.prop} onChange={(e) => set(i, { ...w, prop: e.target.value })} aria-label={noun === "attribute" ? "Attribute" : "Event property"} />
          <CompareFields op={w.op} value={w.value} ops={ops} onChange={(op, value) => set(i, { ...w, op, value })} />
          <button className="btn ghost sm" onClick={() => onChange(where.filter((_, j) => j !== i))} aria-label="Remove filter">Remove</button>
        </div>
      ))}
      {where.length < MAX_FILTERS && (
        <button className="btn ghost sm rule-where-add" onClick={() => onChange([...where, newWhere()])}>{addLabel}</button>
      )}
    </>
  );
}

/** useDebounced returns value once it has stopped changing for ms. */
export function useDebounced<T>(value: T, ms = 400): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}
