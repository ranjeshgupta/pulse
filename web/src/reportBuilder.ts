// Editing model for custom reports (the Reports page). Pure: no React, no DOM.
// The editor keeps a Draft (filters in the form-friendly Where shape); toSpec/fromSpec convert to and
// from the API's ReportSpec. specProblems mirrors the server's rules so we only run valid specs.
import { CHANNELS, CHANNEL_NAMES, type Channel, type PropFilter, type ReportGroup, type ReportMetric, type ReportResult, type ReportSource, type ReportSpec } from "./api";
import { dict, own } from "./safe";
import { ATTR_OPS, DATE_OPS, NUMERIC_OPS, RELATIVE_DATE_OPS, type AttrOp, type Where } from "./segmentRules";

export const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;
export const EVENT_RE = /^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/;
export const MAX_EVENTS = 10;
export const MAX_WHERE = 5;
/** Grouping by day works for ranges up to this many days. */
export const MAX_DAY_RANGE = 100;

export type Chart = ReportSpec["chart"];
export type MessageSource = NonNullable<ReportSpec["message_source"]>;

export interface Draft {
  source: ReportSource;
  metric: { type: ReportMetric; key: string };
  group: { type: ReportGroup; key: string };
  events: string[];
  where: Where[];
  message_source: MessageSource;
  channels: Channel[];
  kinds: string[];
  segment_id: number;
  chart: Chart;
  range_days?: number;
}

// ---- what each source allows ----

type Option<T> = { type: T; label: string; needsKey?: boolean; keyHint?: string };

export const SOURCES: { type: ReportSource; label: string; hint: string }[] = [
  { type: "events", label: "Events", hint: "Events your contacts did, e.g. purchase." },
  { type: "messages", label: "Messages", hint: "What happened to campaign messages. Test sends aren't counted." },
  { type: "contacts", label: "Contacts", hint: "Contact profiles and their attributes." },
];

export const METRICS: Record<ReportSource, Option<ReportMetric>[]> = {
  events: [
    { type: "count", label: "Number of events" },
    { type: "unique_contacts", label: "Unique contacts" },
    { type: "sum", label: "Sum of a property", needsKey: true, keyHint: "numeric property, e.g. amount" },
    { type: "avg", label: "Average of a property", needsKey: true, keyHint: "numeric property, e.g. amount" },
  ],
  messages: [
    { type: "count", label: "Number of messages" },
    { type: "unique_contacts", label: "Unique contacts" },
  ],
  contacts: [
    { type: "count", label: "Number of contacts" },
    { type: "sum", label: "Sum of an attribute", needsKey: true, keyHint: "numeric attribute, e.g. points" },
    { type: "avg", label: "Average of an attribute", needsKey: true, keyHint: "numeric attribute, e.g. points" },
  ],
};

const TIME: Option<ReportGroup>[] = [
  { type: "day", label: "Day" },
  { type: "week", label: "Week" },
  { type: "month", label: "Month" },
];

export const GROUPS: Record<ReportSource, Option<ReportGroup>[]> = {
  events: [
    { type: "none", label: "Nothing (one total)" },
    { type: "event", label: "Event name" },
    { type: "prop", label: "Event property", needsKey: true, keyHint: "property, e.g. sku" },
    { type: "attr", label: "Contact attribute", needsKey: true, keyHint: "attribute, e.g. plan" },
    ...TIME,
  ],
  messages: [
    { type: "none", label: "Nothing (one total)" },
    { type: "kind", label: "Outcome" },
    { type: "channel", label: "Channel" },
    { type: "campaign", label: "Campaign" },
    ...TIME,
  ],
  contacts: [
    { type: "none", label: "Nothing (one total)" },
    { type: "attr", label: "Attribute", needsKey: true, keyHint: "attribute, e.g. geo_country" },
    { type: "day", label: "Day first seen" },
    { type: "week", label: "Week first seen" },
    { type: "month", label: "Month first seen" },
  ],
};

export const MESSAGE_KINDS = ["sent", "delivered", "read", "failed", "fallback", "suppressed", "capped", "deferred", "opened", "clicked", "bounced", "unsubscribed"] as const;
export const KIND_NAMES: Record<string, string> = dict({
  sent: "Sent", delivered: "Delivered", read: "Read", failed: "Failed", fallback: "Fell back", suppressed: "Suppressed",
  capped: "Capped", deferred: "Deferred", opened: "Opened", clicked: "Clicked", bounced: "Bounced", unsubscribed: "Unsubscribed",
});

/** Attribute filter ops for contacts: the attribute ops plus date comparisons. */
export const CONTACT_OPS: { op: AttrOp; label: string }[] = [
  ...ATTR_OPS,
  ...DATE_OPS.map((o) => ({ op: o.op as AttrOp, label: o.suffix ? `${o.label} … ${o.suffix}` : o.label })),
];

export const isTimeGroup = (g: ReportGroup) => g === "day" || g === "week" || g === "month";
export const metricOption = (s: ReportSource, m: ReportMetric) => own(METRICS, s)?.find((o) => o.type === m);
export const groupOption = (s: ReportSource, g: ReportGroup) => own(GROUPS, s)?.find((o) => o.type === g);

// ---- drafts ----

export function newDraft(source: ReportSource = "events"): Draft {
  return {
    source, metric: { type: "count", key: "" }, group: { type: "none", key: "" },
    events: [], where: [], message_source: "", channels: [], kinds: [], segment_id: 0, chart: "bars",
  };
}

/** withSource switches the source, resetting the metric, grouping and filters to valid ones. The
 * segment, chart and saved range carry over (line becomes bars, since the group resets to none). */
export function withSource(d: Draft, source: ReportSource): Draft {
  if (d.source === source) return d;
  return { ...newDraft(source), segment_id: d.segment_id, chart: d.chart === "line" ? "bars" : d.chart, range_days: d.range_days };
}

/** withGroup sets the grouping; a line chart needs time groups, so it falls back to bars. */
export function withGroup(d: Draft, type: ReportGroup, key = d.group.key): Draft {
  const chart = d.chart === "line" && !isTimeGroup(type) ? "bars" : d.chart;
  return { ...d, group: { type, key: groupOption(d.source, type)?.needsKey ? key : "" }, chart };
}

export function withMetric(d: Draft, type: ReportMetric, key = d.metric.key): Draft {
  return { ...d, metric: { type, key: metricOption(d.source, type)?.needsKey ? key : "" } };
}

// ---- filters ----

function compare(op: AttrOp, value: string): { op: string; value?: unknown } {
  switch (op) {
    case "exists": return { op };
    case "in": return { op, value: value.split(",").map((s) => s.trim()).filter(Boolean) };
    case "gt": case "gte": case "lt": case "lte": case "within_days": case "older_than_days": return { op, value: Number(value) };
    case "before": case "after": return { op, value: value.trim() };
    default: return { op, value };
  }
}

export const toFilter = (w: Where): PropFilter => ({ prop: w.prop.trim(), ...compare(w.op, w.value) });
export const fromFilter = (f: PropFilter): Where => ({
  prop: f.prop, op: f.op as AttrOp, value: Array.isArray(f.value) ? f.value.join(", ") : f.value === undefined || f.value === null ? "" : String(f.value),
});

function whereProblems(source: ReportSource, ws: Where[]): string[] {
  const noun = source === "contacts" ? "attribute" : "property";
  const out: string[] = [];
  if (ws.length > MAX_WHERE) out.push(`Use at most ${MAX_WHERE} filters.`);
  for (const w of ws) {
    const name = w.prop.trim() || noun;
    if (!KEY_RE.test(w.prop.trim())) { out.push(`Enter the filter's ${noun} name, e.g. ${source === "contacts" ? "plan" : "amount"}.`); continue; }
    if (source !== "contacts" && RELATIVE_DATE_OPS.includes(w.op)) out.push(`Event properties can't use "${w.op}"; use "is before" or "is after".`);
    else if (w.op === "before" || w.op === "after") { if (!/^\d{4}-\d{2}-\d{2}/.test(w.value.trim())) out.push(`"${name}" needs a date, e.g. 2026-01-31.`); }
    else if (RELATIVE_DATE_OPS.includes(w.op)) {
      const n = Number(w.value);
      if (w.value.trim() === "" || !Number.isInteger(n) || n < 1 || n > 3650) out.push(`"${name}" needs a whole number of days.`);
    } else if (NUMERIC_OPS.includes(w.op) && (w.value.trim() === "" || isNaN(Number(w.value)))) out.push(`"${name}" needs a number.`);
    else if (w.op === "in" && w.value.split(",").every((v) => !v.trim())) out.push(`"${name}": list at least one value, separated by commas.`);
  }
  return out;
}

// ---- spec conversion ----

/** toSpec builds the API spec: only the fields that apply to the source, and only non-empty ones. */
export function toSpec(d: Draft): ReportSpec {
  const m = metricOption(d.source, d.metric.type);
  const g = groupOption(d.source, d.group.type);
  const spec: ReportSpec = {
    source: d.source,
    metric: m?.needsKey ? { type: d.metric.type, key: d.metric.key.trim() } : { type: d.metric.type },
    group_by: g?.needsKey ? { type: d.group.type, key: d.group.key.trim() } : { type: d.group.type },
    chart: d.chart,
  };
  if (d.source === "events" && d.events.length) spec.events = d.events.map((e) => e.trim());
  if (d.source !== "messages" && d.where.length) spec.where = d.where.map(toFilter);
  if (d.source === "messages") {
    if (d.message_source) spec.message_source = d.message_source;
    if (d.channels.length) spec.channels = [...d.channels];
    if (d.kinds.length) spec.kinds = [...d.kinds];
  }
  if (d.segment_id) spec.segment_id = d.segment_id;
  if (d.range_days) spec.range_days = d.range_days;
  return spec;
}

export function fromSpec(s: ReportSpec): Draft {
  const d = newDraft(s.source);
  return {
    ...d,
    metric: { type: s.metric.type, key: s.metric.key ?? "" },
    group: { type: s.group_by.type, key: s.group_by.key ?? "" },
    events: s.events ?? [],
    where: (s.where ?? []).map(fromFilter),
    message_source: s.message_source ?? "",
    channels: s.channels ?? [],
    kinds: s.kinds ?? [],
    segment_id: s.segment_id ?? 0,
    chart: s.chart ?? "bars",
    range_days: s.range_days,
  };
}

/** specProblems lists what the server would reject. rangeDays is the length of the chosen range. */
export function specProblems(d: Draft, rangeDays: number): string[] {
  const out: string[] = [];
  const m = metricOption(d.source, d.metric.type);
  const g = groupOption(d.source, d.group.type);
  if (!m) out.push(`${SOURCES.find((s) => s.type === d.source)?.label} reports can't show "${d.metric.type}".`);
  else if (m.needsKey && !KEY_RE.test(d.metric.key.trim())) out.push(`Enter the ${d.source === "contacts" ? "attribute" : "property"} to ${d.metric.type === "sum" ? "add up" : "average"}, e.g. ${d.source === "contacts" ? "points" : "amount"}.`);
  if (!g) out.push(`${SOURCES.find((s) => s.type === d.source)?.label} reports can't be grouped by "${d.group.type}".`);
  else if (g.needsKey && !KEY_RE.test(d.group.key.trim())) out.push(`Enter the ${d.group.type === "attr" ? "attribute" : "property"} to group by, e.g. ${d.group.type === "attr" ? "plan" : "sku"}.`);
  if (d.chart === "line" && !isTimeGroup(d.group.type)) out.push("A line chart needs grouping by day, week or month.");
  if (d.group.type === "day" && rangeDays > MAX_DAY_RANGE) out.push(`Group by week or month for ranges over ${MAX_DAY_RANGE} days.`);
  if (d.source === "events") {
    if (d.events.length > MAX_EVENTS) out.push(`Pick at most ${MAX_EVENTS} events.`);
    for (const e of d.events) if (!EVENT_RE.test(e.trim())) out.push(`"${e}" isn't a valid event name.`);
  }
  if (d.source !== "messages") out.push(...whereProblems(d.source, d.where));
  if (d.source === "messages") {
    for (const c of d.channels) if (!CHANNELS.includes(c)) out.push(`Unknown channel "${c}".`);
    for (const k of d.kinds) if (!(MESSAGE_KINDS as readonly string[]).includes(k)) out.push(`Unknown outcome "${k}".`);
  }
  return [...new Set(out)];
}

// ---- describing ----

function list(xs: string[], max = 3) {
  return xs.length <= max ? xs.join(", ") : `${xs.slice(0, max).join(", ")} +${xs.length - max} more`;
}

/** describe sums up a spec in one line, e.g. "Sum of amount of purchase, by sku · bars". */
export function describe(s: ReportSpec): string {
  const key = s.metric.key || "?";
  let what: string;
  if (s.source === "events") {
    const of = s.events?.length ? list(s.events) : "";
    what = own({
      count: of ? `Count of ${of}` : "Count of events",
      unique_contacts: of ? `Contacts who did ${of}` : "Unique contacts",
      sum: `Sum of ${key}${of ? ` of ${of}` : ""}`,
      avg: `Average ${key}${of ? ` of ${of}` : ""}`,
    }, s.metric.type) ?? "Events";
  } else if (s.source === "messages") {
    what = s.metric.type === "unique_contacts" ? "Contacts messaged" : "Messages";
    if (s.kinds?.length) what += ` ${list(s.kinds.map((k) => own(KIND_NAMES, k)?.toLowerCase() ?? k))}`;
    if (s.message_source) what += s.message_source === "broadcast" ? " from one-time campaigns" : " from journeys";
    if (s.channels?.length) what += ` via ${list(s.channels.map((c) => own(CHANNEL_NAMES, c) ?? c))}`;
  } else {
    what = own({ count: "Contacts", unique_contacts: "Contacts", sum: `Sum of ${key}`, avg: `Average ${key}` }, s.metric.type) ?? "Contacts";
  }
  if (s.where?.length) what += ` (${s.where.length} ${s.where.length === 1 ? "filter" : "filters"})`;
  const g = s.group_by;
  const by: Partial<Record<ReportGroup, string>> = {
    event: "by event", prop: `by ${g.key || "?"}`, attr: `by ${g.key || "?"}`, kind: "by outcome", channel: "by channel",
    campaign: "by campaign", day: "per day", week: "per week", month: "per month",
  };
  if (g.type === "none") return `${what} · total`;
  return `${what}, ${own(by, g.type) ?? `by ${g.type}`} · ${s.chart}`;
}

// ---- starting points ----

export const PRESETS: { id: string; name: string; hint: string; spec: ReportSpec }[] = [
  {
    id: "revenue-by-product", name: "Revenue by product", hint: "Sum of amount of purchase events, by sku",
    spec: { source: "events", metric: { type: "sum", key: "amount" }, group_by: { type: "prop", key: "sku" }, events: ["purchase"], chart: "bars", range_days: 30 },
  },
  {
    id: "purchases-per-day", name: "Purchases per day", hint: "Purchase events each day",
    spec: { source: "events", metric: { type: "count" }, group_by: { type: "day" }, events: ["purchase"], chart: "line", range_days: 30 },
  },
  {
    id: "email-outcomes", name: "Email outcomes by campaign", hint: "Email messages of each one-time campaign",
    spec: { source: "messages", metric: { type: "count" }, group_by: { type: "campaign" }, message_source: "broadcast", channels: ["email"], chart: "bars", range_days: 30 },
  },
  {
    id: "contacts-by-country", name: "Contacts by country", hint: "All contacts, by the country of their IP",
    spec: { source: "contacts", metric: { type: "count" }, group_by: { type: "attr", key: "geo_country" }, chart: "bars" },
  },
];

// ---- results ----

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** groupLabel names a result group: labels from the server first, "(not set)" for "". */
export function groupLabel(group: string, g: ReportGroup, labels: Record<string, string> = {}): string {
  group = group == null ? "" : String(group);
  if (group === "") return "(not set)";
  // Groups are values from people's data (a sku may well be "constructor"): own keys only.
  const label = own(labels, group);
  if (label) return label;
  if (g === "channel") return own(CHANNEL_NAMES, group) ?? group;
  if (g === "kind") return own(KIND_NAMES, group) ?? group;
  if (g === "campaign") {
    const [kind, id] = group.split("|");
    return `${kind === "journey" ? "Journey" : "Broadcast"} #${id ?? "?"}`;
  }
  if (isTimeGroup(g)) {
    const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(group);
    if (!m) return group;
    const [, y, mo, d] = m;
    const mon = MONTHS[Number(mo) - 1];
    if (!mon) return group;
    if (g === "month") return `${mon} ${y}`;
    return `${g === "week" ? "Week of " : ""}${Number(d)} ${mon} ${y}`;
  }
  return group;
}

/** shortTimeLabel is a compact axis label for a time group: "3 Nov" or "Nov 26". */
export function shortTimeLabel(group: string, g: ReportGroup): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(group);
  if (!m) return group;
  const mon = MONTHS[Number(m[2]) - 1];
  if (!mon) return group;
  return g === "month" ? `${mon} ${m[1].slice(2)}` : `${Number(m[3])} ${mon}`;
}

/** campaignKind is "One-time" or "Journey" for a campaign group ("broadcast|12"). */
export const campaignKind = (group: string) => (group.startsWith("journey|") ? "Journey" : group.startsWith("broadcast|") ? "One-time" : "");

/** formatValue: whole counts for count metrics, up to 2 decimals for sums and averages. */
export function formatValue(v: number, metric: ReportMetric): string {
  if (typeof v !== "number" || !Number.isFinite(v)) return "–";
  if (metric === "sum" || metric === "avg") return v.toLocaleString("en-IN", { maximumFractionDigits: 2 });
  return Math.round(v).toLocaleString("en-IN");
}

/** metricLabel heads the value column. */
export function metricLabel(s: Pick<ReportSpec, "source" | "metric">): string {
  const k = s.metric.key || "value";
  switch (s.metric.type) {
    case "sum": return `Sum of ${k}`;
    case "avg": return `Average ${k}`;
    case "unique_contacts": return "Contacts";
    default: return s.source === "events" ? "Events" : s.source === "messages" ? "Messages" : "Contacts";
  }
}

/** reportRows: a report result as table rows for a CSV export, header first. Values stay numbers
 * (sums and averages to 2 decimals); time groups use ISO dates so spreadsheets read them as dates. */
export function reportRows(spec: ReportSpec, r: Pick<ReportResult, "rows" | "labels">): (string | number)[][] {
  const g = spec.group_by.type;
  const value = (v: number): string | number => (typeof v !== "number" || !Number.isFinite(v) ? "" : spec.metric.type === "sum" || spec.metric.type === "avg" ? Math.round(v * 100) / 100 : Math.round(v));
  const rs = r.rows ?? [];
  if (g === "none") return [[metricLabel(spec)], [value(rs[0]?.value ?? 0)]];
  const head = groupOption(spec.source, g)?.label ?? "Group";
  const campaign = g === "campaign";
  const rows: (string | number)[][] = [[spec.group_by.key ? `${head}: ${spec.group_by.key}` : head, ...(campaign ? ["Type"] : []), metricLabel(spec)]];
  for (const x of rs) {
    const label = isTimeGroup(g) && /^\d{4}-\d{2}-\d{2}/.test(String(x.group)) ? x.group.slice(0, 10) : groupLabel(x.group, g, r.labels);
    rows.push([label, ...(campaign ? [campaignKind(x.group)] : []), value(x.value)]);
  }
  return rows;
}

/** niceMax rounds a maximum up to 1, 2, 2.5 or 5 times a power of ten (0 becomes 1). */
export function niceMax(v: number): number {
  if (!(v > 0)) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const f of [1, 2, 2.5, 5, 10]) if (v <= f * p + 1e-9 * p) return f * p;
  return 10 * p;
}

export type Box = { width: number; height: number; left: number; right: number; top: number; bottom: number };
export type LinePoint = { x: number; y: number; value: number };

/** lineGeometry places the values of a line chart in an SVG box: x spreads evenly across the plot
 * (one value sits in the middle), y runs from 0 at the bottom to a nice maximum at the top. Negative
 * values are drawn at the baseline. */
export function lineGeometry(values: number[], box: Box): { points: LinePoint[]; max: number; ticks: { y: number; value: number }[]; path: string } {
  const plotW = box.width - box.left - box.right;
  const plotH = box.height - box.top - box.bottom;
  values = values.map((v) => (Number.isFinite(v) ? v : 0)); // a missing value draws at 0, not as "NaN" in the path
  const max = niceMax(values.reduce((a, v) => Math.max(a, v), 0)); // no spread: thousands of points must not overflow the stack
  const y = (v: number) => box.top + plotH * (1 - Math.max(0, v) / max);
  const points = values.map((v, i) => ({
    x: values.length === 1 ? box.left + plotW / 2 : box.left + (plotW * i) / (values.length - 1),
    y: y(v),
    value: v,
  }));
  const ticks = [0, max / 2, max].map((value) => ({ value, y: y(value) }));
  const path = points.map((p, i) => `${i === 0 ? "M" : "L"}${round(p.x)},${round(p.y)}`).join(" ");
  return { points, max, ticks, path };
}

const round = (n: number) => Math.round(n * 10) / 10;

/** labelIndices picks up to k evenly spread indices of n points for axis labels (first and last included). */
export function labelIndices(n: number, k = 5): number[] {
  if (!(n > 0) || !Number.isFinite(n)) return [];
  n = Math.floor(n);
  k = Number.isFinite(k) ? Math.max(2, Math.floor(k)) : 5;
  if (n <= k) return Array.from({ length: n }, (_, i) => i);
  const out = new Set<number>();
  for (let i = 0; i < k; i++) out.add(Math.round((i * (n - 1)) / (k - 1)));
  return [...out];
}

/** rangeDaysOf is the number of days a picked range covers. */
export function rangeDaysOf(r: { preset: number | "custom"; from: string; to: string }): number {
  if (r.preset !== "custom") return r.preset;
  if (!r.from || !r.to) return 0;
  const n = Math.round((new Date(`${r.to}T00:00:00Z`).getTime() - new Date(`${r.from}T00:00:00Z`).getTime()) / 86_400_000) + 1;
  return Number.isFinite(n) ? n : 0;
}
