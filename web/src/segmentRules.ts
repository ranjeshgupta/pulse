// Editing model for segment rules. The builder edits a tree of groups and conditions in a
// form-friendly shape; toRule/fromRule convert to and from the API's rule JSON.
import type { PropFilter, Rule } from "./api";

/** A property filter on an event condition, edited like an attribute condition. */
export type Where = { prop: string; op: AttrOp; value: string };

export type Cond =
  | { kind: "attr"; attr: string; op: AttrOp; value: string }
  | { kind: "event"; event: string; op: EventOp; count: number; days: number; where: Where[] }
  | { kind: "segment"; segment: number; op: "in" | "not_in" }
  | { kind: "tag"; tag: string; op: "has" | "not_has" };
export type Group = { kind: "group"; match: "all" | "any"; children: Node[] };
export type Node = Cond | Group;

export type AttrOp = "eq" | "ne" | "contains" | "in" | "gt" | "gte" | "lt" | "lte" | "exists" | DateOp;
/** Date comparisons: before/after a calendar date, or relative to today in whole days. */
export type DateOp = "before" | "after" | "within_days" | "older_than_days";
export type EventOp = "gte" | "gt" | "eq" | "lt" | "lte" | "none";

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
export const isDateOp = (op: string): op is DateOp => DATE_OPS.some((o) => o.op === op);
/** Relative date ops compare with today; event properties can't use them. */
export const RELATIVE_DATE_OPS: AttrOp[] = ["within_days", "older_than_days"];
/** The longest relative window, in days (the backend allows 1-3650). */
export const MAX_DATE_DAYS = 3650;

/** Attributes every profile has, filled by Pulse itself. */
export const BUILTIN_ATTRS: { key: string; label: string; date?: boolean; hint: string }[] = [
  { key: "first_seen", label: "first seen", date: true, hint: "When Pulse first saw this contact do anything" },
  { key: "last_seen", label: "last active", date: true, hint: "The day of this contact's latest event" },
  { key: "geo_country", label: "country", hint: "From the visitor's IP, e.g. India" },
  { key: "geo_country_code", label: "country code", hint: "From the visitor's IP, e.g. IN" },
  { key: "geo_region", label: "state or region", hint: "From the visitor's IP, e.g. Maharashtra" },
  { key: "geo_city", label: "city (from IP)", hint: "From the visitor's IP, e.g. Pune" },
  { key: "geo_postal", label: "postal code", hint: "From the visitor's IP" },
  { key: "geo_timezone", label: "time zone", hint: "From the visitor's IP, e.g. Asia/Kolkata" },
  { key: "stage", label: "lifecycle stage", hint: "The contact's stage, e.g. MQL" },
  { key: "stage_changed_at", label: "stage changed", date: true, hint: "When the contact last moved to another stage" },
  { key: "company_name", label: "company name", hint: "The linked company's name" },
  { key: "company_id", label: "company", hint: "The linked company" },
];
const ATTR_WORDS: Record<string, string> = Object.fromEntries(BUILTIN_ATTRS.map((a) => [a.key, a.label]));

/** isDateAttr reports whether an attribute holds dates: a defined date attribute, or first_seen/last_seen. */
export function isDateAttr(key: string, defs: { key: string; type: string }[] = []): boolean {
  const k = key.trim();
  return BUILTIN_ATTRS.some((a) => a.date && a.key === k) || defs.some((d) => d.key === k && d.type === "date");
}

export const newAttr = (): Cond => ({ kind: "attr", attr: "", op: "eq", value: "" });
export const newEvent = (): Cond => ({ kind: "event", event: "", op: "gte", count: 1, days: 30, where: [] });
export const newSegmentRef = (): Cond => ({ kind: "segment", segment: 0, op: "in" });
export const newTag = (): Cond => ({ kind: "tag", tag: "", op: "has" });
export const newWhere = (): Where => ({ prop: "", op: "eq", value: "" });

const TAG_RE = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;
const KEY_RE = /^[a-zA-Z_][a-zA-Z0-9_]{0,63}$/;

// compare converts an attribute-style comparison to the API's op/value pair.
function compare(op: AttrOp, value: string): { op: string; value?: unknown } {
  switch (op) {
    case "exists": return { op };
    case "in": return { op, value: value.split(",").map((s) => s.trim()).filter(Boolean) };
    case "gt": case "gte": case "lt": case "lte": return { op, value: Number(value) };
    case "within_days": case "older_than_days": return { op, value: Number(value) };
    case "before": case "after": return { op, value: value.trim() };
    default: return { op, value };
  }
}

function compareText(v: unknown) {
  return Array.isArray(v) ? v.join(", ") : v === undefined ? "" : String(v);
}
export const newGroup = (match: "all" | "any" = "all"): Group => ({ kind: "group", match, children: [newAttr()] });

export function toRule(n: Node): Rule {
  if (n.kind === "group") return n.match === "all" ? { all: n.children.map(toRule) } : { any: n.children.map(toRule) };
  if (n.kind === "segment") return { segment: n.segment, op: n.op };
  if (n.kind === "tag") return { tag: n.tag.trim(), op: n.op };
  if (n.kind === "event") {
    const where: PropFilter[] = n.where.map((w) => ({ prop: w.prop.trim(), ...compare(w.op, w.value) }));
    const base = n.op === "none"
      ? { event: n.event.trim(), op: "eq", count: 0, days: n.days }
      : { event: n.event.trim(), op: n.op, count: n.count, days: n.days };
    return where.length ? { ...base, where } : base;
  }
  return { attr: n.attr.trim(), ...compare(n.op, n.value) };
}

export function fromRule(r: Rule): Node {
  if ("all" in r) return { kind: "group", match: "all", children: r.all.map(fromRule) };
  if ("any" in r) return { kind: "group", match: "any", children: r.any.map(fromRule) };
  if ("segment" in r) return { kind: "segment", segment: r.segment, op: r.op };
  if ("tag" in r) return { kind: "tag", tag: r.tag, op: r.op };
  if ("event" in r) {
    const none = r.op === "eq" && r.count === 0;
    const where = (r.where ?? []).map((f) => ({ prop: f.prop, op: f.op as AttrOp, value: compareText(f.value) }));
    return { kind: "event", event: r.event, op: none ? "none" : (r.op as EventOp), count: r.count, days: r.days, where };
  }
  return { kind: "attr", attr: r.attr, op: r.op as AttrOp, value: compareText(r.value) };
}

// The top level is always a group, so the builder has one consistent shape.
export function rootFromRule(r?: Rule): Group {
  if (!r) return newGroup();
  const n = fromRule(r);
  return n.kind === "group" ? n : { kind: "group", match: "all", children: [n] };
}

// problems lists what must be fixed before the rule can be saved or previewed.
export function problems(n: Node): string[] {
  if (n.kind === "group") {
    if (n.children.length === 0) return ["A group needs at least one condition."];
    return n.children.flatMap(problems);
  }
  if (n.kind === "segment") return n.segment > 0 ? [] : ["Choose a segment or list."];
  if (n.kind === "tag") return TAG_RE.test(n.tag.trim()) ? [] : ["Enter a tag: letters, digits, - and _."];
  if (n.kind === "event") {
    const out: string[] = [];
    if (!/^[a-zA-Z][a-zA-Z0-9_.:-]{0,63}$/.test(n.event.trim())) out.push("Enter an event name, e.g. purchase.");
    if (!(n.days >= 1 && n.days <= 365)) out.push("Event windows are 1 to 365 days.");
    if (n.op !== "none" && !(n.count >= 0)) out.push("Event counts can't be negative.");
    if (n.where.length > 5) out.push("Use at most 5 property filters per event.");
    for (const w of n.where) {
      if (!KEY_RE.test(w.prop.trim())) out.push("Enter a property name, e.g. amount.");
      if (RELATIVE_DATE_OPS.includes(w.op)) out.push(`Use "is before" or "is after" on "${w.prop || "property"}"; the event's own days window covers recency.`);
      else out.push(...compareProblems(w.prop || "property", w.op, w.value));
      const pick = PICK_PROPS[w.prop.trim()];
      if (pick && (w.op === "eq" || w.op === "ne") && !w.value.trim()) out.push(`Choose a ${pick.noun}.`);
    }
    return out;
  }
  const out: string[] = [];
  if (!KEY_RE.test(n.attr.trim())) out.push("Enter an attribute name, e.g. city.");
  else if (n.attr.trim() === "company_") out.push("Finish the company attribute's name, e.g. company_industry.");
  out.push(...compareProblems(n.attr || "attribute", n.op, n.value));
  const pick = PICK_ATTRS[n.attr.trim()];
  if (pick && (n.op === "eq" || n.op === "ne") && !n.value.trim()) out.push(`Choose a ${pick.noun}.`);
  return out;
}

function compareProblems(name: string, op: AttrOp, value: string): string[] {
  if (op === "before" || op === "after") return validDate(value) ? [] : [`"${name}" needs a date.`];
  if (RELATIVE_DATE_OPS.includes(op)) {
    const n = Number(value);
    return value.trim() !== "" && Number.isInteger(n) && n >= 1 && n <= MAX_DATE_DAYS ? [] : [`"${name}" needs a whole number of days, 1 to ${MAX_DATE_DAYS}.`];
  }
  if (NUMERIC_OPS.includes(op) && (value.trim() === "" || isNaN(Number(value)))) return [`"${name}" needs a number.`];
  if (op === "in" && value.split(",").every((s) => !s.trim())) return ["List at least one value, separated by commas."];
  if (op === "contains" && !value.trim()) return [`"${name}" needs some text to look for.`];
  return [];
}

/** Names for objects rules point at by id, so sentences read as words. */
export type RefNames = { forms?: Record<number, string>; broadcasts?: Record<number, string>; journeys?: Record<number, string>; companies?: Record<number, string> };

/** Attributes whose value is picked from the workspace's stages (by name) or companies (by id). */
export const PICK_ATTRS: Record<string, { noun: string }> = {
  stage: { noun: "stage" },
  company_id: { noun: "company" },
};

/** Event properties that hold the id of something with a name; the builder offers a picker for them. */
export const PICK_PROPS: Record<string, { noun: string; refs: keyof RefNames | null }> = {
  form_id: { noun: "form", refs: "forms" },
  broadcast_id: { noun: "campaign", refs: "broadcasts" },
  journey_id: { noun: "journey", refs: "journeys" },
};

/** Events Pulse records itself, worded for sentences: did / didn't, the thing, and the property naming it. */
export const KNOWN_EVENTS: Record<string, { did: string; didnt: string; what: string; label: string; message?: boolean }> = {
  page_view: { did: "visited", didnt: "didn't visit", what: "a page", label: "Visited a page" },
  form_submitted: { did: "submitted", didnt: "didn't submit", what: "a form", label: "Submitted a form" },
  email_sent: { did: "was sent", didnt: "wasn't sent", what: "an email", label: "Was sent an email", message: true },
  email_opened: { did: "opened", didnt: "didn't open", what: "an email", label: "Opened an email", message: true },
  email_clicked: { did: "clicked", didnt: "didn't click", what: "a link in an email", label: "Clicked a link in an email", message: true },
  email_bounced: { did: "had", didnt: "didn't have", what: "an email bounce", label: "An email bounced", message: true },
  sms_sent: { did: "was sent", didnt: "wasn't sent", what: "an SMS", label: "Was sent an SMS", message: true },
  sms_delivered: { did: "received", didnt: "didn't receive", what: "an SMS", label: "Received an SMS", message: true },
  whatsapp_delivered: { did: "received", didnt: "didn't receive", what: "a WhatsApp message", label: "Received a WhatsApp message", message: true },
  whatsapp_read: { did: "read", didnt: "didn't read", what: "a WhatsApp message", label: "Read a WhatsApp message", message: true },
  rcs_read: { did: "read", didnt: "didn't read", what: "an RCS message", label: "Read an RCS message", message: true },
  push_sent: { did: "was sent", didnt: "wasn't sent", what: "a push notification", label: "Was sent a push", message: true },
  inbox_opened: { did: "opened", didnt: "didn't open", what: "an app inbox message", label: "Opened an app inbox message", message: true },
  inbox_clicked: { did: "clicked", didnt: "didn't click", what: "an app inbox message", label: "Clicked an app inbox message", message: true },
  unsubscribed: { did: "unsubscribed", didnt: "didn't unsubscribe", what: "", label: "Unsubscribed" },
  journey_entered: { did: "entered", didnt: "didn't enter", what: "a journey", label: "Entered a journey" },
  journey_completed: { did: "completed", didnt: "didn't complete", what: "a journey", label: "Completed a journey" },
  inapp_shown: { did: "saw", didnt: "didn't see", what: "an in-app message", label: "Saw an in-app message" },
  inapp_clicked: { did: "clicked", didnt: "didn't click", what: "an in-app message", label: "Clicked an in-app message" },
};

/** Properties each known event carries, for suggestions. Message events all carry broadcast_id and journey_id. */
export function eventProps(event: string): string[] {
  const own: Record<string, string[]> = {
    page_view: ["url", "path", "title"], form_submitted: ["form_id"], unsubscribed: ["channel"],
    journey_entered: ["journey_id"], journey_completed: ["journey_id"], inapp_shown: ["inapp_id"], inapp_clicked: ["inapp_id"],
  };
  return KNOWN_EVENTS[event]?.message ? ["broadcast_id", "journey_id", "template_id"] : own[event] ?? [];
}

const CHANNEL_WORDS: Record<string, string> = { email: "email", sms: "SMS", whatsapp: "WhatsApp", rcs: "RCS", push: "push", inbox: "app inbox", all: "everything" };
const PROP_WORDS: Record<string, string> = { url: "URL", path: "path", title: "title" };
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
  if ("segment" in r) return `${r.op === "in" ? "in" : "not in"} ${names[r.segment] ? `“${names[r.segment]}”` : `segment ${r.segment}`}`;
  if ("tag" in r) return `${r.op === "has" ? "tagged" : "not tagged"} ${r.tag}`;
  if ("event" in r) {
    if (KNOWN_EVENTS[r.event]) return describeKnownEvent(r, refs);
    const ev = r.where?.length ? `${r.event} where ${r.where.map((f) => describeCompare(f.prop, f.op, f.value)).join(" and ")}` : r.event;
    if (r.op === "eq" && r.count === 0) return `never did ${ev} in ${r.days} days`;
    const op = EVENT_OPS.find((o) => o.op === r.op)?.label ?? r.op;
    return `did ${ev} ${op} ${r.count}× in ${r.days} days`;
  }
  return describeAttr(r, refs);
}

// describeAttr words attribute rules, with stages and companies in their own words:
// "in stage “MQL”", "works at “Acme”", "company industry is Retail".
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
      const obj = name ? `${pick.noun} “${name}”` : `${pick.noun} #${compareText(f.value)}`;
      const not = f.op === "ne" ? "not " : "";
      // "a form" + form_id -> "the form “Contact us”"; message events say where the message came from.
      if (what === `a ${pick.noun}` && !not) what = `the ${obj}`;
      else parts.push(`${not}from ${obj}`);
      continue;
    }
    if (f.prop === "channel" && f.op === "eq" && r.event === "unsubscribed") {
      parts.push(`from ${CHANNEL_WORDS[String(f.value)] ?? String(f.value)}`);
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
  return `${s} in the last ${r.days === 1 ? "day" : days(r.days)}`;
}

function describeCompare(name: string, op: string, value: unknown) {
  if (op === "exists") return `${name} is set`;
  if (op === "before" || op === "after") return `${name} is ${op} ${formatDay(compareText(value))}`;
  if (op === "within_days") return `${name} is in the last ${Number(value) === 1 ? "day" : days(Number(value))}`;
  if (op === "older_than_days") return `${name} is more than ${days(Number(value))} ago`;
  const label = ATTR_OPS.find((o) => o.op === op)?.label ?? op;
  return `${name} ${label} ${compareText(value)}`;
}

function describeNested(r: Rule, names: Record<number, string>, refs: RefNames) {
  return "all" in r || "any" in r ? `(${describe(r, names, refs)})` : describe(r, names, refs);
}

// ---- presets: ready-made conditions the "Add condition" menu inserts ----

export type Preset = { id: string; label: string; hint: string; make: () => Node };
const ev = (event: string, days: number, where: Where[] = [], op: EventOp = "gte"): Cond => ({ kind: "event", event, op, count: 1, days, where });
const pickWhere = (prop: string): Where[] => [{ prop, op: "eq", value: "" }];

/** PRESETS groups the conditions a marketer usually wants, in their words. Each inserts a rule to tweak. */
export const PRESETS: { group: string; items: Preset[] }[] = [
  {
    group: "Profile",
    items: [
      { id: "attr", label: "Profile attribute", hint: "Any field on the contact, e.g. plan is pro", make: newAttr },
      { id: "first_seen", label: "First seen", hint: "New contacts, e.g. first seen in the last 7 days", make: () => ({ kind: "attr", attr: "first_seen", op: "within_days", value: "7" }) },
      { id: "last_active", label: "Last active", hint: "Recently active, e.g. in the last 30 days", make: () => ({ kind: "attr", attr: "last_seen", op: "within_days", value: "30" }) },
      { id: "inactive", label: "Inactive for a while", hint: "Last active more than 30 days ago", make: () => ({ kind: "attr", attr: "last_seen", op: "older_than_days", value: "30" }) },
    ],
  },
  {
    group: "Location",
    items: [
      { id: "country", label: "Country", hint: "From the visitor's IP address", make: () => ({ kind: "attr", attr: "geo_country", op: "eq", value: "" }) },
      { id: "region", label: "State or region", hint: "From the visitor's IP address", make: () => ({ kind: "attr", attr: "geo_region", op: "eq", value: "" }) },
      { id: "city", label: "City", hint: "From the visitor's IP address", make: () => ({ kind: "attr", attr: "geo_city", op: "eq", value: "" }) },
    ],
  },
  {
    group: "Behaviour",
    items: [
      { id: "did", label: "Did an event", hint: "Counted over the last N days", make: newEvent },
      { id: "didnt", label: "Didn't do an event", hint: "Never did it in the last N days", make: () => ev("", 30, [], "none") },
      { id: "page", label: "Visited a page", hint: "A page whose URL contains some text", make: () => ev("page_view", 30, [{ prop: "url", op: "contains", value: "" }]) },
      { id: "form", label: "Submitted a form", hint: "One of your forms", make: () => ev("form_submitted", 90, pickWhere("form_id")) },
    ],
  },
  {
    group: "Messages",
    items: [
      { id: "email_opened", label: "Opened an email from a campaign", hint: "A broadcast you sent", make: () => ev("email_opened", 30, pickWhere("broadcast_id")) },
      { id: "email_clicked", label: "Clicked an email from a campaign", hint: "Clicked any link in it", make: () => ev("email_clicked", 30, pickWhere("broadcast_id")) },
      { id: "email_opened_journey", label: "Opened an email from a journey", hint: "Any email the journey sent", make: () => ev("email_opened", 30, pickWhere("journey_id")) },
      { id: "email_not_opened", label: "Didn't open an email from a campaign", hint: "Good for resending", make: () => ev("email_opened", 30, pickWhere("broadcast_id"), "none") },
      { id: "whatsapp_read", label: "Read a WhatsApp message", hint: "From a campaign or a journey", make: () => ev("whatsapp_read", 30, pickWhere("broadcast_id")) },
      { id: "sms_delivered", label: "Received an SMS", hint: "Delivered, from a campaign or a journey", make: () => ev("sms_delivered", 30, pickWhere("broadcast_id")) },
      { id: "unsubscribed", label: "Unsubscribed from a channel", hint: "Email, SMS, WhatsApp and more", make: () => ev("unsubscribed", 365, [{ prop: "channel", op: "eq", value: "email" }]) },
      { id: "bounced", label: "An email bounced", hint: "Their address may be bad", make: () => ev("email_bounced", 90) },
    ],
  },
  {
    group: "Journeys",
    items: [
      { id: "journey_entered", label: "Entered a journey", hint: "Started it in the last N days", make: () => ev("journey_entered", 90, pickWhere("journey_id")) },
      { id: "journey_completed", label: "Completed a journey", hint: "Reached its end", make: () => ev("journey_completed", 90, pickWhere("journey_id")) },
    ],
  },
  {
    group: "Lifecycle and accounts",
    items: [
      { id: "stage", label: "In stage", hint: "A lifecycle stage, e.g. MQL", make: () => ({ kind: "attr", attr: "stage", op: "eq", value: "" }) },
      { id: "stage_recent", label: "Changed stage recently", hint: "Moved to their stage in the last 7 days", make: () => ({ kind: "attr", attr: "stage_changed_at", op: "within_days", value: "7" }) },
      { id: "company", label: "Works at company", hint: "Linked to one of your companies", make: () => ({ kind: "attr", attr: "company_id", op: "eq", value: "" }) },
      { id: "company_attr", label: "Company attribute", hint: "A field of their company, e.g. industry is Retail", make: () => ({ kind: "attr", attr: "company_", op: "eq", value: "" }) },
    ],
  },
  {
    group: "Tags and lists",
    items: [
      { id: "tag", label: "Has a tag", hint: "Or doesn't have it", make: newTag },
      { id: "segment", label: "In a segment or list", hint: "Or not in it", make: newSegmentRef },
    ],
  },
];

/** segmentRefs lists the segments and lists a rule references. */
export function segmentRefs(r: Rule | null): number[] {
  if (!r) return [];
  if ("all" in r) return r.all.flatMap(segmentRefs);
  if ("any" in r) return r.any.flatMap(segmentRefs);
  return "segment" in r ? [r.segment] : [];
}
