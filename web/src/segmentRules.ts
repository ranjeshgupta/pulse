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

export type AttrOp = "eq" | "ne" | "contains" | "in" | "gt" | "gte" | "lt" | "lte" | "exists";
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
      out.push(...compareProblems(w.prop || "property", w.op, w.value));
    }
    return out;
  }
  const out: string[] = [];
  if (!KEY_RE.test(n.attr.trim())) out.push("Enter an attribute name, e.g. city.");
  out.push(...compareProblems(n.attr || "attribute", n.op, n.value));
  return out;
}

function compareProblems(name: string, op: AttrOp, value: string): string[] {
  if (NUMERIC_OPS.includes(op) && (value.trim() === "" || isNaN(Number(value)))) return [`"${name}" needs a number.`];
  if (op === "in" && value.split(",").every((s) => !s.trim())) return ["List at least one value, separated by commas."];
  return [];
}

// describe renders a rule as a sentence for lists and the journey canvas. names maps segment ids
// to names, so references read as words.
export function describe(r: Rule | null, names: Record<number, string> = {}): string {
  if (!r) return "added by hand or import";
  if ("all" in r) return r.all.map((c) => describeNested(c, names)).join(" and ");
  if ("any" in r) return r.any.map((c) => describeNested(c, names)).join(" or ");
  if ("segment" in r) return `${r.op === "in" ? "in" : "not in"} ${names[r.segment] ? `“${names[r.segment]}”` : `segment ${r.segment}`}`;
  if ("tag" in r) return `${r.op === "has" ? "tagged" : "not tagged"} ${r.tag}`;
  if ("event" in r) {
    const ev = r.where?.length ? `${r.event} where ${r.where.map((f) => describeCompare(f.prop, f.op, f.value)).join(" and ")}` : r.event;
    if (r.op === "eq" && r.count === 0) return `never did ${ev} in ${r.days} days`;
    const op = EVENT_OPS.find((o) => o.op === r.op)?.label ?? r.op;
    return `did ${ev} ${op} ${r.count}× in ${r.days} days`;
  }
  return describeCompare(r.attr, r.op, r.value);
}

function describeCompare(name: string, op: string, value: unknown) {
  if (op === "exists") return `${name} is set`;
  const label = ATTR_OPS.find((o) => o.op === op)?.label ?? op;
  return `${name} ${label} ${compareText(value)}`;
}

function describeNested(r: Rule, names: Record<number, string>) {
  return "all" in r || "any" in r ? `(${describe(r, names)})` : describe(r, names);
}

/** segmentRefs lists the segments and lists a rule references. */
export function segmentRefs(r: Rule | null): number[] {
  if (!r) return [];
  if ("all" in r) return r.all.flatMap(segmentRefs);
  if ("any" in r) return r.any.flatMap(segmentRefs);
  return "segment" in r ? [r.segment] : [];
}
