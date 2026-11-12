// Pure logic behind the on-site audience editors: dynamic content variants (ordered: the first
// matching variant is shown) and focus items (ordered per kind). Mirrors internal/onsite/variants.go
// so the editor can warn about variants that can never be shown before the slot is saved.
import type { Device, DWCShadow, DWCVariant, OnsiteAudience, OnsiteCondition, Overlap } from "./api";

export const DEVICES: { value: Device; label: string }[] = [
  { value: "desktop", label: "Desktop" },
  { value: "mobile", label: "Mobile" },
  { value: "tablet", label: "Tablet" },
];

/** The most variants a slot may have, and contact-field rules per audience (server limits). */
export const MAX_VARIANTS = 20;
export const MAX_CONDITIONS = 5;

/** Comparisons offered for contact-field rules: the ops of segment attribute rules. */
export const CONDITION_OPS: { op: string; label: string; value: "none" | "one" | "list" | "number" }[] = [
  { op: "eq", label: "is", value: "one" },
  { op: "ne", label: "is not", value: "one" },
  { op: "contains", label: "contains", value: "one" },
  { op: "not_contains", label: "doesn't contain", value: "one" },
  { op: "starts_with", label: "starts with", value: "one" },
  { op: "ends_with", label: "ends with", value: "one" },
  { op: "in", label: "is one of", value: "list" },
  { op: "not_in", label: "is not one of", value: "list" },
  { op: "gt", label: "is greater than", value: "number" },
  { op: "gte", label: "is at least", value: "number" },
  { op: "lt", label: "is less than", value: "number" },
  { op: "lte", label: "is at most", value: "number" },
  { op: "has", label: "has", value: "one" },
  { op: "not_has", label: "doesn't have", value: "one" },
  { op: "exists", label: "is set", value: "none" },
  { op: "not_exists", label: "is not set", value: "none" },
];
export const conditionOp = (op: string) => CONDITION_OPS.find((o) => o.op === op) ?? CONDITION_OPS[0];

/** setConditionOp changes a rule's comparison, reshaping its value to fit (list, number or none). */
export function setConditionOp(c: OnsiteCondition, op: string): OnsiteCondition {
  const shape = conditionOp(op).value;
  if (shape === "none") return { prop: c.prop, op };
  const raw = Array.isArray(c.value) ? c.value.join(", ") : c.value == null ? "" : String(c.value);
  return { prop: c.prop, op, value: parseConditionValue(op, raw) };
}

/** parseConditionValue turns what was typed into the value the server compares with. */
export function parseConditionValue(op: string, raw: string): unknown {
  switch (conditionOp(op).value) {
    case "none": return undefined;
    case "list": return splitList(raw);
    case "number": return raw.trim() !== "" && !Number.isNaN(Number(raw)) ? Number(raw) : raw;
    default: return raw;
  }
}

/** conditionValueText is a rule's value as typed (lists comma-separated). */
export const conditionValueText = (c: OnsiteCondition) => (Array.isArray(c.value) ? c.value.join(", ") : c.value == null ? "" : String(c.value));

/** splitList reads a comma-separated input: trimmed, no empty items. */
export const splitList = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

/** normaliseVariant gives a variant an audience, folding a pre-M6 segment_id into segment_ids. */
export function normaliseVariant(v: DWCVariant | (Omit<DWCVariant, "audience"> & { audience?: OnsiteAudience | null })): DWCVariant {
  const aud: OnsiteAudience = { ...(v.audience ?? {}) };
  const ids = [...(aud.segment_ids ?? [])];
  if (v.segment_id && v.segment_id > 0 && !ids.includes(v.segment_id)) ids.push(v.segment_id);
  aud.segment_ids = ids;
  const out: DWCVariant = { ...v, audience: aud };
  delete out.segment_id;
  return out;
}

/** cleanAudience drops empty rule lists (and blank page rules) before saving or counting. */
export function cleanAudience(a: OnsiteAudience): OnsiteAudience {
  const out: OnsiteAudience = {};
  if (a.segment_ids?.length) out.segment_ids = a.segment_ids;
  if (a.exclude_segment_ids?.length) out.exclude_segment_ids = a.exclude_segment_ids;
  const inc = (a.url_contains ?? []).map((s) => s.trim()).filter(Boolean);
  const exc = (a.url_not_contains ?? []).map((s) => s.trim()).filter(Boolean);
  if (inc.length) out.url_contains = inc;
  if (exc.length) out.url_not_contains = exc;
  if (a.devices?.length) out.devices = a.devices;
  if (a.conditions?.length) out.conditions = a.conditions;
  return out;
}

/** moveVariant moves the item at from to index to (drag and drop: it lands where it was dropped). */
export function moveVariant<T>(list: readonly T[], from: number, to: number): T[] {
  const out = [...list];
  if (from < 0 || from >= out.length || to < 0 || to >= out.length || from === to) return out;
  const [item] = out.splice(from, 1);
  out.splice(to, 0, item);
  return out;
}

/** moveBy moves an item one place up (-1) or down (+1); at either end nothing changes. */
export const moveBy = <T,>(list: readonly T[], i: number, dir: -1 | 1): T[] => moveVariant(list, i, i + dir);

/** reorder applies a new order (indexes into list) — e.g. focus items sorted for saving. */
export function reorder<T>(list: readonly T[], order: number[]): T[] {
  return order.map((i) => list[i]);
}

/** isEveryone: an audience with no rules at all (it takes every visitor). */
export function isEveryone(a: OnsiteAudience | undefined): boolean {
  const x = a ?? {};
  return !x.segment_ids?.length && !x.exclude_segment_ids?.length && !x.url_contains?.length && !x.url_not_contains?.length && !x.devices?.length && !x.conditions?.length;
}

const subset = <T,>(a: readonly T[], b: readonly T[]) => a.every((x) => b.includes(x));
// Key order follows the Go struct (prop, op, value), so equality matches the server's JSON check.
const condKey = (c: OnsiteCondition) => JSON.stringify(c.value === undefined ? { prop: c.prop, op: c.op } : { prop: c.prop, op: c.op, value: c.value });

/** covers reports whether every visitor matching b also matches a, judged from the rules alone. */
export function covers(a: OnsiteAudience, b: OnsiteAudience): boolean {
  const aSeg = a.segment_ids ?? [], bSeg = b.segment_ids ?? [];
  if (aSeg.length > 0 && (bSeg.length === 0 || !subset(bSeg, aSeg))) return false;
  if (!subset(a.exclude_segment_ids ?? [], b.exclude_segment_ids ?? [])) return false;
  const aInc = a.url_contains ?? [], bInc = b.url_contains ?? [];
  if (aInc.length > 0) {
    if (bInc.length === 0) return false;
    if (!bInc.every((pb) => aInc.some((pa) => pb.includes(pa)))) return false;
  }
  const bExc = b.url_not_contains ?? [];
  if (!(a.url_not_contains ?? []).every((pa) => bExc.some((pb) => pa.includes(pb)))) return false;
  const aDev = a.devices ?? [], bDev = b.devices ?? [];
  if (aDev.length > 0 && (bDev.length === 0 || !subset(bDev, aDev))) return false;
  const bConds = (b.conditions ?? []).map(condKey);
  if (!(a.conditions ?? []).every((ca) => bConds.includes(condKey(ca)))) return false;
  return true;
}

/** localShadows lists the variants that can never be shown (an earlier one takes everyone they
 * target) and whether an "everyone" variant hides the default content. */
export function localShadows(auds: OnsiteAudience[]): { shadowed: DWCShadow[]; defaultNeverShown: boolean } {
  const shadowed: DWCShadow[] = [];
  for (let j = 0; j < auds.length; j++) {
    for (let i = 0; i < j; i++) {
      if (covers(auds[i], auds[j])) {
        shadowed.push({ index: j, by: i, note: `Never shown: everyone it targets sees variant ${i + 1} first. Move it up or narrow variant ${i + 1}.` });
        break;
      }
    }
  }
  return { shadowed, defaultNeverShown: auds.some(isEveryone) };
}

/** firstEveryone is the index of the first variant with no rules (-1 if none). */
export const firstEveryone = (auds: OnsiteAudience[]) => auds.findIndex(isEveryone);

const joinOr = (xs: string[]) => (xs.length <= 1 ? xs.join("") : `${xs.slice(0, -1).join(", ")} or ${xs[xs.length - 1]}`);

/** describeCondition: a contact-field rule in words ("city is Pune"). */
export function describeCondition(c: OnsiteCondition): string {
  const o = conditionOp(c.op);
  if (o.value === "none") return `${c.prop} ${o.label}`;
  const v = Array.isArray(c.value) ? joinOr(c.value.map(String)) : String(c.value ?? "");
  return `${c.prop} ${o.label} ${v}`;
}

/** describeAudience says who an audience reaches, in one plain sentence. */
export function describeAudience(a: OnsiteAudience | undefined, segmentNames: Record<number, string> | Map<number, string> = {}): string {
  const x = a ?? {};
  if (isEveryone(x)) return "Everyone";
  const name = (id: number) => (segmentNames instanceof Map ? segmentNames.get(id) : segmentNames[id]) ?? `segment ${id}`;
  const parts: string[] = [];
  parts.push(x.segment_ids?.length ? `Members of ${joinOr(x.segment_ids.map(name))}` : "Everyone");
  if (x.exclude_segment_ids?.length) parts.push(`not in ${joinOr(x.exclude_segment_ids.map(name))}`);
  if (x.url_contains?.length) parts.push(`on pages containing ${joinOr(x.url_contains)}`);
  if (x.url_not_contains?.length) parts.push(`not on pages containing ${joinOr(x.url_not_contains)}`);
  if (x.devices?.length) parts.push(`on ${joinOr(x.devices.map((d) => DEVICES.find((o) => o.value === d)?.label.toLowerCase() ?? d))}`);
  if (x.conditions?.length) parts.push(`whose ${x.conditions.map(describeCondition).join(" and ")}`);
  return parts.join(", ");
}

export const variantLabel = (i: number, name?: string) => (name?.trim() ? `${i + 1}. ${name.trim()}` : `Variant ${i + 1}`);

export type OverlapRow = { index: number; label: string; matching: number; sees: number; approximate: boolean; hidden: boolean; shadowedBy?: number };

/** overlapRows turns an Overlap into one row per variant, in order. */
export function overlapRows(o: Overlap, fallbackNames: string[] = []): OverlapRow[] {
  const shadows = new Map((o.shadowed ?? []).map((s) => [s.index, s.by]));
  return (o.variants ?? []).map((v) => {
    const label = variantLabel(v.index, o.names?.[v.index] || fallbackNames[v.index]);
    return { index: v.index, label, matching: v.matching, sees: v.sees, approximate: !!v.approximate, hidden: shadows.has(v.index), shadowedBy: shadows.get(v.index) };
  });
}

/** overlapPairs lists the pairs of variants that share contacts, most shared first. */
export function overlapPairs(o: Overlap, fallbackNames: string[] = []): { a: string; b: string; both: number }[] {
  const label = (i: number) => variantLabel(i, o.names?.[i] || fallbackNames[i]);
  return (o.pairs ?? []).filter((p) => p.both > 0).sort((x, y) => y.both - x.both || x.a - y.a || x.b - y.b)
    .map((p) => ({ a: label(p.a), b: label(p.b), both: p.both }));
}
