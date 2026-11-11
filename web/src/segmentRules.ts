// Editing model for segment rules. The builder edits a tree of groups and conditions in a
// form-friendly shape; toRule/fromRule convert to and from the API's rule JSON, and treeReducer
// applies edits by path (so nested groups stay pure data).
import type { PropFilter, Rule } from "./api";

/** A property filter on an event condition, edited like an attribute condition. values holds the
 * list for "is one of" / "is not one of"; value2 the upper bound of "is between". */
export type Where = { prop: string; op: AttrOp; value: string; value2?: string; values?: string[] };

export type AttrCond = { kind: "attr"; attr: string; op: AttrOp; value: string; value2?: string; values?: string[] };
/** An event condition counts over the last `days`, or (with from) an absolute range of UTC days,
 * from–to both included; an empty to means up to today. */
export type EventCond = { kind: "event"; event: string; op: EventOp; count: number; days: number; from?: string; to?: string; where: Where[] };
export type Cond =
  | AttrCond
  | EventCond
  | { kind: "segment"; segment: number; op: "in" | "not_in" }
  | { kind: "tag"; tag: string; op: "has" | "not_has" };
/** A group matches when all, any, or none of its filters do ("none" excludes). */
export type Match = "all" | "any" | "none";
export type Group = { kind: "group"; match: Match; children: Node[] };
export type Node = Cond | Group;

/** Comparison ops. "empty" / "not_empty" are the builder's words for eq "" / ne "" (a missing
 * attribute reads as empty). */
export type AttrOp =
  | "eq" | "ne" | "contains" | "not_contains" | "starts_with" | "ends_with" | "in" | "not_in"
  | "gt" | "gte" | "lt" | "lte" | "between" | "exists" | "not_exists" | "empty" | "not_empty" | "has" | "not_has" | DateOp;
/** Date comparisons: on/before/after a calendar date, or relative to today in whole days. */
export type DateOp = "on" | "before" | "after" | "within_days" | "older_than_days";
export type EventOp = "gte" | "gt" | "eq" | "lt" | "lte" | "none";

/** The comparisons offered by the report, funnel and cohort editors (they share Where). */
export const ATTR_OPS: { op: AttrOp; label: string }[] = [
  { op: "eq", label: "is" },
  { op: "ne", label: "is not" },
  { op: "contains", label: "contains" },
  { op: "in", label: "is one of" },
  { op: "gt", label: "is greater than" },
  { op: "gte", label: "is at least" },
  { op: "lt", label: "is less than" },
  { op: "lte", label: "is at most" },
  { op: "exists", label: "is set" },
];

/** Every comparison's words, for menus and sentences. */
export const OP_LABELS: Record<AttrOp, string> = {
  eq: "is", ne: "is not", contains: "contains", not_contains: "doesn't contain", starts_with: "starts with", ends_with: "ends with",
  in: "is one of", not_in: "is not one of", gt: "is greater than", gte: "is at least", lt: "is less than", lte: "is at most",
  between: "is between", exists: "is set", not_exists: "is not set", empty: "is empty", not_empty: "is not empty",
  on: "is on", before: "is before", after: "is after", within_days: "is in the last", older_than_days: "is more than",
  has: "has", not_has: "doesn't have",
};

/** The kind of value a field holds; it decides the operators and the value input. list: a set of
 * values (has / doesn't have, like tags); enum: one of a field's options (a dropdown). */
export type FieldType = "text" | "number" | "boolean" | "date" | "list" | "enum";

/** Operators the segment builder offers for each field type, in menu order. */
export const OPS_BY_TYPE: Record<FieldType, AttrOp[]> = {
  text: ["eq", "ne", "contains", "not_contains", "starts_with", "ends_with", "in", "not_in", "empty", "not_empty"],
  number: ["eq", "ne", "gt", "gte", "lt", "lte", "between", "empty", "not_empty"],
  boolean: ["eq", "ne", "exists", "not_exists"],
  date: ["within_days", "older_than_days", "on", "before", "after", "between", "empty", "not_empty"],
  list: ["has", "not_has", "empty", "not_empty"],
  enum: ["eq", "ne", "in", "not_in", "empty", "not_empty"],
};
/** Event properties can't compare with today: their event's own window covers recency. */
export const PROP_OPS_BY_TYPE: Record<FieldType, AttrOp[]> = {
  ...OPS_BY_TYPE,
  text: [...OPS_BY_TYPE.text, "exists", "not_exists"],
  date: ["on", "before", "after", "between", "exists", "not_exists"],
  list: ["has", "not_has", "exists", "not_exists"],
  enum: [...OPS_BY_TYPE.enum, "exists", "not_exists"],
};

/** opsFor lists the operators for a field type, keeping a saved rule's op visible if it isn't offered. */
export function opsFor(type: FieldType, current?: AttrOp, prop = false): AttrOp[] {
  const ops = (prop ? PROP_OPS_BY_TYPE : OPS_BY_TYPE)[type];
  return current && !ops.includes(current) ? [...ops, current] : ops;
}

/** valueShape says which value input an op needs. */
export function valueShape(op: AttrOp): "none" | "one" | "list" | "range" | "date" | "days" {
  switch (op) {
    case "has": case "not_has": return "one";
    case "exists": case "not_exists": case "empty": case "not_empty": return "none";
    case "in": case "not_in": return "list";
    case "between": return "range";
    case "on": case "before": case "after": return "date";
    case "within_days": case "older_than_days": return "days";
  }
  return "one";
}

export const EVENT_OPS: { op: EventOp; label: string }[] = [
  { op: "gte", label: "at least" },
  { op: "gt", label: "more than" },
  { op: "eq", label: "exactly" },
  { op: "lt", label: "fewer than" },
  { op: "lte", label: "at most" },
  { op: "none", label: "never" },
];

export const NUMERIC_OPS: AttrOp[] = ["gt", "gte", "lt", "lte"];

/** Date comparisons, worded for the builder. "suffix" follows the value input. */
export const DATE_OPS: { op: DateOp; label: string; suffix?: string }[] = [
  { op: "before", label: "is before" },
  { op: "after", label: "is after" },
  { op: "within_days", label: "is in the last", suffix: "days" },
  { op: "older_than_days", label: "is more than", suffix: "days ago" },
];
export const isDateOp = (op: string): op is DateOp => op === "on" || DATE_OPS.some((o) => o.op === op);
/** Relative date ops compare with today; event properties can't use them. */
export const RELATIVE_DATE_OPS: AttrOp[] = ["within_days", "older_than_days"];
/** The longest relative window, in days (the backend allows 1-3650). */
export const MAX_DATE_DAYS = 3650;
/** The backend's limits: rules nest at most 8 levels (the top group is level 1) and hold at most 200 nodes. */
export const MAX_RULE_DEPTH = 8;
export const MAX_RULE_NODES = 200;

/** Fields Pulse knows by name: its own attributes and the standard contact fields. group is the
 * picker group; pick marks fields chosen from the workspace's own stages, companies or users. */
export type FieldInfo = { key: string; label: string; type: FieldType; group: string; hint: string; pick?: "stage" | "company" | "owner" };
export const FIELDS: FieldInfo[] = [
  // Contact (standard fields your data fills)
  { key: "email", label: "Email", type: "text", group: "contact", hint: "Their email address" },
  { key: "email_domain", label: "Email domain", type: "text", group: "contact", hint: "The part after @, e.g. gmail.com (kept by Pulse)" },
  { key: "first_name", label: "First name", type: "text", group: "contact", hint: "Standard field" },
  { key: "last_name", label: "Last name", type: "text", group: "contact", hint: "Standard field" },
  { key: "phone", label: "Mobile", type: "text", group: "contact", hint: "Phone number in E.164, e.g. +919812345678" },
  { key: "phone_country", label: "Phone country", type: "text", group: "location", hint: "From the mobile number's country code, e.g. IN (kept by Pulse)" },
  { key: "address1", label: "Address line 1", type: "text", group: "contact", hint: "Standard field" },
  { key: "locale", label: "Preferred locale", type: "text", group: "contact", hint: "e.g. en-IN, from the SDKs or your data" },
  { key: "owner_id", label: "Owner", type: "text", group: "contact", hint: "The teammate who owns the contact", pick: "owner" },
  { key: "date_added", label: "Date added", type: "date", group: "contact", hint: "When Pulse created the contact" },
  { key: "date_identified", label: "Date identified", type: "date", group: "contact", hint: "When they first had a known ID or an email" },
  { key: "last_seen", label: "Date last active", type: "date", group: "contact", hint: "The day of their latest event" },
  { key: "first_seen", label: "First seen", type: "date", group: "contact", hint: "When Pulse first saw them do anything" },
  { key: "date_modified", label: "Modified date", type: "date", group: "contact", hint: "The day their profile last changed" },
  { key: "attribution", label: "Attribution", type: "number", group: "contact", hint: "Revenue attributed to the contact, if your data sets it" },
  { key: "attribution_date", label: "Attribution date", type: "date", group: "contact", hint: "When attribution was last set, if your data sets it" },
  { key: "last_utm_source", label: "UTM source", type: "text", group: "contact", hint: "Latest campaign they arrived from (web SDK)" },
  { key: "last_utm_medium", label: "UTM medium", type: "text", group: "contact", hint: "Latest campaign they arrived from (web SDK)" },
  { key: "last_utm_campaign", label: "UTM campaign", type: "text", group: "contact", hint: "Latest campaign they arrived from (web SDK)" },
  { key: "last_utm_content", label: "UTM content", type: "text", group: "contact", hint: "Latest campaign they arrived from (web SDK)" },
  { key: "last_utm_term", label: "UTM term", type: "text", group: "contact", hint: "Latest campaign they arrived from (web SDK)" },
  { key: "first_utm_source", label: "First UTM source", type: "text", group: "contact", hint: "The campaign that first brought them" },
  { key: "first_utm_campaign", label: "First UTM campaign", type: "text", group: "contact", hint: "The campaign that first brought them" },
  // Company
  { key: "company_id", label: "Company", type: "text", group: "company", hint: "The linked company", pick: "company" },
  { key: "company_name", label: "Company name", type: "text", group: "company", hint: "The linked company's name" },
  // Location
  { key: "city", label: "City", type: "text", group: "location", hint: "From your data" },
  { key: "state", label: "State", type: "text", group: "location", hint: "From your data" },
  { key: "country", label: "Country", type: "text", group: "location", hint: "From your data" },
  { key: "zip", label: "Zip code", type: "text", group: "location", hint: "From your data" },
  { key: "geo_city", label: "City (from IP)", type: "text", group: "location", hint: "Located by the visitor's IP, e.g. Pune" },
  { key: "geo_region", label: "State or region (from IP)", type: "text", group: "location", hint: "e.g. Maharashtra" },
  { key: "geo_country", label: "Country (from IP)", type: "text", group: "location", hint: "e.g. India" },
  { key: "geo_country_code", label: "Country code (from IP)", type: "text", group: "location", hint: "e.g. IN" },
  { key: "geo_postal", label: "Postal code (from IP)", type: "text", group: "location", hint: "Located by the visitor's IP" },
  { key: "geo_timezone", label: "Time zone (from IP)", type: "text", group: "location", hint: "e.g. Asia/Kolkata" },
  // Device (last seen, from the SDKs)
  { key: "device_type", label: "Device type", type: "text", group: "device", hint: "desktop, mobile or tablet" },
  { key: "device_os", label: "Device OS", type: "text", group: "device", hint: "e.g. Android, iOS, Windows" },
  { key: "device_os_version", label: "OS version", type: "text", group: "device", hint: "e.g. 14, 17.4" },
  { key: "device_brand", label: "Device brand", type: "text", group: "device", hint: "e.g. Samsung, Apple" },
  { key: "device_model", label: "Device model", type: "text", group: "device", hint: "e.g. SM-S918B, iPhone" },
  { key: "browser", label: "Browser", type: "text", group: "device", hint: "e.g. Chrome, Safari" },
  { key: "app_version", label: "App version", type: "text", group: "device", hint: "From the mobile SDK" },
  // Consent and deliverability
  { key: "optout_email", label: "Unsubscribed from email", type: "boolean", group: "consent", hint: "Opted out of email" },
  { key: "optout_sms", label: "Unsubscribed from SMS", type: "boolean", group: "consent", hint: "Opted out of SMS" },
  { key: "optout_whatsapp", label: "Unsubscribed from WhatsApp", type: "boolean", group: "consent", hint: "Opted out of WhatsApp" },
  { key: "optout_rcs", label: "Unsubscribed from RCS", type: "boolean", group: "consent", hint: "Opted out of RCS" },
  { key: "optout_push", label: "Unsubscribed from push", type: "boolean", group: "consent", hint: "Opted out of push" },
  { key: "optout_all", label: "Unsubscribed from everything", type: "boolean", group: "consent", hint: "Opted out of every channel" },
  { key: "email_bounced", label: "Bounced: email", type: "boolean", group: "consent", hint: "Their address hard-bounced; email is suppressed" },
  { key: "sms_bounced", label: "Bounced: SMS", type: "boolean", group: "consent", hint: "Their number failed permanently; SMS is suppressed" },
  // Points and stage
  { key: "points", label: "Points", type: "number", group: "points", hint: "Their score" },
  { key: "stage", label: "Stage", type: "text", group: "points", hint: "Lifecycle stage, e.g. MQL", pick: "stage" },
  { key: "stage_changed_at", label: "Stage changed", type: "date", group: "points", hint: "When they last moved to another stage" },
];
const FIELD_BY_KEY: Record<string, FieldInfo> = Object.fromEntries(FIELDS.map((f) => [f.key, f]));
export const fieldInfo = (key: string): FieldInfo | undefined => FIELD_BY_KEY[key.trim()];

/** Attributes every profile has or Pulse fills itself (kept for the report builder's suggestions). */
export const BUILTIN_ATTRS: { key: string; label: string; date?: boolean; hint: string }[] = FIELDS.map((f) => ({
  key: f.key, label: f.label.toLowerCase(), date: f.type === "date" || undefined, hint: f.hint,
}));

/** Lower-case words for describe(), e.g. "last active". */
const ATTR_WORDS: Record<string, string> = {
  ...Object.fromEntries(FIELDS.map((f) => [f.key, f.label.toLowerCase()])),
  first_seen: "first seen", last_seen: "last active", geo_country: "country", geo_country_code: "country code", geo_region: "state or region",
  geo_city: "city (from IP)", geo_postal: "postal code", geo_timezone: "time zone", stage: "lifecycle stage", company_id: "company",
  phone: "mobile", email_bounced: "email bounced", sms_bounced: "SMS bounced",
};

export type AttrDefLike = { key: string; type: string };

/** fieldType is a field's value type: Pulse's own fields, else the attribute definition, else text. */
export function fieldType(key: string, defs: AttrDefLike[] = []): FieldType {
  const k = key.trim();
  const f = FIELD_BY_KEY[k];
  if (f) return f.type;
  const d = defs.find((x) => x.key === k)?.type;
  if (d === "number" || d === "boolean" || d === "date" || d === "list" || d === "enum") return d;
  if (k.startsWith("optout_")) return "boolean";
  return "text";
}

/** isDateAttr reports whether an attribute holds dates: a defined date attribute, or one of Pulse's date fields. */
export function isDateAttr(key: string, defs: AttrDefLike[] = []): boolean {
  return fieldType(key, defs) === "date";
}

export const newAttr = (attr = "", op: AttrOp = "eq", value = ""): AttrCond => ({ kind: "attr", attr, op, value });
export const newEvent = (): Cond => ({ kind: "event", event: "", op: "gte", count: 1, days: 30, where: [] });
export const newSegmentRef = (): Cond => ({ kind: "segment", segment: 0, op: "in" });
export const newTag = (): Cond => ({ kind: "tag", tag: "", op: "has" });
export const newWhere = (): Where => ({ prop: "", op: "eq", value: "" });
/** A new group starts empty: the builder opens its filter picker right away. */
export const newGroup = (match: Match = "all", children: Node[] = []): Group => ({ kind: "group", match, children });

const TAG_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;
const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;

/** validPath reports whether p names an event property: a name, a dotted path (product.code, up
 * to 4 parts), or a path through one list with [] (items[].code: any item). */
export function validPath(p: string): boolean {
  if (!p || p.length > 128) return false;
  const parts = p.split(".");
  if (parts.length > 4) return false;
  let lists = 0;
  for (const part of parts) {
    const key = part.endsWith("[]") ? part.slice(0, -2) : part;
    if (part.endsWith("[]")) lists++;
    if (!KEY_RE.test(key)) return false;
  }
  return lists <= 1;
}

const splitList = (value: string) => value.split(",").map((s) => s.trim()).filter(Boolean);
const listOf = (c: { value: string; values?: string[] }) => (c.values ? c.values.map((s) => s.trim()).filter(Boolean) : splitList(c.value));

// compare converts an attribute-style comparison to the API's op/value pair.
function compare(c: { op: AttrOp; value: string; value2?: string; values?: string[] }): { op: string; value?: unknown } {
  const { op, value } = c;
  switch (op) {
    case "exists": case "not_exists": return { op };
    case "empty": return { op: "eq", value: "" };
    case "not_empty": return { op: "ne", value: "" };
    case "in": case "not_in": return { op, value: listOf(c) };
    case "between": return isDay(value) && isDay(c.value2 ?? "")
      ? { op, value: [value.trim(), (c.value2 ?? "").trim()] }
      : { op, value: [Number(value), Number(c.value2 ?? "")] };
    case "gt": case "gte": case "lt": case "lte": return { op, value: Number(value) };
    case "within_days": case "older_than_days": return { op, value: Number(value) };
    case "on": case "before": case "after": return { op, value: value.trim() };
    default: return { op, value: value === "true" ? true : value === "false" ? false : value };
  }
}

function compareText(v: unknown) {
  return Array.isArray(v) ? v.join(", ") : v === undefined ? "" : String(v);
}

// fromCompare is compare's inverse for the editing model.
function fromCompare(op: string, value: unknown): { op: AttrOp; value: string; value2?: string; values?: string[] } {
  if ((op === "eq" || op === "ne") && value === "") return { op: op === "eq" ? "empty" : "not_empty", value: "" };
  if ((op === "in" || op === "not_in") && Array.isArray(value)) return { op, value: compareText(value), values: value.map(String) };
  if (op === "between" && Array.isArray(value)) return { op, value: String(value[0] ?? ""), value2: String(value[1] ?? "") };
  return { op: op as AttrOp, value: compareText(value) };
}

/** isDay reports whether v is a YYYY-MM-DD date. */
const isDay = (v: string) => /^\d{4}-\d{2}-\d{2}$/.test(v.trim());

export function toRule(n: Node): Rule {
  if (n.kind === "group") {
    const kids = n.children.map(toRule);
    return n.match === "all" ? { all: kids } : n.match === "any" ? { any: kids } : { none: kids };
  }
  if (n.kind === "segment") return { segment: n.segment, op: n.op };
  if (n.kind === "tag") return { tag: n.tag.trim(), op: n.op };
  if (n.kind === "event") {
    const where: PropFilter[] = n.where.map((w) => ({ prop: w.prop.trim(), ...compare(w) }));
    const window = n.from !== undefined ? { from: n.from.trim(), ...(n.to?.trim() ? { to: n.to.trim() } : {}) } : { days: n.days };
    const base = n.op === "none"
      ? { event: n.event.trim(), op: "eq", count: 0, ...window }
      : { event: n.event.trim(), op: n.op, count: n.count, ...window };
    return where.length ? { ...base, where } : base;
  }
  return { attr: n.attr.trim(), ...compare(n) };
}

export function fromRule(r: Rule): Node {
  if ("all" in r) return { kind: "group", match: "all", children: r.all.map(fromRule) };
  if ("any" in r) return { kind: "group", match: "any", children: r.any.map(fromRule) };
  if ("none" in r) return { kind: "group", match: "none", children: r.none.map(fromRule) };
  if ("segment" in r) return { kind: "segment", segment: r.segment, op: r.op };
  if ("tag" in r) return { kind: "tag", tag: r.tag, op: r.op };
  if ("event" in r) {
    const none = r.op === "eq" && r.count === 0;
    const where = (r.where ?? []).map((f) => ({ prop: f.prop, ...fromCompare(f.op, f.value) }));
    const window = r.from ? { days: 30, from: r.from, to: r.to ?? "" } : { days: r.days ?? 30 };
    return { kind: "event", event: r.event, op: none ? "none" : (r.op as EventOp), count: r.count, ...window, where };
  }
  return { kind: "attr", attr: r.attr, ...fromCompare(r.op, r.value) };
}

// The top level is always a group, so the builder has one consistent shape.
export function rootFromRule(r?: Rule): Group {
  if (!r) return newGroup();
  const n = fromRule(r);
  return n.kind === "group" ? n : { kind: "group", match: "all", children: [n] };
}

// ---- editing the tree by path ----

/** A node's path: child indexes from the top group ([] is the top group itself). */
export type Path = number[];
export type TreeAction =
  | { type: "add"; group: Path; node: Node }
  | { type: "replace"; path: Path; node: Node }
  | { type: "remove"; path: Path }
  | { type: "match"; group: Path; match: Match };

function updateGroup(g: Group, path: Path, fn: (g: Group) => Group): Group {
  if (path.length === 0) return fn(g);
  const [i, ...rest] = path;
  const child = g.children[i];
  if (!child || child.kind !== "group") return g;
  return { ...g, children: g.children.map((c, j) => (j === i ? updateGroup(child, rest, fn) : c)) };
}

/** treeReducer applies one edit. Removing a nested group's last filter removes the group too, so no
 * empty box is left behind; the top group may be empty (the builder then asks for a first filter). */
export function treeReducer(root: Group, a: TreeAction): Group {
  switch (a.type) {
    case "add":
      return updateGroup(root, a.group, (g) => ({ ...g, children: [...g.children, a.node] }));
    case "match":
      return updateGroup(root, a.group, (g) => ({ ...g, match: a.match }));
    case "replace": {
      if (a.path.length === 0) return a.node.kind === "group" ? a.node : root;
      const parent = a.path.slice(0, -1), i = a.path[a.path.length - 1];
      return updateGroup(root, parent, (g) => ({ ...g, children: g.children.map((c, j) => (j === i ? a.node : c)) }));
    }
    case "remove": {
      if (a.path.length === 0) return { ...root, children: [] };
      const parent = a.path.slice(0, -1), i = a.path[a.path.length - 1];
      const next = updateGroup(root, parent, (g) => ({ ...g, children: g.children.filter((_, j) => j !== i) }));
      const p = nodeAt(next, parent);
      if (parent.length > 0 && p?.kind === "group" && p.children.length === 0) return treeReducer(next, { type: "remove", path: parent });
      return next;
    }
  }
}

export function nodeAt(root: Group, path: Path): Node | undefined {
  let n: Node | undefined = root;
  for (const i of path) n = n?.kind === "group" ? n.children[i] : undefined;
  return n;
}

/** groupDepth is a group's level in the rule: the top group is 1. */
export const groupDepth = (path: Path) => path.length + 1;
/** canNest reports whether a group at path may hold another group (whose filters sit one level deeper). */
export const canNest = (path: Path) => groupDepth(path) + 2 <= MAX_RULE_DEPTH;
/** MAX_GROUP_LEVELS is how many group levels a rule can have (filters need the last level). */
export const MAX_GROUP_LEVELS = MAX_RULE_DEPTH - 1;

export function countNodes(n: Node): number {
  return n.kind === "group" ? 1 + n.children.reduce((s, c) => s + countNodes(c), 0) : 1;
}

/** depthOf is the depth of a node's deepest level when placed at level 1. */
export function depthOf(n: Node): number {
  return n.kind === "group" ? 1 + Math.max(0, ...n.children.map(depthOf)) : 1;
}

// ---- validation ----

// problems lists what must be fixed before the rule can be saved or previewed.
export function problems(n: Node): string[] {
  const out = nodeProblems(n);
  if (n.kind === "group" && countNodes(n) > MAX_RULE_NODES) out.push(`A segment can hold at most ${MAX_RULE_NODES} filters and groups.`);
  if (n.kind === "group" && depthOf(n) > MAX_RULE_DEPTH) out.push(`Groups can be nested at most ${MAX_GROUP_LEVELS} levels deep.`);
  return out;
}

function nodeProblems(n: Node): string[] {
  if (n.kind === "group") {
    if (n.children.length === 0) return ["A group needs at least one filter."];
    return n.children.flatMap(nodeProblems);
  }
  if (n.kind === "segment") return n.segment > 0 ? [] : ["Choose a segment."];
  if (n.kind === "tag") return TAG_RE.test(n.tag.trim()) ? [] : ["Enter a tag: letters, digits, - and _."];
  if (n.kind === "event") {
    const out: string[] = [];
    if (!/^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(n.event.trim())) out.push("Enter an event name, e.g. purchase.");
    if (n.from !== undefined) {
      if (!validDate(n.from) || n.from < "2000-01-01") out.push("Choose the first day of the date range.");
      else if (n.to && !validDate(n.to)) out.push("The range's last day isn't a date.");
      else if (n.to && n.to < n.from) out.push("The date range ends before it starts.");
    } else if (!(n.days >= 1 && n.days <= 365)) out.push("Event windows are 1 to 365 days.");
    if (n.op !== "none" && !(n.count >= 0)) out.push("Event counts can't be negative.");
    if (n.where.length > 5) out.push("Use at most 5 property filters per event.");
    for (const w of n.where) {
      if (!validPath(w.prop.trim())) out.push("Enter a property: a name like amount, a path like product.code, or items[].code for any item.");
      const pick = PICK_PROPS[w.prop.trim()] || (n.event.trim() === "unsubscribed" && (w.prop.trim() === "channel" || w.prop.trim() === "method"));
      if (RELATIVE_DATE_OPS.includes(w.op)) out.push(`Use "is before" or "is after" on "${w.prop || "property"}"; the event's own days window covers recency.`);
      else out.push(...compareProblems(w.prop || "property", w, !!pick));
      if (PICK_PROPS[w.prop.trim()] && (w.op === "eq" || w.op === "ne") && !w.value.trim()) out.push(`Choose ${article(pickNoun(w.prop.trim(), n.event.trim()))}.`);
    }
    return out;
  }
  const out: string[] = [];
  const key = n.attr.trim();
  if (!KEY_RE.test(key)) out.push("Enter an attribute name, e.g. city.");
  else if (key === "company_") out.push("Finish the company attribute's name, e.g. company_industry.");
  const pick = fieldInfo(key)?.pick;
  out.push(...compareProblems(fieldInfo(key)?.label ?? (n.attr || "attribute"), n, !!pick));
  if (pick && (n.op === "eq" || n.op === "ne") && !n.value.trim()) out.push(`Choose ${article(pick === "owner" ? "owner" : pick)}.`);
  return out;
}

const article = (noun: string) => `${/^[aeiou]/i.test(noun) && !/^(one|user)/i.test(noun) ? "an" : "a"} ${noun}`;

// compareProblems checks a comparison's value. picked: the value comes from a picker that reports
// its own "Choose a …".
function compareProblems(name: string, c: { op: AttrOp; value: string; value2?: string; values?: string[] }, picked = false): string[] {
  const { op, value } = c;
  // A blank "is" would quietly mean "is empty"; that has its own operator.
  if ((op === "eq" || op === "ne") && !value.trim() && !picked) return [`"${name}" needs a value (or choose "is empty").`];
  if (op === "on" || op === "before" || op === "after") return validDate(value) ? [] : [`"${name}" needs a date.`];
  if (RELATIVE_DATE_OPS.includes(op)) {
    const n = Number(value);
    return value.trim() !== "" && Number.isInteger(n) && n >= 1 && n <= MAX_DATE_DAYS ? [] : [`"${name}" needs a whole number of days, 1 to ${MAX_DATE_DAYS}.`];
  }
  const num = (v?: string) => v !== undefined && v.trim() !== "" && !isNaN(Number(v));
  if (NUMERIC_OPS.includes(op) && !num(value)) return [`"${name}" needs a number.`];
  if (op === "between" && (isDay(value) || isDay(c.value2 ?? ""))) {
    if (!validDate(value) || !validDate(c.value2 ?? "")) return [`"${name}" needs two dates.`];
    if (value > (c.value2 ?? "")) return [`"${name}": put the earlier date first.`];
  } else if (op === "between") {
    if (!num(value) || !num(c.value2)) return [`"${name}" needs two numbers.`];
    if (Number(value) > Number(c.value2)) return [`"${name}": put the smaller number first.`];
  }
  if ((op === "has" || op === "not_has") && !value.trim()) return [`"${name}" needs a value to look for.`];
  if ((op === "in" || op === "not_in") && listOf(c).length === 0) return [`"${name}": list at least one value.`];
  if ((op === "contains" || op === "not_contains" || op === "starts_with" || op === "ends_with") && !value.trim()) return [`"${name}" needs some text to look for.`];
  return [];
}

// ---- names for objects rules point at ----

/** Names for objects rules point at by id, so sentences read as words. */
export type RefNames = {
  forms?: Record<number, string>; broadcasts?: Record<number, string>; journeys?: Record<number, string>; companies?: Record<number, string>;
  templates?: Record<number, string>; assets?: Record<number, string>; pages?: Record<number, string>; users?: Record<number, string>;
};

/** Attributes whose value is picked from the workspace's stages (by name), companies or users (by id). */
export const PICK_ATTRS: Record<string, { noun: string }> = {
  stage: { noun: "stage" },
  company_id: { noun: "company" },
  owner_id: { noun: "owner" },
};

/** Event properties that hold the id of something with a name; the builder offers a picker for them. */
export const PICK_PROPS: Record<string, { noun: string; refs: keyof RefNames | null }> = {
  form_id: { noun: "form", refs: "forms" },
  broadcast_id: { noun: "campaign", refs: "broadcasts" },
  journey_id: { noun: "journey", refs: "journeys" },
  template_id: { noun: "template", refs: "templates" },
  asset_id: { noun: "asset", refs: "assets" },
  page_id: { noun: "landing page", refs: "pages" },
};

/** pickNoun words a picked property for an event: a template of an email event is "email". */
export function pickNoun(prop: string, event: string): string {
  if (prop === "template_id") {
    const ch = event.split("_")[0];
    return ({ email: "email", sms: "SMS", whatsapp: "WhatsApp message", rcs: "RCS message", push: "push notification", inbox: "app inbox message" } as Record<string, string>)[ch] ?? "template";
  }
  return PICK_PROPS[prop]?.noun ?? prop;
}

/** Events Pulse records itself, worded for sentences: did / didn't, the thing, and the property naming it.
 * one is the thing once a specific message is chosen ("the email" → the email “Welcome”). */
export type KnownEvent = { did: string; didnt: string; what: string; label: string; message?: boolean; one?: string; channel?: string };
export const KNOWN_EVENTS: Record<string, KnownEvent> = {
  page_view: { did: "visited", didnt: "didn't visit", what: "a page", label: "Visited a page" },
  session_start: { did: "started", didnt: "didn't start", what: "a browsing session", label: "Started a browsing session" },
  landing_page_viewed: { did: "visited", didnt: "didn't visit", what: "a landing page", label: "Visited a landing page" },
  form_submitted: { did: "submitted", didnt: "didn't submit", what: "a form", label: "Submitted a form" },
  asset_downloaded: { did: "downloaded", didnt: "didn't download", what: "an asset", label: "Downloaded an asset" },
  email_sent: { did: "was sent", didnt: "wasn't sent", what: "an email", label: "Was sent an email", message: true, one: "the email", channel: "email" },
  email_opened: { did: "opened", didnt: "didn't open", what: "an email", label: "Opened an email", message: true, one: "the email", channel: "email" },
  email_clicked: { did: "clicked", didnt: "didn't click", what: "a link in an email", label: "Clicked a link in an email", message: true, one: "a link in the email", channel: "email" },
  email_bounced: { did: "had", didnt: "didn't have", what: "an email bounce", label: "An email bounced", message: true },
  sms_sent: { did: "was sent", didnt: "wasn't sent", what: "an SMS", label: "Was sent an SMS", message: true, one: "the SMS", channel: "sms" },
  sms_delivered: { did: "received", didnt: "didn't receive", what: "an SMS", label: "Received an SMS", message: true, one: "the SMS", channel: "sms" },
  sms_clicked: { did: "clicked", didnt: "didn't click", what: "a link in an SMS", label: "Clicked a link in an SMS", message: true, one: "a link in the SMS", channel: "sms" },
  sms_failed: { did: "had", didnt: "didn't have", what: "an SMS fail", label: "An SMS failed", message: true },
  whatsapp_delivered: { did: "received", didnt: "didn't receive", what: "a WhatsApp message", label: "Received a WhatsApp message", message: true, one: "the WhatsApp message", channel: "whatsapp" },
  whatsapp_read: { did: "read", didnt: "didn't read", what: "a WhatsApp message", label: "Read a WhatsApp message", message: true, one: "the WhatsApp message", channel: "whatsapp" },
  rcs_read: { did: "read", didnt: "didn't read", what: "an RCS message", label: "Read an RCS message", message: true, one: "the RCS message", channel: "rcs" },
  rcs_clicked: { did: "clicked", didnt: "didn't click", what: "a link in an RCS message", label: "Clicked a link in an RCS message", message: true, one: "a link in the RCS message", channel: "rcs" },
  push_sent: { did: "was sent", didnt: "wasn't sent", what: "a push notification", label: "Was sent a push", message: true, one: "the push notification", channel: "push" },
  inbox_opened: { did: "opened", didnt: "didn't open", what: "an app inbox message", label: "Opened an app inbox message", message: true, one: "the app inbox message", channel: "inbox" },
  inbox_clicked: { did: "clicked", didnt: "didn't click", what: "an app inbox message", label: "Clicked an app inbox message", message: true, one: "the app inbox message", channel: "inbox" },
  unsubscribed: { did: "unsubscribed", didnt: "didn't unsubscribe", what: "", label: "Unsubscribed" },
  journey_entered: { did: "entered", didnt: "didn't enter", what: "a journey", label: "Entered a journey" },
  journey_completed: { did: "completed", didnt: "didn't complete", what: "a journey", label: "Completed a journey" },
  inapp_shown: { did: "saw", didnt: "didn't see", what: "an in-app message", label: "Saw an in-app message" },
  inapp_clicked: { did: "clicked", didnt: "didn't click", what: "an in-app message", label: "Clicked an in-app message" },
};

/** Properties each known event carries, for suggestions. Message events all carry broadcast_id, journey_id and template_id. */
export function eventProps(event: string): string[] {
  const own: Record<string, string[]> = {
    page_view: ["url", "path", "title"], form_submitted: ["form_id"], unsubscribed: ["channel"],
    journey_entered: ["journey_id"], journey_completed: ["journey_id"], inapp_shown: ["inapp_id"], inapp_clicked: ["inapp_id"],
    session_start: ["url", "referrer", "utm_source", "utm_campaign", "device_type", "browser"], landing_page_viewed: ["page_id", "utm_source", "utm_campaign"],
    asset_downloaded: ["asset_id"],
  };
  const extra: Record<string, string[]> = {
    page_view: ["referrer", "referrer_domain", "utm_source", "utm_medium", "utm_campaign", "device_type", "session_id"],
    unsubscribed: ["method", "source"], email_clicked: ["url"], sms_clicked: ["url"], rcs_clicked: ["url"],
  };
  if (KNOWN_EVENTS[event]?.message) return ["broadcast_id", "journey_id", "template_id", ...(extra[event] ?? [])];
  return [...(own[event] ?? []), ...(extra[event] ?? [])];
}

const CHANNEL_WORDS: Record<string, string> = { email: "email", sms: "SMS", whatsapp: "WhatsApp", rcs: "RCS", push: "push", inbox: "app inbox", all: "everything" };
const PROP_WORDS: Record<string, string> = {
  url: "URL", path: "path", title: "page title", referrer: "referrer", referrer_domain: "referring site", utm_source: "campaign source",
  utm_medium: "campaign medium", utm_campaign: "campaign", device_type: "device type", session_id: "session",
};
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** formatDay turns 2026-11-30 into "30 Nov 2026"; anything else is returned as is. */
export function formatDay(v: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
  return m && validDate(v) ? `${Number(m[3])} ${MONTHS[Number(m[2]) - 1]} ${m[1]}` : v;
}

function validDate(v: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v.trim());
  if (!m) return false;
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return d.getUTCFullYear() === Number(m[1]) && d.getUTCMonth() === Number(m[2]) - 1 && d.getUTCDate() === Number(m[3]);
}

const days = (n: number) => `${n} day${n === 1 ? "" : "s"}`;

// describe renders a rule as a sentence for lists and the journey canvas. names maps segment ids
// to names, so references read as words; refs does the same for forms, campaigns and journeys.
export function describe(r: Rule | null, names: Record<number, string> = {}, refs: RefNames = {}): string {
  if (!r) return "added by hand or import";
  if ("all" in r) return r.all.map((c) => describeNested(c, names, refs)).join(" and ");
  if ("any" in r) return r.any.map((c) => describeNested(c, names, refs)).join(" or ");
  if ("none" in r) return `none of (${r.none.map((c) => describeNested(c, names, refs)).join("; ")})`;
  if ("segment" in r) return `${r.op === "in" ? "in" : "not in"} ${names[r.segment] ? `“${names[r.segment]}”` : `segment ${r.segment}`}`;
  if ("tag" in r) return `${r.op === "has" ? "tagged" : "not tagged"} ${r.tag}`;
  if ("event" in r) {
    if (KNOWN_EVENTS[r.event]) return describeKnownEvent(r, refs);
    const ev = r.where?.length ? `${r.event} where ${r.where.map((f) => describeCompare(f.prop, f.op, f.value)).join(" and ")}` : r.event;
    if (r.op === "eq" && r.count === 0) return `never did ${ev} ${describeWindow(r, "in")}`;
    const op = EVENT_OPS.find((o) => o.op === r.op)?.label ?? r.op;
    return `did ${ev} ${op} ${r.count}× ${describeWindow(r, "in")}`;
  }
  return describeAttr(r, refs);
}

// describeAttr words attribute rules, with stages, companies and owners in their own words:
// "in stage “MQL”", "works at “Acme”", "owned by “Ravi”", "company industry is Retail".
function describeAttr(r: Extract<Rule, { attr: string }>, refs: RefNames): string {
  const v = compareText(r.value);
  if (r.attr === "stage" && (r.op === "eq" || r.op === "ne") && v) return `${r.op === "eq" ? "in" : "not in"} stage “${v}”`;
  if (r.attr === "company_id") {
    if (r.op === "exists") return "works at a company";
    if ((r.op === "eq" || r.op === "ne") && v) {
      const name = refs.companies?.[Number(v)];
      return `${r.op === "eq" ? "works at" : "doesn't work at"} ${name ? `“${name}”` : `company #${v}`}`;
    }
  }
  if (r.attr === "owner_id") {
    if (r.op === "exists") return "has an owner";
    if (r.op === "not_exists") return "has no owner";
    if ((r.op === "eq" || r.op === "ne") && v) {
      const name = refs.users?.[Number(v)];
      return `${r.op === "eq" ? "owned by" : "not owned by"} ${name ? `“${name}”` : `user #${v}`}`;
    }
  }
  const words = ATTR_WORDS[r.attr] ?? (r.attr.startsWith("company_") ? `company ${r.attr.slice(8).replace(/_/g, " ")}` : r.attr);
  return describeCompare(words, r.op, r.value);
}

// describeKnownEvent words an event Pulse records itself, e.g. "opened an email from campaign
// “Diwali sale” in the last 30 days" or "didn't visit a page whose URL contains /pricing in the last 7 days".
function describeKnownEvent(r: Extract<Rule, { event: string }>, refs: RefNames): string {
  const w = KNOWN_EVENTS[r.event];
  let what = w.what;
  const parts: string[] = [];
  const rest: string[] = [];
  for (const f of r.where ?? []) {
    const pick = PICK_PROPS[f.prop];
    if (pick && (f.op === "eq" || f.op === "ne") && f.value !== undefined && f.value !== "") {
      const id = Number(f.value);
      const name = pick.refs ? refs[pick.refs]?.[id] : undefined;
      const noun = pickNoun(f.prop, r.event);
      const label = name ? `“${name}”` : `#${compareText(f.value)}`;
      const not = f.op === "ne" ? "not " : "";
      if (f.prop === "template_id" && w.one && !not) what = `${w.one} ${label}`;
      // "a form" + form_id -> "the form “Contact us”"; message events say where the message came from.
      else if (what.replace(/^an? /, "") === noun && !not) what = `the ${noun} ${label}`;
      else parts.push(`${not}from ${noun} ${label}`);
      continue;
    }
    if (f.prop === "channel" && f.op === "eq" && r.event === "unsubscribed") {
      parts.push(`from ${CHANNEL_WORDS[String(f.value)] ?? String(f.value)}`);
      continue;
    }
    if (f.prop === "method" && f.op === "eq" && r.event === "unsubscribed") {
      parts.push(f.value === "manual" ? "manually (by your team or API)" : "by themselves");
      continue;
    }
    rest.push(describeCompare(PROP_WORDS[f.prop] ?? f.prop, f.op, f.value));
  }
  const none = r.op === "eq" && r.count === 0;
  let s = [none ? w.didnt : w.did, what, ...parts].filter(Boolean).join(" ");
  if (rest.length) s += ` ${r.event === "page_view" ? "whose" : "where"} ${rest.join(" and ")}`;
  if (!none && !(r.op === "gte" && r.count === 1)) {
    const op = EVENT_OPS.find((o) => o.op === r.op)?.label ?? r.op;
    s += ` ${op} ${r.count} time${r.count === 1 ? "" : "s"}`;
  }
  return `${s} ${describeWindow(r, "in the last")}`;
}

/** describeWindow words an event rule's window: "in the last 7 days", "between 1 Jan 2026 and 31 Mar 2026", "since 1 Jan 2026". */
export function describeWindow(r: { days?: number; from?: string; to?: string }, lead: "in" | "in the last"): string {
  if (r.from) return r.to ? `between ${formatDay(r.from)} and ${formatDay(r.to)}` : `since ${formatDay(r.from)}`;
  const n = r.days ?? 0;
  return lead === "in" ? `in ${days(n)}` : `in the last ${n === 1 ? "day" : days(n)}`;
}

/** oldRange reports whether an event window starts more than 365 days before today: such rules are
 * evaluated by the periodic sweep (every few minutes), not as each event arrives. */
export function oldRange(from: string | undefined, today = new Date()): boolean {
  if (!from || !validDate(from)) return false;
  const start = Date.UTC(Number(from.slice(0, 4)), Number(from.slice(5, 7)) - 1, Number(from.slice(8, 10)));
  const t = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate());
  return (t - start) / 86_400_000 > 364;
}

function describeCompare(name: string, op: string, value: unknown) {
  if (op === "exists") return `${name} is set`;
  if (op === "not_exists") return `${name} is not set`;
  if ((op === "eq" || op === "ne") && value === "") return `${name} ${op === "eq" ? "is empty" : "is not empty"}`;
  if ((op === "eq" || op === "ne") && typeof value === "boolean") return `${name} ${op === "eq" ? "is" : "is not"} ${value ? "yes" : "no"}`;
  if (op === "on" || op === "before" || op === "after") return `${name} is ${op} ${formatDay(compareText(value))}`;
  if (op === "within_days") return `${name} is in the last ${Number(value) === 1 ? "day" : days(Number(value))}`;
  if (op === "older_than_days") return `${name} is more than ${days(Number(value))} ago`;
  if (op === "between" && Array.isArray(value)) return `${name} is between ${formatDay(String(value[0]))} and ${formatDay(String(value[1]))}`;
  if (op === "has" || op === "not_has") return `${name} ${op === "has" ? "has" : "doesn't have"} ${compareText(value)}`;
  const label = OP_LABELS[op as AttrOp] ?? op;
  return `${name} ${label} ${compareText(value)}`;
}

function describeNested(r: Rule, names: Record<number, string>, refs: RefNames) {
  return "all" in r || "any" in r ? `(${describe(r, names, refs)})` : describe(r, names, refs);
}

/** segmentRefs lists the segments and lists a rule references. */
export function segmentRefs(r: Rule | null): number[] {
  if (!r) return [];
  if ("all" in r) return r.all.flatMap(segmentRefs);
  if ("any" in r) return r.any.flatMap(segmentRefs);
  if ("none" in r) return r.none.flatMap(segmentRefs);
  return "segment" in r ? [r.segment] : [];
}

/** SchemaPropLike is the part of an event schema's property the builder reads. */
export type SchemaPropLike = { key: string; path?: string; label?: string; type: string; values?: { value: string; label?: string }[] };

/** schemaProp finds the schema property a filter names, by its path (or its key when the path is the key). */
export function schemaProp(props: SchemaPropLike[] | undefined, prop: string): SchemaPropLike | undefined {
  const p = prop.trim();
  return props?.find((x) => (x.path || x.key) === p);
}

/** propFieldType maps a schema property's type to the builder's field type. */
export function propFieldType(t: string): FieldType {
  switch (t) {
    case "number": return "number";
    case "bool": return "boolean";
    case "date": return "date";
    case "enum": return "enum";
    case "list": return "list";
  }
  return "text";
}

/** guessPropType types a property without a schema from its name. */
export const guessPropType = (prop: string): FieldType => (/amount|price|count|total|value|qty|quantity/.test(prop) ? "number" : "text");

/** filtersFromWhere converts edited property filters to the API's (event triggers use them too). */
export const filtersFromWhere = (where: Where[]): PropFilter[] => where.map((w) => ({ prop: w.prop.trim(), ...compare(w) }));
/** whereFromFilters is filtersFromWhere's inverse. */
export const whereFromFilters = (f: PropFilter[] = []): Where[] => f.map((x) => ({ prop: x.prop, ...fromCompare(x.op, x.value) }));
/** describeWhere words property filters: "payment_method is upi and value is at least 500". */
export const describeWhere = (f: PropFilter[] = []): string => f.map((x) => describeCompare(x.prop, x.op, x.value)).join(" and ");
